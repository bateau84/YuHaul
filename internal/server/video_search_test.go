package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/videosearch"
)

// vsClock is the controllable clock behind the real service.
type vsClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *vsClock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *vsClock) Set(t time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.t = t }

type vsFix struct {
	t     *testing.T
	db    *sql.DB
	srv   *Server
	clock *vsClock
	logs  *bytes.Buffer
	route int64
}

var vsNow = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func newVSFix(t *testing.T) *vsFix {
	t.Helper()
	db, stateStore, routerStore, versioner, cleanup := setupRunVideoTest(t)
	t.Cleanup(cleanup)
	f := &vsFix{t: t, db: db, clock: &vsClock{t: vsNow}, logs: &bytes.Buffer{}}
	f.srv = NewServer(&fakePipeline{}, pipeline.NewBroadcaster(),
		WithServerLogger(slog.New(slog.NewJSONHandler(f.logs, nil))),
		WithRunStore(stateStore), WithRunVideoStore(stateStore),
		WithRouteStore(routerStore), WithConfigVersionStore(versioner),
		WithVideoSearch(videosearch.NewService(db, f.clock.Now)))
	f.route = createRouteForRunVideoTests(t, context.Background(), routerStore).ID
	return f
}

func (f *vsFix) exec(q string, args ...any) int64 {
	f.t.Helper()
	res, err := f.db.Exec(q, args...)
	require.NoError(f.t, err, q)
	id, _ := res.LastInsertId()
	return id
}

func (f *vsFix) run(runType, status string, initiatedBy any, started time.Time) int64 {
	return f.exec(`INSERT INTO run (started_at, run_type, status, initiated_by) VALUES (?,?,?,?)`, ts(started), runType, status, initiatedBy)
}

// video inserts a video row; outcome may be nil.
func (f *vsFix) video(id string, disc time.Time, discRun any, outcome any) int64 {
	var completed any
	if outcome != nil {
		completed = ts(disc.Add(time.Minute))
	}
	state := "discovered"
	if outcome != nil {
		state = "completed"
	}
	return f.exec(`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome, completed_at)
		VALUES (?, 'UCvid', ?, ?, ?, ?, ?)`, id, ts(disc), discRun, state, outcome, completed)
}

func (f *vsFix) held(id, title, channel string) {
	f.exec(`INSERT INTO transient_metadata (video_id, title, channel_name, fetched_at, expires_at, is_active) VALUES (?,?,?,?,?,1)`,
		id, title, channel, ts(vsNow.Add(-time.Hour)), ts(vsNow.Add(30*24*time.Hour)))
}

func (f *vsFix) decision(vid, run int64, decision string, skip any) {
	f.exec(`INSERT INTO route_decision (video_id, run_id, route_id, evaluation_order, decision, skip_reason) VALUES (?,?,?,1,?,?)`, vid, run, f.route, decision, skip)
}

func (f *vsFix) outcome(run, vid int64, outcome string, deferred, errMsg any) {
	f.exec(`INSERT INTO run_video_outcome (run_id, video_id, final_outcome, deferred_reason, error_message, completed_at, updated_at) VALUES (?,?,?,?,?,?,?)`,
		run, vid, outcome, deferred, errMsg, ts(vsNow.Add(-time.Hour)), ts(vsNow))
}

func (f *vsFix) reference(id string, channel any) {
	f.exec(`INSERT INTO title_fingerprint (video_id, fingerprint, fingerprint_tokens, channel_id, source) VALUES (?, 'SECRETFINGERPRINT', 'SECRETTOKENS', ?, 'user_import')`, id, channel)
}

type vsResp struct {
	code   int
	header http.Header
	raw    []byte
}

func (f *vsFix) get(path string) vsResp {
	f.t.Helper()
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return vsResp{rec.Code, rec.Header(), rec.Body.Bytes()}
}

func (f *vsFix) search(q string, extra ...string) vsResp {
	v := url.Values{"q": {q}}
	for i := 0; i+1 < len(extra); i += 2 {
		v.Set(extra[i], extra[i+1])
	}
	return f.get("/api/videos/search?" + v.Encode())
}

func (r vsResp) search(t *testing.T) videosearch.SearchResult {
	t.Helper()
	require.Equal(t, http.StatusOK, r.code, string(r.raw))
	var res videosearch.SearchResult
	require.NoError(t, json.Unmarshal(r.raw, &res))
	return res
}

func (r vsResp) view(t *testing.T) map[string]any {
	t.Helper()
	require.Equal(t, http.StatusOK, r.code, string(r.raw))
	var m map[string]any
	require.NoError(t, json.Unmarshal(r.raw, &m))
	return m
}

func ids(p *videosearch.Page) []string {
	out := []string{}
	for _, it := range p.Items {
		out = append(out, it.VideoID)
	}
	return out
}

// vid builds a valid 11-character video ID from n.
func vid(n int) string { return fmt.Sprintf("lofi%07d", n) }

func TestVideoSearchPagingTotalsAndJumps_T10(t *testing.T) {
	f := newVSFix(t)
	for i := 0; i < 130; i++ {
		id := vid(i)
		f.video(id, vsNow.Add(-time.Duration(i)*time.Hour), nil, nil)
		f.held(id, fmt.Sprintf("Lofi Beats %d", i), "Chan")
	}
	f.video("other000001", vsNow, nil, nil)
	f.held("other000001", "Jazz", "Chan")

	var walked []string
	cursors := []string{}
	var p1 *videosearch.Page
	next := ""
	for page := 1; page <= 3; page++ {
		args := []string{}
		if next != "" {
			args = []string{"cursor", next}
		}
		res := f.search("lofi", args...).search(t)
		require.Equal(t, "results", res.Kind)
		p := res.Results
		if page == 1 {
			p1 = p
			assert.Nil(t, p.PrevCursor)
		}
		assert.Equal(t, 130, p.Total)
		assert.Equal(t, 3, p.TotalPages)
		assert.Equal(t, page, p.Number)
		assert.Equal(t, 50, p.PageSize)
		wantFirst := (page-1)*50 + 1
		require.NotNil(t, p.FirstPosition)
		assert.Equal(t, wantFirst, *p.FirstPosition)
		assert.Equal(t, wantFirst+len(p.Items)-1, *p.LastPosition)
		walked = append(walked, ids(p)...)
		if p.PrevCursor != nil {
			cursors = append(cursors, *p.PrevCursor)
		}
		if page < 3 {
			require.NotNil(t, p.NextCursor)
			next = *p.NextCursor
		} else {
			assert.Nil(t, p.NextCursor)
			assert.Len(t, p.Items, 30)
		}
	}
	require.Len(t, walked, 130)
	for i, id := range walked { // newest discovery first == ascending i
		assert.Equal(t, vid(i), id)
	}
	// prev_cursor walks back to the same pages.
	require.Len(t, cursors, 2)
	back := f.search("lofi", "cursor", cursors[1]).search(t).Results
	assert.Equal(t, walked[50:100], ids(back))
	back = f.search("lofi", "cursor", *back.PrevCursor).search(t).Results
	assert.Equal(t, ids(p1), ids(back))
	// page=n returns the cursor pages' items.
	assert.Equal(t, walked[50:100], ids(f.search("lofi", "page", "2").search(t).Results))
	assert.Equal(t, walked[100:], ids(f.search("lofi", "page", "3").search(t).Results))
	past := f.search("lofi", "page", "4").search(t).Results
	assert.Empty(t, past.Items)
	assert.Equal(t, 130, past.Total)
	assert.Equal(t, 4, past.Number)
	assert.Nil(t, past.FirstPosition)
	// Zero matches.
	none := f.search("zzzzz").search(t).Results
	assert.Equal(t, 0, none.Total)
	assert.Empty(t, none.Items)
	// Expiring one matching title between page 1 and page 2: total drops.
	f.exec(`UPDATE transient_metadata SET expired_at = ?, title = NULL, is_active = 0 WHERE video_id = ?`, ts(vsNow), vid(7))
	p2 := f.search("lofi", "cursor", next).search(t).Results
	assert.Equal(t, 129, p2.Total)
}

func TestVideoSearchNonLookupKindsReadNoDatabase_T11(t *testing.T) {
	f := newVSFix(t)
	require.NoError(t, f.db.Close()) // any read would now fail with 500
	cases := map[string]string{
		"": "empty", "   ": "empty", "lo": "too_short",
		strings.Repeat("a", 201):                   "too_long",
		"https://www.youtube.com/watch?list=PL123": "unusable_link",
		string([]byte{0xff, 0xfe, 'a'}):            "invalid_text",
	}
	for q, kind := range cases {
		res := f.search(q).search(t)
		assert.Equal(t, kind, res.Kind, q)
	}
	// Paging parameters on such a kind are refused with the paging code, still
	// without a read.
	r := f.search("lo", "cursor", "x")
	assert.Equal(t, 400, r.code)
	assert.JSONEq(t, `{"error":"invalid_cursor"}`, string(r.raw))
	r = f.search("lo", "page", "2")
	assert.Equal(t, 400, r.code)
	assert.JSONEq(t, `{"error":"invalid_page"}`, string(r.raw))
}

func TestVideoSearchIDKinds_T12_T13_T14(t *testing.T) {
	f := newVSFix(t)
	known := "dQw4w9WgXcQ"
	f.video(known, vsNow.Add(-time.Hour), nil, nil)
	f.held(known, "Known one", "Chan")
	f.video("someother111", vsNow, nil, nil)
	f.held("someother111", "mentions XyZ12345678 here", "Chan")
	f.reference("ref00000001", "UCref")

	res := f.search(known).search(t)
	assert.Equal(t, "video", res.Kind)
	assert.Equal(t, known, res.VideoID)
	require.NotNil(t, res.FromLink)
	assert.False(t, *res.FromLink)
	// Different case: id_not_found with name results (title substring only).
	res = f.search(strings.ToUpper(known)).search(t)
	assert.Equal(t, "id_not_found", res.Kind)
	require.NotNil(t, res.Results)
	// Unknown typed ID found as a title substring.
	res = f.search("XyZ12345678").search(t)
	assert.Equal(t, "id_not_found", res.Kind)
	require.NotNil(t, res.Results)
	assert.Equal(t, []string{"someother111"}, ids(res.Results))
	// Link to a known ID, and to an unknown one (no results member at all).
	res = f.search("https://youtu.be/dQw4w9WgXcQ?t=42").search(t)
	assert.Equal(t, "video", res.Kind)
	assert.True(t, *res.FromLink)
	r := f.search("https://youtu.be/AAAAAAAAAAA")
	res = r.search(t)
	assert.Equal(t, "id_not_found", res.Kind)
	assert.Nil(t, res.Results)
	assert.NotContains(t, string(r.raw), `"results"`)
	// A link or a found ID never pages.
	assert.Equal(t, 400, f.search("https://youtu.be/AAAAAAAAAAA", "page", "1").code)
	assert.Equal(t, 400, f.search(known, "cursor", "x").code)

	// T13 imported reference.
	res = f.search("ref00000001").search(t)
	assert.Equal(t, "video", res.Kind)
	v := f.get("/api/videos/ref00000001").view(t)
	assert.Equal(t, "imported_reference", v["record"])
	assert.Equal(t, map[string]any{"state": "imported_reference"}, v["latest_outcome"])
	assert.Equal(t, map[string]any{"state": "none"}, v["queue_state"])
	assert.Equal(t, "UCref", v["channel"].(map[string]any)["channel_id"])
	assert.NotContains(t, string(f.get("/api/videos/ref00000001").raw), "SECRET")

	// T14 cursor of a different q.
	for i := 0; i < 60; i++ {
		id := vid(i)
		f.video(id, vsNow.Add(-time.Duration(i)*time.Minute), nil, nil)
		f.held(id, "alpha "+id, "C")
	}
	page := f.search("alpha").search(t).Results
	require.NotNil(t, page.NextCursor)
	r = f.search("beta", "cursor", *page.NextCursor)
	assert.Equal(t, 400, r.code)
	assert.JSONEq(t, `{"error":"invalid_cursor"}`, string(r.raw))
	assert.Equal(t, 400, f.search("alpha", "page", "0").code)
	assert.Equal(t, 400, f.search("alpha", "page", "2", "cursor", *page.NextCursor).code)
	r = f.search("alpha", "page", "2", "cursor", *page.NextCursor)
	assert.JSONEq(t, `{"error":"invalid_page"}`, string(r.raw))
}

func TestVideoSearchCoverageFlow_T19(t *testing.T) {
	f := newVSFix(t)
	store := metadata.NewStore(f.db)
	mgr := metadata.NewManager(store, nil, nil, metadata.WithNow(f.clock.Now))
	id := "cover000001"
	f.video(id, vsNow.Add(-time.Hour), nil, nil)
	f.held(id, "Coverage title", "Chan")
	require.Equal(t, []string{id}, ids(f.search("coverage").search(t).Results))

	// expires_at moved past without a sweep: not found, expired in the view.
	f.exec(`UPDATE transient_metadata SET expires_at = ? WHERE video_id = ?`, ts(vsNow.Add(-time.Minute)), id)
	assert.Empty(t, f.search("coverage").search(t).Results.Items)
	v := f.get("/api/videos/" + id).view(t)
	assert.Equal(t, "expired", v["title"].(map[string]any)["availability"])
	assert.Nil(t, v["title"].(map[string]any)["text"])
	f.exec(`UPDATE transient_metadata SET expires_at = ? WHERE video_id = ?`, ts(vsNow.Add(time.Hour)), id)
	require.Len(t, f.search("coverage").search(t).Results.Items, 1)

	// Real sweeps. A cursor from before the sweep names no item afterwards.
	for i := 0; i < 55; i++ {
		x := vid(i)
		f.video(x, vsNow.Add(-time.Duration(i)*time.Minute), nil, nil)
		f.held(x, "sweepable "+x, "C")
	}
	first := f.search("sweepable").search(t).Results
	require.NotNil(t, first.NextCursor)
	f.clock.Set(vsNow.Add(31 * 24 * time.Hour))
	_, err := mgr.ExpireStale(context.Background())
	require.NoError(t, err)
	assert.Empty(t, f.search("sweepable").search(t).Results.Items)
	assert.Empty(t, f.search("sweepable", "cursor", *first.NextCursor).search(t).Results.Items)
	v = f.get("/api/videos/" + id).view(t)
	assert.Equal(t, "expired", v["title"].(map[string]any)["availability"])
	_, err = mgr.CleanupExpired(context.Background())
	require.NoError(t, err)
	v = f.get("/api/videos/" + id).view(t)
	assert.Equal(t, "unavailable", v["title"].(map[string]any)["availability"])
}

func TestVideoSearchHidesFingerprints_T20_T21(t *testing.T) {
	f := newVSFix(t)
	f.reference("ref00000002", "UCref")
	f.exec(`INSERT INTO transient_metadata (video_id, title, channel_name, fetched_at, expires_at, is_active) VALUES ('ref00000002','Former Imported Title','Former Channel',?,?,0)`,
		ts(vsNow.Add(-time.Hour)), ts(vsNow.Add(time.Hour)))
	f.video("vidlogs0001", vsNow, nil, nil)
	f.held("vidlogs0001", "Needle Title", "SecretChannelName")

	for _, q := range []string{"SECRETFINGERPRINT", "Former Imported"} {
		r := f.search(q)
		assert.Empty(t, r.search(t).Results.Items, q)
		assert.NotContains(t, string(r.raw), "SECRETFINGERPRINT")
	}
	// T21: no-store on every response, and logs hold no search text.
	paths := []string{"/api/videos/search?q=needle", "/api/videos/search?q=", "/api/videos/search?page=x&q=needle",
		"/api/videos/vidlogs0001", "/api/videos/bad", "/api/videos/nope0000000"}
	for _, p := range paths {
		assert.Equal(t, "no-store", f.get(p).header.Get("Cache-Control"), p)
	}
	first := f.search("needle").search(t)
	require.NotNil(t, first)
	f.get("/api/videos/vidlogs0001")
	// A failing read logs a diagnostic, still without text.
	require.NoError(t, f.db.Close())
	r := f.search("needle title")
	assert.Equal(t, 500, r.code)
	assert.JSONEq(t, `{"error":"search_failed"}`, string(r.raw))
	assert.Equal(t, "no-store", r.header.Get("Cache-Control"))
	r = f.get("/api/videos/vidlogs0001")
	assert.Equal(t, 500, r.code)
	assert.JSONEq(t, `{"error":"video_view_failed"}`, string(r.raw))
	logs := f.logs.String()
	require.NotEmpty(t, logs)
	for _, secret := range []string{"needle", "Needle", "SecretChannelName", "SECRETFINGERPRINT", "cursor"} {
		assert.NotContains(t, logs, secret)
	}
	assert.Contains(t, logs, `"kind":"results"`)
}

func TestVideoViewFieldsPerState_T22_T23_T24_T25_T26(t *testing.T) {
	f := newVSFix(t)
	t0 := vsNow.Add(-48 * time.Hour)
	prod1 := f.run("scheduled", "completed", "schedule", t0)
	prod2 := f.run("manual", "completed", "run_now", t0.Add(time.Hour))
	prod3 := f.run("manual", "completed", "queue_retry", t0.Add(2*time.Hour))
	oldRun := f.run("manual", "completed", nil, t0.Add(-time.Hour))
	dry := f.run("dry_run", "completed", nil, t0.Add(3*time.Hour))
	replay := f.run("replay", "completed", nil, t0.Add(4*time.Hour))
	backtest := f.run("backtest", "completed", nil, t0.Add(5*time.Hour))

	// A: unavailable_on_youtube natively, with production and simulation runs.
	a := f.video("aaaaaaaaaaa", t0, prod1, "unavailable_on_youtube")
	f.held("aaaaaaaaaaa", "Alpha", "ChanA")
	f.outcome(prod1, a, "failed", nil, "boom")
	f.outcome(prod2, a, "unavailable_on_youtube", nil, nil)
	f.decision(a, dry, "matched", nil)
	f.decision(a, replay, "not_matched", nil)
	f.decision(a, backtest, "skipped", "exclusion")
	// A discovered-only production run (Q3: not listed).
	f.outcome(prod3, a, "not_yet_evaluated", nil, nil)
	// B: failed whose message carries code video_unavailable stays failed.
	b := f.video("bbbbbbbbbbb", t0, prod1, "failed")
	f.outcome(prod1, b, "failed", nil, "diag:v1 phase=unknown operation=execute_action code=video_unavailable")
	// C: not_yet_evaluated.
	f.video("ccccccccccc", t0, prod1, nil)
	// D: legacy committed outcome with no run_video_outcome row.
	f.video("ddddddddddd", t0, oldRun, "accepted")
	// E: channel expired, F: never fetched.
	f.video("eeeeeeeeeee", t0, prod1, nil)
	f.exec(`INSERT INTO transient_metadata (video_id, title, channel_name, fetched_at, expires_at, expired_at, is_active) VALUES ('eeeeeeeeeee',NULL,NULL,?,?,?,0)`,
		ts(t0), ts(t0.Add(time.Hour)), ts(t0.Add(2*time.Hour)))

	view := func(id string) map[string]any { return f.get("/api/videos/" + id).view(t) }
	sub := func(m map[string]any, k string) map[string]any { return m[k].(map[string]any) }

	va := view("aaaaaaaaaaa")
	assert.Equal(t, "known", va["record"])
	lo := sub(va, "latest_outcome")
	assert.Equal(t, "committed", lo["state"])
	assert.Equal(t, "unavailable_on_youtube", lo["outcome"])
	assert.EqualValues(t, prod2, lo["run_id"])
	assert.NotNil(t, lo["committed_at"])
	assert.Equal(t, "current_local", sub(va, "title")["availability"])
	assert.Equal(t, "Alpha", sub(va, "title")["text"])

	// Runs: newest first, Q3 run omitted, simulations labelled, fields set.
	runs := va["runs"].([]any)
	var gotIDs []int64
	byID := map[int64]map[string]any{}
	for _, r := range runs {
		m := r.(map[string]any)
		id := int64(m["run_id"].(float64))
		gotIDs = append(gotIDs, id)
		byID[id] = m
	}
	assert.Equal(t, []int64{backtest, replay, dry, prod2, prod1}, gotIDs)
	assert.Equal(t, "simulation", byID[dry]["mode"])
	assert.Equal(t, "accepted", byID[dry]["outcome"])
	assert.Equal(t, "rejected", byID[replay]["outcome"])
	assert.Equal(t, "excluded", byID[backtest]["outcome"])
	assert.Equal(t, "unavailable_on_youtube", byID[prod2]["outcome"])
	assert.Equal(t, "failed", byID[prod1]["outcome"])
	assert.Equal(t, "production", byID[prod2]["mode"])
	assert.Equal(t, "run_now", byID[prod2]["initiated_by"])
	assert.Equal(t, "schedule", byID[prod1]["initiated_by"])
	assert.Nil(t, byID[dry]["initiated_by"])
	assert.Equal(t, ts(t0.Add(time.Hour)), byID[prod2]["started_at"])
	assert.Equal(t, "completed", byID[prod2]["run_status"])

	// T26 / T24: every run entry agrees with, and resolves to, the run-video API.
	for _, r := range runs {
		m := r.(map[string]any)
		resp := f.get(fmt.Sprintf("/api/runs/%d/videos/aaaaaaaaaaa", int64(m["run_id"].(float64))))
		require.Equal(t, 200, resp.code, string(resp.raw))
		var rv map[string]any
		require.NoError(t, json.Unmarshal(resp.raw, &rv))
		assert.Equal(t, fmt.Sprintf("/runs/%d/video/aaaaaaaaaaa", int64(m["run_id"].(float64))), m["decision_detail_path"])
		assert.Equal(t, m["outcome"], rv["video"].(map[string]any)["final_outcome"], "run %v", m["run_id"])
	}

	// T22 B, C, D, E, F.
	lo = sub(view("bbbbbbbbbbb"), "latest_outcome")
	assert.Equal(t, "failed", lo["outcome"])
	assert.Equal(t, map[string]any{"state": "not_yet_evaluated"}, view("ccccccccccc")["latest_outcome"])
	lo = sub(view("ddddddddddd"), "latest_outcome")
	assert.Equal(t, "accepted", lo["outcome"])
	assert.Nil(t, lo["run_id"])
	assert.NotNil(t, lo["committed_at"])
	ve := view("eeeeeeeeeee")
	assert.Equal(t, "expired", sub(ve, "channel")["name_availability"])
	assert.Nil(t, sub(ve, "channel")["name"])
	assert.Equal(t, "unavailable", sub(view("ccccccccccc"), "channel")["name_availability"])
	assert.Empty(t, view("ccccccccccc")["runs"])

	// T23: later dry runs change neither latest_outcome nor queue_state, and a
	// simulation-only video is labelled and still found by name and ID.
	laterDry := f.run("dry_run", "completed", nil, vsNow)
	f.decision(a, laterDry, "not_matched", nil)
	assert.Equal(t, lo0(va), lo0(view("aaaaaaaaaaa")))
	sim := f.video("sssssssssss", t0, dry, nil)
	f.decision(sim, dry, "matched", nil)
	f.held("sssssssssss", "Simulated Only", "SimChan")
	vs := view("sssssssssss")
	assert.Equal(t, "simulation_only", vs["record"])
	assert.Equal(t, map[string]any{"state": "simulation_only"}, vs["latest_outcome"])
	assert.Equal(t, map[string]any{"state": "none"}, vs["queue_state"])
	assert.Equal(t, "video", f.search("sssssssssss").search(t).Kind)
	assert.Equal(t, []string{"sssssssssss"}, ids(f.search("simulated only").search(t).Results))
}

func lo0(m map[string]any) any { return m["latest_outcome"] }

func TestVideoViewQueueStatePrecedence_T22(t *testing.T) {
	f := newVSFix(t)
	t0 := vsNow.Add(-24 * time.Hour)
	runID := f.run("manual", "completed", "run_now", t0)
	claim := f.run("manual", "running", "queue_retry", t0.Add(time.Hour))
	mk := func(id string) int64 { return f.video(id, t0, runID, "deferred") }
	deferred := func(v int64, status string, retry any) {
		f.exec(`INSERT INTO deferred_item (video_id, run_id, deferral_reason, status, retry_run_id, created_at) VALUES (?,?,?,?,?,?)`,
			v, runID, "quota_exhausted", status, retry, ts(t0))
	}
	errEntry := func(v int64, status string, retry any) {
		f.exec(`INSERT INTO error_entry (video_id, run_id, error_category, error_message, status, retry_run_id, created_at) VALUES (?,?,?,?,?,?,?)`,
			v, runID, "api_error", "msg", status, retry, ts(t0))
	}
	q := func(id string) map[string]any {
		return f.get("/api/videos/" + id).view(t)["queue_state"].(map[string]any)
	}
	v1 := mk("queuedcl001") // deferred claimed beats everything
	deferred(v1, "pending", nil)
	deferred(v1, "retrying", claim)
	errEntry(v1, "active", nil)
	assert.Equal(t, map[string]any{"state": "deferred_claimed", "reason": "quota_exhausted", "claiming_run_id": float64(claim)}, q("queuedcl001"))
	v2 := mk("queuedpe001")
	errEntry(v2, "retrying", claim)
	deferred(v2, "pending", nil)
	assert.Equal(t, map[string]any{"state": "deferred_pending", "reason": "quota_exhausted"}, q("queuedpe001"))
	v3 := mk("queueder001")
	errEntry(v3, "active", nil)
	errEntry(v3, "retrying", claim)
	assert.Equal(t, map[string]any{"state": "errors_claimed", "category": "api_error", "claiming_run_id": float64(claim)}, q("queueder001"))
	v4 := mk("queueder002")
	errEntry(v4, "active", nil)
	assert.Equal(t, map[string]any{"state": "errors_active", "category": "api_error"}, q("queueder002"))
	v5 := mk("queuedrm001")
	deferred(v5, "discarded", nil)
	assert.Equal(t, map[string]any{"state": "removed_by_user"}, q("queuedrm001"))
	mk("queuednn001")
	assert.Equal(t, map[string]any{"state": "none"}, q("queuednn001"))
	// Search rows carry the same state.
	f.held("queuedcl001", "queue probe", "C")
	items := f.search("queue probe").search(t).Results.Items
	require.Len(t, items, 1)
	b, _ := json.Marshal(items[0].QueueState)
	assert.JSONEq(t, fmt.Sprintf(`{"state":"deferred_claimed","reason":"quota_exhausted","claiming_run_id":%d}`, claim), string(b))
}

func TestVideoRoutes(t *testing.T) {
	f := newVSFix(t)
	assert.Equal(t, 400, f.get("/api/videos/short").code)
	assert.JSONEq(t, `{"error":"invalid_video_id"}`, string(f.get("/api/videos/short").raw))
	r := f.get("/api/videos/unknown0000")
	assert.Equal(t, 404, r.code)
	assert.JSONEq(t, `{"error":"video_not_found"}`, string(r.raw))
	// "search" is a literal route, never a video ID.
	assert.Equal(t, 200, f.get("/api/videos/search").code)
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/videos/search", nil))
	assert.NotEqual(t, 200, rec.Code)
}

// T31: the search endpoints change nothing.
func TestVideoSearchWritesNothing_T31(t *testing.T) {
	f := newVSFix(t)
	t0 := vsNow.Add(-24 * time.Hour)
	run := f.run("manual", "completed", "run_now", t0)
	v := f.video("snapshot001", t0, run, "accepted")
	f.held("snapshot001", "Snapshot title", "C")
	f.outcome(run, v, "accepted", nil, nil)
	f.decision(v, run, "matched", nil)
	f.reference("snapshot002", "UC")
	before := dumpTables(t, f.db)
	for _, p := range []string{"/api/videos/search?q=snapshot", "/api/videos/search?q=snapshot001", "/api/videos/snapshot001", "/api/videos/snapshot002", "/api/videos/search?q=zzz"} {
		require.Equal(t, 200, f.get(p).code, p)
	}
	assert.Equal(t, before, dumpTables(t, f.db))
}

func dumpTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	trows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	require.NoError(t, err)
	var tables []string
	for trows.Next() {
		var n string
		require.NoError(t, trows.Scan(&n))
		tables = append(tables, n)
	}
	require.NoError(t, trows.Close())
	var out []string
	for _, tbl := range tables {
		rows, err := db.Query(`SELECT * FROM "` + tbl + `"`)
		require.NoError(t, err)
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			require.NoError(t, rows.Scan(ptrs...))
			out = append(out, fmt.Sprintf("%s %v", tbl, vals))
		}
		require.NoError(t, rows.Close())
	}
	sort.Strings(out)
	return out
}

// ---- SPA contract: real handler output against web/src/types.ts -------------

var tsKeyRe = regexp.MustCompile(`(\w+)\??:`)

// tsBlock returns the text of the exported declaration named name.
func tsBlock(t *testing.T, src, name string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^export (?:interface|type) ` + name + `\b`)
	loc := re.FindStringIndex(src)
	require.NotNil(t, loc, name)
	rest := src[loc[0]:]
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

func tsKeys(s string) []string {
	var keys []string
	for _, m := range tsKeyRe.FindAllStringSubmatch(s, -1) {
		keys = append(keys, m[1])
	}
	sort.Strings(keys)
	return keys
}

// tsInterfaceKeys returns the member names of an interface body: top-level
// members, with an inline nested object listed as parent.child.
func tsInterfaceKeys(t *testing.T, src, name string) (top []string, nested map[string][]string) {
	block := tsBlock(t, src, name)
	nested = map[string][]string{}
	for _, line := range strings.Split(block, "\n")[1:] {
		line = strings.TrimSpace(line)
		m := regexp.MustCompile(`^(\w+)\??:\s*(.*)$`).FindStringSubmatch(line)
		if m == nil {
			continue
		}
		top = append(top, m[1])
		if strings.HasPrefix(m[2], "{") && strings.HasSuffix(m[2], "}") {
			nested[m[1]] = tsKeys(m[2])
		}
	}
	sort.Strings(top)
	return top, nested
}

// tsUnionKeys maps each discriminator value to the member names of its variant.
func tsUnionKeys(t *testing.T, src, name, disc string) map[string][]string {
	out := map[string][]string{}
	valRe := regexp.MustCompile(disc + `:\s*((?:'\w+'\s*\|?\s*)+)`)
	for _, line := range strings.Split(tsBlock(t, src, name), "\n") {
		m := valRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		keys := tsKeys(line[strings.Index(line, "{"):])
		for _, v := range regexp.MustCompile(`'(\w+)'`).FindAllStringSubmatch(m[1], -1) {
			out[v[1]] = keys
		}
	}
	return out
}

func jsonKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestVideoSearchHandlerOutputMatchesSPAShape decodes the real handler output
// against the SPA's declared field names (web/src/types.ts), so a snake_case or
// renamed member breaks this test rather than the browser.
func TestVideoSearchHandlerOutputMatchesSPAShape(t *testing.T) {
	raw, err := os.ReadFile("../../web/src/types.ts")
	require.NoError(t, err)
	src := string(raw)

	f := newVSFix(t)
	t0 := vsNow.Add(-24 * time.Hour)
	prod := f.run("manual", "completed", "run_now", t0)
	claim := f.run("manual", "running", "queue_retry", t0.Add(time.Hour))
	dry := f.run("dry_run", "completed", nil, t0.Add(2*time.Hour))
	v := f.video("shapevid001", t0, prod, "deferred")
	f.held("shapevid001", "Shape probe", "ShapeChan")
	f.outcome(prod, v, "deferred", "quota_exhausted", nil)
	f.decision(v, dry, "matched", nil)
	f.exec(`INSERT INTO deferred_item (video_id, run_id, deferral_reason, status, retry_run_id) VALUES (?,?,?,?,?)`, v, prod, "quota_exhausted", "retrying", claim)
	f.reference("shaperef001", nil)
	cm := f.video("shapecom001", t0, prod, "accepted")
	f.outcome(prod, cm, "accepted", nil, nil)
	f.held("shapecom001", "Shape probe committed", "C")
	f.video("shapeyet001", t0, prod, nil)
	f.held("shapeyet001", "Shape probe yet", "C")
	sim := f.video("shapesim001", t0, dry, nil)
	f.decision(sim, dry, "matched", nil)
	f.held("shapesim001", "Shape probe sim", "C")
	f.exec(`INSERT INTO error_entry (video_id, run_id, error_category, error_message, status) VALUES (?,?,?,?,?)`, cm, prod, "runtime", "m", "active")
	f.exec(`INSERT INTO error_entry (video_id, run_id, error_category, error_message, status, retry_run_id) VALUES (?,?,?,?,?,?)`, cm, prod, "runtime", "m", "retrying", claim)
	f.video("shapedis001", t0, prod, "deferred")
	f.exec(`INSERT INTO deferred_item (video_id, run_id, deferral_reason, status) VALUES ((SELECT id FROM video WHERE video_id='shapedis001'),?,?,?)`, prod, "quota_exhausted", "pending")
	f.held("shapedis001", "Shape probe pending", "C")
	f.video("shapeerr001", t0, prod, "failed")
	f.exec(`INSERT INTO error_entry (video_id, run_id, error_category, error_message, status) VALUES ((SELECT id FROM video WHERE video_id='shapeerr001'),?,?,?,?)`, prod, "runtime", "m", "active")
	f.held("shapeerr001", "Shape probe err", "C")
	f.video("shapegon001", t0, prod, "deferred")
	f.exec(`INSERT INTO deferred_item (video_id, run_id, deferral_reason, status) VALUES ((SELECT id FROM video WHERE video_id='shapegon001'),?,?,?)`, prod, "quota_exhausted", "discarded")
	f.held("shapegon001", "Shape probe gone", "C")

	summaryTop, summaryNested := tsInterfaceKeys(t, src, "VideoSummary")
	runTop, _ := tsInterfaceKeys(t, src, "VideoRunEntry")
	pageTop, _ := tsInterfaceKeys(t, src, "VideoSearchPage")
	latestKeys := tsUnionKeys(t, src, "VideoLatestOutcome", "state")
	queueKeys := tsUnionKeys(t, src, "VideoQueueState", "state")
	respKeys := tsUnionKeys(t, src, "VideoSearchResponse", "kind")
	require.NotEmpty(t, latestKeys)
	require.NotEmpty(t, queueKeys)
	require.NotEmpty(t, respKeys)

	seenLatest, seenQueue := map[string]bool{}, map[string]bool{}
	checkSummary := func(m map[string]any, extra ...string) {
		t.Helper()
		assert.Equal(t, sorted(append(append([]string{}, summaryTop...), extra...)), jsonKeys(m), "summary members")
		for k, want := range summaryNested {
			assert.Equal(t, want, jsonKeys(m[k].(map[string]any)), k)
		}
		for _, c := range []struct {
			field string
			want  map[string][]string
			seen  map[string]bool
		}{{"latest_outcome", latestKeys, seenLatest}, {"queue_state", queueKeys, seenQueue}} {
			obj := m[c.field].(map[string]any)
			state := obj["state"].(string)
			want, ok := c.want[state]
			if assert.True(t, ok, "%s state %q is not declared in types.ts", c.field, state) {
				assert.Equal(t, want, jsonKeys(obj), "%s %s", c.field, state)
			}
			c.seen[state] = true
		}
	}
	decode := func(r vsResp) map[string]any {
		t.Helper()
		require.Equal(t, 200, r.code, string(r.raw))
		var m map[string]any
		dec := json.NewDecoder(bytes.NewReader(r.raw))
		require.NoError(t, dec.Decode(&m))
		return m
	}
	checkResponse := func(m map[string]any) {
		kind := m["kind"].(string)
		want, ok := respKeys[kind]
		require.True(t, ok, "kind %q not declared in types.ts", kind)
		got := jsonKeys(m)
		// `results` is optional on id_not_found.
		if kind == "id_not_found" {
			filtered := []string{}
			for _, k := range got {
				if k != "results" {
					filtered = append(filtered, k)
				}
			}
			var w []string
			for _, k := range want {
				if k != "results" {
					w = append(w, k)
				}
			}
			assert.Equal(t, w, filtered)
		} else {
			assert.Equal(t, want, got, kind)
		}
		if res, ok := m["results"].(map[string]any); ok {
			assert.Equal(t, pageTop, jsonKeys(res), "page members")
			for _, it := range res["items"].([]any) {
				checkSummary(it.(map[string]any))
			}
		}
	}

	for _, q := range []string{"", "lo", strings.Repeat("a", 201), "https://www.youtube.com/watch?list=1", "shape probe", "shapevid001",
		"https://youtu.be/shapevid001", "https://youtu.be/AAAAAAAAAAA", "ShAPEVID001", "\xff\xfe\xfd"} {
		checkResponse(decode(f.search(q)))
	}
	// Cover each queue/latest state through the view endpoint as well.
	for _, id := range []string{"shapevid001", "shaperef001", "shapecom001", "shapeyet001", "shapesim001", "shapedis001", "shapegon001", "shapeerr001"} {
		m := decode(f.get("/api/videos/" + id))
		checkSummary(m, "runs")
		for _, r := range m["runs"].([]any) {
			assert.Equal(t, runTop, jsonKeys(r.(map[string]any)), "run entry members")
		}
	}
	for state := range latestKeys {
		assert.True(t, seenLatest[state], "latest_outcome %s never produced by the fixture", state)
	}
	for state := range queueKeys {
		assert.True(t, seenQueue[state], "queue_state %s never produced by the fixture", state)
	}
	// Run entries must have been produced at least once for the check to bite.
	require.NotEmpty(t, decode(f.get("/api/videos/shapevid001"))["runs"])
}

func sorted(s []string) []string { sort.Strings(s); return s }
