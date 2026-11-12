//go:build integration

package main

// ARS-001 §9.2 timing tests P1-P4 for video search.
//
// They run only with YUHAUL_PERF=1 (they take tens of minutes) and are meant
// for the deployment host or one of equal capacity:
//
//	YUHAUL_PERF=1 go test -tags integration -run TestVideoSearchPerf -v -timeout 4h ./cmd/server
//
// Every case goes through the real server composition (a child process, as in
// the ADR-007 composition tests) over loopback HTTP, so a time here is the
// client-observed time from request start to the last body byte; it includes
// the loopback hop that a ServeHTTP-only measurement would not. Set
// TMPDIR to a disk-backed directory: the seed database is several hundred MB
// and the test makes three copies of it.
//
// Seed (recorded seed perfSeedValue): 150,000 known videos holding a title,
// fetched over the last 29 days; 100,000 known videos whose title expired;
// 150,000 imported references; about 200 runs (20 simulations) with 1-3
// route_decision and run_video_outcome rows per video; queue items. Planted
// tokens match exactly 20,000 / 5,000 / 500 / 1 / 0 held titles, and the
// generator records the ground-truth result list for each.

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/bateau84/YuHaul/internal/videosearch"
)

const (
	perfSeedValue   = 20261112
	perfRuns        = 200
	perfProdVideos  = 1_000 // videos the P3 production run discovers
	perfP1SearchMin = 210
	perfP1ViewMin   = 200
	perfP1Limit     = time.Second
	perfP2Limit     = 2 * time.Second
	perfPageSize    = 50
)

// Seed sizes. YUHAUL_PERF_SMOKE=1 shrinks them to check the harness quickly;
// its numbers are not evidence for ARS-001.
var (
	perfKnownHeld    = 150_000
	perfKnownExpired = 100_000
	perfImported     = 150_000
)

func init() {
	if os.Getenv("YUHAUL_PERF_SMOKE") == "1" {
		perfKnownHeld, perfKnownExpired, perfImported = 3_000, 2_000, 2_000
		perfTokens[0].matches, perfTokens[1].matches, perfTokens[2].matches = 200, 50, 10
	}
}

// perfTokens are the planted words. The vocabulary of ordinary title words has
// no q, x or z, so no ordinary title can contain one by accident.
var perfTokens = []struct {
	word    string
	matches int
	label   string
}{
	{"QzxAlpha", 20_000, "20k"},
	{"QzxBeta", 5_000, "5k"},
	{"QzxGamma", 500, "500"},
	{"QzxDelta", 1, "1"},
	{"QzxNone", 0, "0"},
}

func perfNeedle(word string) string { return strings.ToLower(word) }

type perfTruth struct {
	expected map[string][]string // needle -> IDs in the §7.1 order
	held     []string
	expired  []string
	imported []string
}

func TestVideoSearchPerf(t *testing.T) {
	if os.Getenv("YUHAUL_PERF") != "1" {
		t.Skip("set YUHAUL_PERF=1 to run the ARS-001 timing tests")
	}
	dir := t.TempDir()
	seedPath := filepath.Join(dir, "seed.db")
	began := time.Now()
	truth := seedPerfDB(t, seedPath)
	t.Log(machineInfo())
	t.Logf("seeded in %s", time.Since(began).Round(time.Second))
	fi, err := os.Stat(seedPath)
	require.NoError(t, err)
	t.Logf("seed database: %d MB", fi.Size()>>20)

	st := newPerfStats()
	var p12Elapsed time.Duration

	t.Run("P1_P2_P4", func(t *testing.T) {
		dbPath := filepath.Join(dir, "p12.db")
		copyFile(t, seedPath, dbPath)
		yt := newFakeYouTube(t)
		srv := startServer(t, dbPath, yt.srv.URL, "YUHAUL_DAILY_QUOTA=100000000")
		perfWaitOverview(t, srv)
		pc := newPerfClient(srv.addr)
		rng := rand.New(rand.NewPCG(perfSeedValue, 2))

		for _, w := range []string{"qzxbeta", "qzxnone"} { // warm the process and page cache, untimed
			_, code, _, err := pc.get(searchPath(w, ""))
			require.NoError(t, err)
			require.Equal(t, 200, code)
		}
		start := time.Now()

		// P1.
		for _, r := range p1Requests(truth, rng, "P1 ") {
			pc.do(st, r)
		}
		// P2, first pages.
		for _, r := range p2FirstPageRequests(truth, "P2 ") {
			pc.do(st, r)
		}
		// P2, page >= 100, reached by following next_cursor; P4 on every page.
		cursors := map[int]string{}
		var cursor100 string
		for _, tok := range perfTokens {
			group := ""
			switch tok.label {
			case "20k":
				group = "P2 page>=100 20k"
			case "5k":
				group = "P2 page>=100 5k"
			}
			needle := perfNeedle(tok.word)
			walkToken(t, pc, st, truth, needle, group, func(n int, c string) {
				if tok.label == "5k" && n == 100 {
					cursor100 = c
				}
				if tok.label == "20k" {
					cursors[n] = c
				}
			})
			if tok.label == "5k" {
				// Page 100 is the last page of the 5k token: repeat it from its
				// cursor so there is a sample set, not one value.
				if cursor100 == "" {
					require.Equal(t, "1", os.Getenv("YUHAUL_PERF_SMOKE"), "5k token must reach page 100")
					continue
				}
				for i := 0; i < 30; i++ {
					pc.do(st, pageReq(truth, needle, cursor100, 100, group))
				}
			}
		}
		p12Elapsed = time.Since(start)
		perfSavedCursors = cursors
	})

	if t.Failed() {
		t.Log(st.report(false))
		return
	}

	// P3: the same production run twice from identical seed copies, once with
	// no searches and once with the P1/P2 request mix running throughout.
	t.Run("P3", func(t *testing.T) {
		est := p12Elapsed
		delay := time.Duration(float64(est) * 1.3 / float64(perfProdVideos*7/10))
		if delay < 50*time.Millisecond {
			delay = 50 * time.Millisecond
		}
		if delay > 3*time.Second {
			delay = 3 * time.Second
		}
		t.Logf("P1/P2 phase took %s; production run POST latency set to %s", est.Round(time.Second), delay)

		base := runProduction(t, dir, seedPath, "base", delay, nil, nil, truth)
		var loaded prodResult
		{
			rng := rand.New(rand.NewPCG(perfSeedValue, 3))
			work := p3Workload(truth, rng, perfSavedCursors)
			loaded = runProduction(t, dir, seedPath, "loaded", delay, work, st, truth)
		}
		t.Logf("production run without searches: %s, with searches: %s (%d searches issued while it ran)",
			base.duration.Round(time.Second), loaded.duration.Round(time.Second), loaded.searches)
		require.GreaterOrEqual(t, base.processed, perfProdVideos)
		require.GreaterOrEqual(t, loaded.processed, perfProdVideos)
		diffTables(t, base.tables, loaded.tables)
	})

	rep := st.report(true)
	t.Log("\n" + rep)
	if p := os.Getenv("YUHAUL_PERF_REPORT"); p != "" {
		require.NoError(t, os.WriteFile(p, []byte(machineInfo()+"\n"+rep), 0o644))
	}
	st.assertLimits(t)
	require.Empty(t, st.failuresAll(), "P4: every response must be 200 and complete")
}

// perfSavedCursors carries the page cursors of the 20k token from P2 to P3.
var perfSavedCursors map[int]string

// ---------------------------------------------------------------------------
// Seed

func seedPerfDB(t *testing.T, dbPath string) *perfTruth {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, dbPath)
	require.NoError(t, err)
	tokens, err := auth.NewSQLiteTokenStore(db, []byte(childKey))
	require.NoError(t, err)
	require.NoError(t, tokens.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "local-test", RefreshToken: "local-refresh", TokenType: "Bearer", Expiry: time.Now().Add(1000 * time.Hour), Scopes: auth.DefaultScopes()}))
	routeID, err := router.NewStore(db).CreateRoute(ctx, &router.Route{
		Name: "Jazz", EvaluationOrder: 1, IsEnabled: true, ConditionComposition: "OR",
		Conditions: []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "jazz"}},
		Actions:    []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLjazz"}},
	})
	require.NoError(t, err)

	rng := rand.New(rand.NewPCG(perfSeedValue, 1))
	now := time.Now().UTC().Truncate(time.Second)
	rfc := func(x time.Time) string { return x.UTC().Format(time.RFC3339) }

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	prep := func(q string) *sql.Stmt {
		s, err := tx.PrepareContext(ctx, q)
		require.NoError(t, err, q)
		return s
	}
	must := func(_ sql.Result, err error) { require.NoError(t, err) }

	// Runs: perfRuns completed runs a few days ago; every 10th is a simulation.
	runStmt := prep(`INSERT INTO run (started_at, completed_at, run_type, status) VALUES (?,?,?,'completed')`)
	var prodRuns, allRuns []int64
	for i := 1; i <= perfRuns; i++ {
		typ := "manual"
		switch {
		case i%10 == 0:
			typ = "dry_run"
		case i%2 == 0:
			typ = "scheduled"
		}
		started := now.Add(-72*time.Hour + time.Duration(i)*5*time.Minute)
		res, err := runStmt.Exec(rfc(started), rfc(started.Add(time.Minute)), typ)
		require.NoError(t, err)
		id, _ := res.LastInsertId()
		allRuns = append(allRuns, id)
		if typ != "dry_run" {
			prodRuns = append(prodRuns, id)
		}
	}
	isSim := map[int64]bool{}
	for _, id := range allRuns {
		isSim[id] = true
	}
	for _, id := range prodRuns {
		delete(isSim, id)
	}

	// Ordinary title words: no q, x or z.
	syl := strings.Fields("ba be bi bo bu da de di do du ka ke ki ko ku la le li lo lu ma me mi mo mu na ne ni no nu pa pe pi po pu ra re ri ro ru sa se si so su ta te ti to tu va ve vi vo vu")
	seen := map[string]bool{}
	var vocab []string
	for len(vocab) < 3000 {
		w := syl[rng.IntN(len(syl))] + syl[rng.IntN(len(syl))] + syl[rng.IntN(len(syl))]
		if !seen[w] {
			seen[w] = true
			vocab = append(vocab, w)
		}
	}
	const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"
	used := map[string]bool{}
	newID := func() string {
		for {
			b := make([]byte, 11)
			x, y := rng.Uint64(), rng.Uint64()
			for i := range b {
				if i < 6 {
					b[i] = alpha[(x>>(6*i))&63]
				} else {
					b[i] = alpha[(y>>(6*(i-6)))&63]
				}
			}
			s := string(b)
			if !used[s] && !strings.HasPrefix(s, "pv") && !strings.HasPrefix(s, "nf") {
				used[s] = true
				return s
			}
		}
	}

	// Planted tokens: disjoint sets of held-title videos.
	plant := make([]int, perfKnownHeld) // index into perfTokens, -1 none
	for i := range plant {
		plant[i] = -1
	}
	perm := rng.Perm(perfKnownHeld)
	pos := 0
	for ti, tok := range perfTokens {
		for k := 0; k < tok.matches; k++ {
			plant[perm[pos]] = ti
			pos++
		}
	}

	truth := &perfTruth{expected: map[string][]string{}}
	type matched struct{ disc, id string }
	matches := map[int][]matched{}

	videoStmt := prep(`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome, deferred_reason, error_message, completed_at) VALUES (?,?,?,?,?,?,?,?,?)`)
	metaStmt := prep(`INSERT INTO transient_metadata (video_id, title, channel_name, fetched_at, expires_at, expired_at, is_active) VALUES (?,?,?,?,?,?,?)`)
	decStmt := prep(`INSERT INTO route_decision (video_id, run_id, route_id, evaluation_order, decision, skip_reason) VALUES (?,?,?,1,?,?)`)
	outStmt := prep(`INSERT INTO run_video_outcome (run_id, video_id, final_outcome, deferred_reason, error_message, completed_at, updated_at) VALUES (?,?,?,?,?,?,?)`)

	outcomes := []string{"accepted", "accepted", "accepted", "accepted", "rejected", "rejected", "rejected", "excluded", "deferred", "failed", "unavailable_on_youtube"}
	decisionOf := map[string][2]any{
		"accepted": {"matched", nil}, "rejected": {"not_matched", nil}, "excluded": {"skipped", "exclusion"},
		"deferred": {"deferred", nil}, "failed": {"matched", nil}, "unavailable_on_youtube": {"not_matched", nil},
	}

	baseDisc := now.Truncate(time.Hour)
	total := perfKnownHeld + perfKnownExpired
	for i := 0; i < total; i++ {
		id := newID()
		held := i < perfKnownHeld
		disc := rfc(baseDisc.Add(-time.Duration(rng.IntN(180*24)) * time.Hour)) // hourly slots: many ties

		// Links to 1-3 distinct runs.
		k := 1 + rng.IntN(3)
		picked := map[int64]bool{}
		var links []int64
		for len(links) < k {
			r := allRuns[rng.IntN(len(allRuns))]
			if !picked[r] {
				picked[r] = true
				links = append(links, r)
			}
		}
		var finalOutcome, deferredReason, errMsg, completed any
		state := "discovered"
		type linkOut struct {
			run     int64
			outcome string
		}
		var lo []linkOut
		for _, r := range links {
			lo = append(lo, linkOut{r, outcomes[rng.IntN(len(outcomes))]})
		}
		for _, l := range lo {
			if !isSim[l.run] {
				finalOutcome, state, completed = l.outcome, "completed", disc
				if l.outcome == "deferred" {
					deferredReason = "quota_exhausted"
				}
				if l.outcome == "failed" {
					errMsg = "perf seeded failure"
				}
				break
			}
		}
		must(videoStmt.Exec(id, "UCperf"+strconv.Itoa(i%500), disc, links[0], state, finalOutcome, deferredReason, errMsg, completed))
		vid := int64(i + 1) // fresh database: AUTOINCREMENT ids are 1..n in insertion order
		for _, l := range lo {
			d := decisionOf[l.outcome]
			must(decStmt.Exec(vid, l.run, routeID, d[0], d[1]))
			if !isSim[l.run] {
				var dr, em any
				if l.outcome == "deferred" {
					dr = "quota_exhausted"
				}
				if l.outcome == "failed" {
					em = "perf seeded failure"
				}
				must(outStmt.Exec(l.run, vid, l.outcome, dr, em, disc, disc))
			}
		}

		if held {
			fetched := now.Add(-time.Duration(rng.IntN(29*24*60)) * time.Minute)
			nw := 3 + rng.IntN(4)
			words := make([]string, nw)
			for j := range words {
				words[j] = vocab[rng.IntN(len(vocab))]
			}
			if ti := plant[i]; ti >= 0 {
				at := rng.IntN(nw + 1)
				words = append(words[:at], append([]string{perfTokens[ti].word}, words[at:]...)...)
				matches[ti] = append(matches[ti], matched{disc, id})
			}
			must(metaStmt.Exec(id, strings.Join(words, " "), "Channel "+strconv.Itoa(i%500), rfc(fetched), rfc(fetched.Add(30*24*time.Hour)), nil, 1))
			truth.held = append(truth.held, id)
		} else {
			expiredAt := now.Add(-time.Duration(1+rng.IntN(29*24)) * time.Hour)
			must(metaStmt.Exec(id, nil, nil, rfc(expiredAt.Add(-30*24*time.Hour)), rfc(expiredAt), rfc(expiredAt), 0))
			truth.expired = append(truth.expired, id)
		}
	}
	for ti, tok := range perfTokens {
		m := matches[ti]
		require.Len(t, m, tok.matches)
		sort.Slice(m, func(a, b int) bool {
			if m[a].disc != m[b].disc {
				return m[a].disc > m[b].disc
			}
			return m[a].id < m[b].id
		})
		ids := make([]string, len(m))
		for j := range m {
			ids[j] = m[j].id
		}
		truth.expected[perfNeedle(tok.word)] = ids
	}

	// Imported references: no video row. Some fingerprints carry a planted
	// word to show that fingerprint text is never searched.
	fpStmt := prep(`INSERT INTO title_fingerprint (video_id, fingerprint, channel_id, source, import_batch_id) VALUES (?,?,?,'user_import','perf')`)
	for i := 0; i < perfImported; i++ {
		id := newID()
		fp := "former title " + vocab[rng.IntN(len(vocab))]
		if i%100 == 0 {
			fp += " " + perfTokens[i/100%4].word
		}
		must(fpStmt.Exec(id, fp, "UCimp"+strconv.Itoa(i%500)))
		truth.imported = append(truth.imported, id)
	}

	// Queue: 5,000 deferred items and 1,000 error entries in mixed states, each
	// on its own video. Pending items wait for a retry time far in the future
	// and no row is claimed, so the P3 production run does not retry them (a
	// claim by a finished run is inconsistent retry ownership).
	nDef, nErr := 5000, 1000
	if total < 60_000 { // smoke seed
		nDef, nErr = 500, 100
	}
	queueVideos := rng.Perm(total)[:nDef+nErr]
	defStmt := prep(`INSERT INTO deferred_item (video_id, run_id, deferral_reason, retry_after, retry_count, status) VALUES (?,?,'quota_exhausted',?,0,?)`)
	dStates := []string{"pending", "pending", "pending", "completed", "discarded"}
	for i := 0; i < nDef; i++ {
		s := dStates[rng.IntN(len(dStates))]
		must(defStmt.Exec(queueVideos[i]+1, prodRuns[rng.IntN(len(prodRuns))], rfc(now.Add(365*24*time.Hour)), s))
	}
	errStmt := prep(`INSERT INTO error_entry (video_id, run_id, error_category, error_message, status) VALUES (?,?,?,?,?)`)
	eStates := []string{"active", "active", "dismissed", "resolved"}
	cats := []string{"api_error", "authorization", "runtime"}
	for i := 0; i < nErr; i++ {
		must(errStmt.Exec(queueVideos[nDef+i]+1, prodRuns[rng.IntN(len(prodRuns))], cats[rng.IntN(len(cats))], "perf seeded error", eStates[rng.IntN(len(eStates))]))
	}

	require.NoError(t, tx.Commit())
	_, err = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	for _, ext := range []string{"-wal", "-shm"} {
		_ = os.Remove(dbPath + ext)
	}
	return truth
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	require.NoError(t, err)
	defer in.Close()
	out, err := os.Create(dst)
	require.NoError(t, err)
	_, err = io.Copy(out, in)
	require.NoError(t, err)
	require.NoError(t, out.Close())
}

func perfWaitOverview(t *testing.T, p *serverProcess) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + p.addr + "/api/overview")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case <-p.exited:
			t.Fatalf("server exited before serving: %v\n%s", p.err, p.out.String())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("server never served /api/overview\n%s", p.out.String())
}

// ---------------------------------------------------------------------------
// Client, requests, checks

type perfClient struct {
	base string
	c    *http.Client
}

func newPerfClient(addr string) *perfClient {
	return &perfClient{base: "http://" + addr, c: &http.Client{Timeout: 2 * time.Minute}}
}

func (pc *perfClient) get(path string) (time.Duration, int, []byte, error) {
	start := time.Now()
	resp, err := pc.c.Get(pc.base + path)
	if err != nil {
		return time.Since(start), 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return time.Since(start), resp.StatusCode, body, err
}

type perfReq struct {
	group  string
	path   string
	verify func(body []byte) error
}

// do times one request and records it. A transport error, a non-200 answer or
// a failed content check is a P4 failure; the sample is still recorded.
func (pc *perfClient) do(st *perfStats, r perfReq) {
	d, code, body, err := pc.get(r.path)
	st.add(r.group, d)
	switch {
	case err != nil:
		st.fail(r.group, "transport: "+err.Error())
	case code != http.StatusOK:
		st.fail(r.group, fmt.Sprintf("status %d: %.120s", code, body))
	case r.verify != nil:
		if err := r.verify(body); err != nil {
			st.fail(r.group, err.Error())
		}
	}
}

func searchPath(q, cursor string) string {
	v := url.Values{"q": {q}}
	if cursor != "" {
		v.Set("cursor", cursor)
	}
	return "/api/videos/search?" + v.Encode()
}

func decodeSearch(body []byte) (videosearch.SearchResult, error) {
	var res videosearch.SearchResult
	return res, json.Unmarshal(body, &res)
}

func wantKind(kind string) func([]byte) error {
	return func(body []byte) error {
		res, err := decodeSearch(body)
		if err != nil {
			return err
		}
		if res.Kind != kind {
			return fmt.Errorf("kind %q, want %q", res.Kind, kind)
		}
		return nil
	}
}

func wantVideoID(id string) func([]byte) error {
	return func(body []byte) error {
		res, err := decodeSearch(body)
		if err != nil {
			return err
		}
		if res.Kind != "video" || res.VideoID != id {
			return fmt.Errorf("kind %q video_id %q, want video %q", res.Kind, res.VideoID, id)
		}
		return nil
	}
}

func p1Requests(tr *perfTruth, rng *rand.Rand, prefix string) []perfReq {
	pick := func() string {
		switch rng.IntN(3) {
		case 0:
			return tr.imported[rng.IntN(len(tr.imported))]
		case 1:
			return tr.expired[rng.IntN(len(tr.expired))]
		default:
			return tr.held[rng.IntN(len(tr.held))]
		}
	}
	var out []perfReq
	for i := 0; i < 60; i++ {
		id := pick()
		out = append(out, perfReq{prefix + "search typed ID found", searchPath(id, ""), wantVideoID(id)})
	}
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("nf%09d", rng.IntN(1_000_000_000))
		out = append(out, perfReq{prefix + "search typed ID not found", searchPath(id, ""), wantKind("id_not_found")})
	}
	links := []struct{ name, fmtStr string }{
		{"watch", "https://www.youtube.com/watch?v=%s&t=42"},
		{"youtu.be", "https://youtu.be/%s?t=42"},
		{"shorts", "https://www.youtube.com/shorts/%s"},
	}
	for _, l := range links {
		for i := 0; i < 30; i++ {
			id := pick()
			out = append(out, perfReq{prefix + "search link " + l.name, searchPath(fmt.Sprintf(l.fmtStr, id), ""), wantVideoID(id)})
		}
	}
	for i := 0; i < perfP1ViewMin; i++ {
		var id string
		if i%2 == 0 {
			id = tr.held[rng.IntN(len(tr.held))]
		} else {
			id = tr.imported[rng.IntN(len(tr.imported))]
		}
		out = append(out, perfReq{prefix + "GET /api/videos/{id}", "/api/videos/" + id, func(body []byte) error {
			var m struct {
				VideoID string `json:"video_id"`
			}
			if err := json.Unmarshal(body, &m); err != nil || m.VideoID != id {
				return fmt.Errorf("view of %s answered video_id %q (%v)", id, m.VideoID, err)
			}
			return nil
		}})
	}
	return out
}

func p2FirstPageRequests(tr *perfTruth, prefix string) []perfReq {
	var out []perfReq
	for _, tok := range perfTokens {
		needle := perfNeedle(tok.word)
		for i := 0; i < 40; i++ {
			out = append(out, perfReq{prefix + "first page " + tok.label + " matches", searchPath(needle, ""), pageVerify(tr, needle, 1)})
		}
	}
	return out
}

func pageReq(tr *perfTruth, needle, cursor string, n int, group string) perfReq {
	return perfReq{group, searchPath(needle, cursor), pageVerify(tr, needle, n)}
}

// pageVerify is the P4 check for one page: kind, total, page number and the
// exact items at that position of the ground truth.
func pageVerify(tr *perfTruth, needle string, n int) func([]byte) error {
	exp := tr.expected[needle]
	return func(body []byte) error {
		res, err := decodeSearch(body)
		if err != nil {
			return err
		}
		if res.Kind != "results" || res.Results == nil {
			return fmt.Errorf("kind %q, want results", res.Kind)
		}
		p := res.Results
		lo := min((n-1)*perfPageSize, len(exp))
		hi := min(n*perfPageSize, len(exp))
		if p.Total != len(exp) {
			return fmt.Errorf("page %d total %d, want %d", n, p.Total, len(exp))
		}
		if p.Number != n && len(exp) > 0 {
			return fmt.Errorf("page number %d, want %d", p.Number, n)
		}
		if len(p.Items) != hi-lo {
			return fmt.Errorf("page %d holds %d items, want %d", n, len(p.Items), hi-lo)
		}
		for i, it := range p.Items {
			if it.VideoID != exp[lo+i] {
				return fmt.Errorf("page %d item %d is %s, want %s", n, i, it.VideoID, exp[lo+i])
			}
		}
		if (p.NextCursor != nil) != (hi < len(exp)) {
			return fmt.Errorf("page %d next_cursor presence wrong", n)
		}
		return nil
	}
}

// walkToken follows next_cursor from page 1 to the last page, verifying every
// page against the ground truth (P4, so the union of pages is exactly the
// truth). Pages numbered 100 and above are timed under group when it is set.
func walkToken(t *testing.T, pc *perfClient, st *perfStats, tr *perfTruth, needle, group string, onCursor func(n int, cursor string)) {
	t.Helper()
	path := searchPath(needle, "")
	for n := 1; ; n++ {
		d, code, body, err := pc.get(path)
		g := group
		if n < 100 || group == "" {
			g = "walk (untimed) " + needle
		} else {
			st.add(group, d)
		}
		switch {
		case err != nil:
			st.fail(g, "transport: "+err.Error())
			return
		case code != http.StatusOK:
			st.fail(g, fmt.Sprintf("status %d: %.120s", code, body))
			return
		}
		if err := pageVerify(tr, needle, n)(body); err != nil {
			st.fail(g, err.Error())
		}
		res, err := decodeSearch(body)
		require.NoError(t, err)
		if res.Results == nil || res.Results.NextCursor == nil {
			return
		}
		onCursor(n+1, *res.Results.NextCursor)
		path = searchPath(needle, *res.Results.NextCursor)
	}
}

// ---------------------------------------------------------------------------
// P3

func p3Workload(tr *perfTruth, rng *rand.Rand, cursors map[int]string) []perfReq {
	work := p1Requests(tr, rng, "P3 ")
	work = append(work, p2FirstPageRequests(tr, "P3 ")...)
	alpha := perfNeedle(perfTokens[0].word)
	var pages []int
	for n := range cursors {
		if n >= 100 {
			pages = append(pages, n)
		}
	}
	sort.Ints(pages)
	for _, n := range pages {
		work = append(work, pageReq(tr, alpha, cursors[n], n, "P3 page>=100 20k"))
	}
	rng.Shuffle(len(work), func(i, j int) { work[i], work[j] = work[j], work[i] })
	return work
}

type prodResult struct {
	duration  time.Duration
	searches  int
	processed int
	tables    map[string][]string
}

// runProduction starts a fresh server on a copy of the seed, runs one
// production run (Run Now) against a controlled provider that discovers
// perfProdVideos videos, and, when work is given, issues the search workload
// one request at a time for as long as the run is active.
func runProduction(t *testing.T, dir, seedPath, name string, postDelay time.Duration, work []perfReq, st *perfStats, tr *perfTruth) prodResult {
	t.Helper()
	dbPath := filepath.Join(dir, name+".db")
	copyFile(t, seedPath, dbPath)

	ids := make([]string, perfProdVideos)
	for i := range ids {
		ids[i] = fmt.Sprintf("pv%09d", i)
	}
	yt := newFakeYouTube(t, ids...)
	yt.titleOf = func(id string) string {
		n, _ := strconv.Atoi(id[2:])
		if n%10 < 7 {
			return "Jazz " + id // matches the route: a playlist insertion
		}
		return "Rock " + id // matches nothing: rejected
	}
	yt.setPostDelay(postDelay)
	stopDrain := make(chan struct{})
	go func() {
		for {
			select {
			case <-yt.postReached:
			case <-stopDrain:
				return
			}
		}
	}()
	defer close(stopDrain)

	srv := startServer(t, dbPath, yt.srv.URL, "YUHAUL_DAILY_QUOTA=100000000", childRunTimeLimitEnv+"=3h")
	perfWaitOverview(t, srv)
	db := openReadDB(t, dbPath)

	var runID int64
	deadline := time.Now().Add(2 * time.Minute)
	for {
		status, body := srv.do(http.MethodPost, "/api/runs")
		if status == http.StatusAccepted {
			var acc struct {
				RunID int64 `json:"run_id"`
			}
			require.NoError(t, json.Unmarshal([]byte(body), &acc))
			runID = acc.RunID
			break
		}
		if status != http.StatusServiceUnavailable || time.Now().After(deadline) {
			t.Fatalf("Run Now refused: %d %s\n%s", status, body, srv.out.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
	began := time.Now()

	var done atomic.Bool
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			var status string
			if err := db.QueryRow(`SELECT status FROM run WHERE id=?`, runID).Scan(&status); err == nil && status != "running" {
				done.Store(true)
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
	}()

	searches := 0
	if work != nil {
		pc := newPerfClient(srv.addr)
		for _, r := range work {
			if done.Load() {
				break
			}
			pc.do(st, r)
			searches++
		}
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Hour):
		t.Fatalf("production run %d did not finish\n%s", runID, srv.out.String())
	}
	dur := time.Since(began)
	status := scalar[string](t, db, `SELECT status FROM run WHERE id=?`, runID)
	require.Equal(t, "completed", status, "production run %s\n%s", name, srv.out.String())
	res := prodResult{
		duration:  dur,
		searches:  searches,
		processed: scalar[int](t, db, `SELECT count(*) FROM run_video_outcome WHERE run_id=?`, runID),
		tables:    snapshotOutcomes(t, db),
	}
	t.Logf("%s: run %d completed in %s, %d videos with an outcome, %d playlist POSTs, searches issued %d of %d",
		name, runID, dur.Round(time.Second), res.processed, scalar[int](t, db, `SELECT count(*) FROM executed_action WHERE run_id=?`, runID), searches, len(work))
	srv.sigterm()
	return res
}

// snapshotOutcomes reads the P3 comparison tables with timestamps left out and
// internal video ids replaced by the external ID.
func snapshotOutcomes(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	queries := map[string]string{
		"run_video_outcome": `SELECT o.run_id, v.video_id, IFNULL(o.final_outcome,''), IFNULL(o.deferred_reason,''), IFNULL(o.error_message,'') FROM run_video_outcome o JOIN video v ON v.id=o.video_id`,
		"executed_action":   `SELECT a.run_id, v.video_id, a.action_type, a.target_playlist_id, a.outcome, IFNULL(a.error_message,''), IFNULL(a.outcome_reason,'') FROM executed_action a JOIN video v ON v.id=a.video_id`,
		"deferred_item":     `SELECT d.run_id, v.video_id, d.deferral_reason, d.retry_count, d.status, IFNULL(d.retry_run_id,0) FROM deferred_item d JOIN video v ON v.id=d.video_id`,
		"error_entry":       `SELECT e.run_id, v.video_id, e.error_category, e.error_message, IFNULL(e.error_detail,''), e.status, e.retry_count, IFNULL(e.retry_run_id,0) FROM error_entry e JOIN video v ON v.id=e.video_id`,
	}
	out := map[string][]string{}
	for name, q := range queries {
		rows, err := db.Query(q)
		require.NoError(t, err, name)
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			require.NoError(t, rows.Scan(ptrs...))
			parts := make([]string, len(cols))
			for i, v := range vals {
				parts[i] = fmt.Sprint(v)
			}
			out[name] = append(out[name], strings.Join(parts, "|"))
		}
		require.NoError(t, rows.Err())
		rows.Close()
		sort.Strings(out[name])
	}
	return out
}

func diffTables(t *testing.T, base, loaded map[string][]string) {
	t.Helper()
	for _, name := range []string{"run_video_outcome", "executed_action", "deferred_item", "error_entry"} {
		a, b := base[name], loaded[name]
		t.Logf("P3 %s: %d rows without searches, %d rows with searches", name, len(a), len(b))
		if len(a) == 0 && name != "error_entry" {
			t.Errorf("P3 %s: empty comparison", name)
		}
		shown := 0
		for i := 0; i < max(len(a), len(b)) && shown < 5; i++ {
			var x, y string
			if i < len(a) {
				x = a[i]
			}
			if i < len(b) {
				y = b[i]
			}
			if x != y {
				t.Errorf("P3 %s differs at sorted row %d:\n  without searches: %s\n  with searches:    %s", name, i, x, y)
				shown++
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Statistics and report

type perfStats struct {
	samples  map[string][]time.Duration
	order    []string
	failures map[string][]string
}

func newPerfStats() *perfStats {
	return &perfStats{samples: map[string][]time.Duration{}, failures: map[string][]string{}}
}

func (s *perfStats) add(group string, d time.Duration) {
	if _, ok := s.samples[group]; !ok {
		s.order = append(s.order, group)
	}
	s.samples[group] = append(s.samples[group], d)
}

func (s *perfStats) fail(group, msg string) { s.failures[group] = append(s.failures[group], msg) }

func (s *perfStats) failuresAll() []string {
	var out []string
	for g, f := range s.failures {
		for i, m := range f {
			if i >= 3 {
				break
			}
			out = append(out, g+": "+m)
		}
	}
	return out
}

type perfAgg struct {
	name   string
	prefix string
	match  func(group string) bool
	limit  time.Duration
}

func (s *perfStats) aggregates() []perfAgg {
	var out []perfAgg
	for _, p := range []string{"P1 ", "P3 "} {
		p := p
		out = append(out,
			perfAgg{p + "ALL search-endpoint ID requests", p, func(g string) bool { return strings.HasPrefix(g, p+"search ") }, perfP1Limit},
			perfAgg{p + "ALL GET /api/videos/{id}", p, func(g string) bool { return g == p+"GET /api/videos/{id}" }, perfP1Limit},
		)
	}
	for _, p := range []string{"P2 ", "P3 "} {
		p := p
		out = append(out,
			perfAgg{p + "ALL first page", p, func(g string) bool { return strings.HasPrefix(g, p+"first page") }, perfP2Limit},
			perfAgg{p + "ALL page>=100", p, func(g string) bool { return strings.HasPrefix(g, p+"page>=100") }, perfP2Limit},
		)
	}
	return out
}

func pct(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(float64(len(sorted))*p+0.999999) - 1
	return sorted[min(max(i, 0), len(sorted)-1)]
}

func summarize(d []time.Duration) (n int, p50, p95, mx time.Duration) {
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	if len(s) == 0 {
		return 0, 0, 0, 0
	}
	return len(s), pct(s, 0.50), pct(s, 0.95), s[len(s)-1]
}

func (s *perfStats) groupsFor(match func(string) bool) []time.Duration {
	var all []time.Duration
	for g, d := range s.samples {
		if match(g) {
			all = append(all, d...)
		}
	}
	return all
}

func (s *perfStats) report(full bool) string {
	var b strings.Builder
	row := func(name string, d []time.Duration, verdict string) {
		n, p50, p95, mx := summarize(d)
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s | %s |\n", name, n, ms(p50), ms(p95), ms(mx), verdict)
	}
	b.WriteString("| Case | n | p50 | p95 | max | verdict |\n|---|---|---|---|---|---|\n")
	groups := append([]string(nil), s.order...)
	sort.SliceStable(groups, func(i, j int) bool { return groups[i] < groups[j] })
	for _, g := range groups {
		if strings.HasPrefix(g, "walk (untimed)") {
			continue
		}
		row(g, s.samples[g], "")
	}
	for _, a := range s.aggregates() {
		d := s.groupsFor(a.match)
		if len(d) == 0 {
			continue
		}
		_, _, p95, _ := summarize(d)
		v := "PASS"
		if p95 > a.limit {
			v = "FAIL (p95 > " + a.limit.String() + ")"
		}
		row("**"+a.name+"**", d, v)
	}
	if f := s.failuresAll(); len(f) > 0 {
		b.WriteString("\nP4 failures (first 3 per case):\n")
		for _, m := range f {
			b.WriteString("- " + m + "\n")
		}
	}
	return b.String()
}

func (s *perfStats) assertLimits(t *testing.T) {
	t.Helper()
	for _, a := range s.aggregates() {
		d := s.groupsFor(a.match)
		if len(d) == 0 {
			continue
		}
		_, _, p95, _ := summarize(d)
		if p95 > a.limit {
			t.Errorf("%s: p95 %s exceeds %s", a.name, p95, a.limit)
		}
	}
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1f ms", float64(d)/float64(time.Millisecond)) }

func machineInfo() string {
	cpu, cores := "unknown", 0
	if f, err := os.Open("/proc/cpuinfo"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			l := sc.Text()
			if strings.HasPrefix(l, "model name") && cpu == "unknown" {
				cpu = strings.TrimSpace(l[strings.Index(l, ":")+1:])
			}
			if strings.HasPrefix(l, "processor") {
				cores++
			}
		}
	}
	mem := "unknown"
	if f, err := os.Open("/proc/meminfo"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var kb int
			if _, err := fmt.Sscanf(sc.Text(), "MemTotal: %d kB", &kb); err == nil {
				mem = fmt.Sprintf("%.1f GiB", float64(kb)/1024/1024)
				break
			}
		}
	}
	return fmt.Sprintf("Machine: %s, %d logical CPUs, %s RAM", cpu, cores, mem)
}
