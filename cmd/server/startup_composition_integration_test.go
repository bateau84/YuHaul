//go:build integration

package main

// ADR-007 composition tests. Each case starts the real server composition as a
// child process (this test binary re-executed into main) against a disposable
// SQLite file and a local fake YouTube endpoint, then drives it over HTTP and
// SIGTERM. Nothing here can reach a live provider: the child's only YouTube
// base URL is the loopback fake, and its OAuth token is a synthetic encrypted
// row.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
)

const (
	childMainEnv  = "YUHAUL_TEST_CHILD_MAIN"
	childKey      = "01234567890123456789012345678901"
	runDoneBudget = 40 * time.Second
	exitBudget    = 25 * time.Second
)

// TestMain turns the test binary into the server when asked to, so the tests
// run the real main() in its own process.
func TestMain(m *testing.M) {
	if os.Getenv(childMainEnv) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeYouTube serves the Data API calls one production run makes. Hooks let a
// test hold discovery or a playlist insertion open.
type fakeYouTube struct {
	srv    *httptest.Server
	videos []string

	mu          sync.Mutex
	subHold     chan struct{}
	subReached  chan struct{}
	postHold    chan struct{}
	postReached chan string
	posts       map[string]int
	quotaVideo  string // POST for this video answers 403 quotaExceeded
}

func newFakeYouTube(t *testing.T, videos ...string) *fakeYouTube {
	t.Helper()
	f := &fakeYouTube{videos: videos, posts: map[string]int{}, subReached: make(chan struct{}), postReached: make(chan string, 16)}
	mux := http.NewServeMux()
	mux.HandleFunc("/subscriptions", f.handleSubscriptions)
	mux.HandleFunc("/channels", func(w http.ResponseWriter, r *http.Request) {
		fakeJSON(w, map[string]any{"items": []map[string]any{{
			"id": "UC1", "snippet": map[string]any{"title": "Test Channel"},
			"contentDetails": map[string]any{"relatedPlaylists": map[string]any{"uploads": "UUPL1"}},
		}}})
	})
	mux.HandleFunc("/playlistItems", f.handlePlaylistItems)
	mux.HandleFunc("/videos", f.handleVideos)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func fakeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeYouTube) hold(ch chan struct{}, r *http.Request) bool {
	if ch == nil {
		return true
	}
	select {
	case <-ch:
		return true
	case <-r.Context().Done():
		return false
	}
}

func (f *fakeYouTube) handleSubscriptions(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	hold := f.subHold
	f.mu.Unlock()
	if hold != nil {
		select {
		case <-f.subReached:
		default:
			close(f.subReached)
		}
		if !f.hold(hold, r) {
			return
		}
	}
	fakeJSON(w, map[string]any{"items": []map[string]any{{"snippet": map[string]any{"resourceId": map[string]any{"channelId": "UC1"}}}}})
}

func (f *fakeYouTube) handlePlaylistItems(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			Snippet struct {
				PlaylistID string `json:"playlistId"`
				ResourceID struct {
					VideoID string `json:"videoId"`
				} `json:"resourceId"`
			} `json:"snippet"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		video := body.Snippet.ResourceID.VideoID
		f.mu.Lock()
		f.posts[video]++
		hold := f.postHold
		quota := f.quotaVideo == video
		f.mu.Unlock()
		f.postReached <- video
		if quota {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":{"code":403,"message":"quota","errors":[{"reason":"quotaExceeded","domain":"youtube.quota"}]}}`)
			return
		}
		if hold != nil && !f.hold(hold, r) {
			return
		}
		fakeJSON(w, map[string]any{"id": "inserted-" + video, "snippet": body.Snippet})
		return
	}
	published := time.Now().UTC().Add(-time.Hour)
	items := make([]map[string]any, len(f.videos))
	for i, id := range f.videos {
		items[i] = map[string]any{"snippet": map[string]any{
			"title": "Jazz " + id, "channelId": "UC1",
			"publishedAt": published.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano),
			"resourceId":  map[string]any{"videoId": id, "kind": "youtube#video"},
			"thumbnails":  map[string]any{"default": map[string]any{"url": "http://example.com/t.jpg"}},
		}}
	}
	fakeJSON(w, map[string]any{"items": items})
}

func (f *fakeYouTube) handleVideos(w http.ResponseWriter, r *http.Request) {
	published := time.Now().UTC().Add(-time.Hour)
	items := make([]map[string]any, len(f.videos))
	for i, id := range f.videos {
		items[i] = map[string]any{"id": id,
			"snippet": map[string]any{
				"title": "Jazz " + id, "channelId": "UC1", "channelTitle": "Test Channel", "description": "jazz",
				"publishedAt": published.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano),
				"thumbnails":  map[string]any{"default": map[string]any{"url": "http://example.com/t.jpg"}},
			},
			"contentDetails": map[string]any{"duration": "PT4M30S"},
		}
	}
	fakeJSON(w, map[string]any{"items": items})
}

func (f *fakeYouTube) setSubscriptionHold(ch chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subHold = ch
}

func (f *fakeYouTube) setQuotaVideo(video string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.quotaVideo = video
}

func (f *fakeYouTube) setPostHold(ch chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.postHold = ch
}

func (f *fakeYouTube) postCount(video string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posts[video]
}

// seedInterruptedRun builds a disposable database with a synthetic encrypted
// token, a Jazz route and one version-1 interrupted run that has not started
// discovery, exactly what a restart after process loss finds.
func seedInterruptedRun(t *testing.T, dbPath string) (runID, configVersionID int64) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, dbPath)
	require.NoError(t, err)
	defer db.Close()
	tokens, err := auth.NewSQLiteTokenStore(db, []byte(childKey))
	require.NoError(t, err)
	require.NoError(t, tokens.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "local-test", RefreshToken: "local-refresh", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour), Scopes: auth.DefaultScopes()}))
	routerStore := router.NewStore(db)
	_, err = routerStore.CreateRoute(ctx, &router.Route{
		Name: "Jazz", EvaluationOrder: 1, IsEnabled: true, ConditionComposition: "OR",
		Conditions: []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "jazz"}},
		Actions:    []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLjazz"}},
	})
	require.NoError(t, err)
	stateStore, err := state.NewStore(db)
	require.NoError(t, err)
	cfgStore := config.NewStore(db)
	p := pipeline.NewPipeline(db, state.NewMachine(stateStore), stateStore, nil, nil, nil, nil, nil, nil, nil, pipeline.NewBroadcaster(),
		pipeline.WithVersioner(config.NewService(routerStore, cfgStore)), pipeline.WithConfigStore(cfgStore))
	run, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	require.NoError(t, stateStore.InterruptRun(ctx, run.ID, "test: process stopped"))
	require.NotNil(t, run.ConfigVersionID)
	return run.ID, *run.ConfigVersionID
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type serverProcess struct {
	t      *testing.T
	cmd    *exec.Cmd
	out    *syncBuffer
	addr   string
	exited chan struct{}
	err    error
	start  time.Time
}

func startServer(t *testing.T, dbPath, youtubeURL string) *serverProcess {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(),
		childMainEnv+"=1",
		"TMPDIR="+t.TempDir(),
		"YUHAUL_DB_PATH="+dbPath,
		"YUHAUL_HTTP_ADDR="+addr,
		"YUHAUL_WEB_ROOT="+t.TempDir(),
		"YUHAUL_LOG_LEVEL=info",
		"YUHAUL_OAUTH_CLIENT_ID=test",
		"YUHAUL_OAUTH_CLIENT_SECRET=test",
		"YUHAUL_OAUTH_REDIRECT_URL=http://localhost/callback",
		"YUHAUL_ENCRYPTION_KEY="+childKey,
		"YUHAUL_YOUTUBE_BASE_URL="+youtubeURL,
	)
	out := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	p := &serverProcess{t: t, cmd: cmd, out: out, addr: addr, exited: make(chan struct{}), start: time.Now()}
	require.NoError(t, cmd.Start())
	go func() { p.err = cmd.Wait(); close(p.exited) }()
	t.Cleanup(func() {
		select {
		case <-p.exited:
		default:
			_ = cmd.Process.Kill()
			<-p.exited
		}
	})
	return p
}

// sigterm sends SIGTERM and requires a clean exit within the budget.
func (p *serverProcess) sigterm() {
	p.t.Helper()
	require.NoError(p.t, p.cmd.Process.Signal(syscall.SIGTERM))
	select {
	case <-p.exited:
		require.NoError(p.t, p.err, "the server must exit 0 after SIGTERM; log:\n%s", p.out.String())
	case <-time.After(exitBudget):
		p.t.Fatalf("server did not exit within %s of SIGTERM; log:\n%s", exitBudget, p.out.String())
	}
}

func (p *serverProcess) do(method, path string) (int, string) {
	p.t.Helper()
	req, err := http.NewRequest(method, "http://"+p.addr+path, nil)
	require.NoError(p.t, err)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(p.t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(p.t, err)
	return resp.StatusCode, strings.TrimSpace(string(body))
}

// waitOverview polls the probe path until it answers 200 and returns when.
func (p *serverProcess) waitOverview() time.Duration {
	p.t.Helper()
	client := &http.Client{Timeout: time.Second}
	for time.Since(p.start) < 30*time.Second {
		resp, err := client.Get("http://" + p.addr + "/api/overview")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return time.Since(p.start)
			}
		}
		select {
		case <-p.exited:
			p.t.Fatalf("server exited before serving: %v\n%s", p.err, p.out.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	p.t.Fatalf("server never served /api/overview\n%s", p.out.String())
	return 0
}

func openReadDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_busy_timeout=5000")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func scalar[T any](t *testing.T, db *sql.DB, query string, args ...any) T {
	t.Helper()
	var v T
	require.NoError(t, db.QueryRow(query, args...).Scan(&v))
	return v
}

func eventually(t *testing.T, budget time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Required test 1: liveness and reads are independent of run length; every
// production producer is refused with the exact 503 until recovery finishes,
// then the gate opens and the scheduler starts.
func TestStartupResumeIsBackgroundBehindProducerGate(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
	runID, _ := seedInterruptedRun(t, dbPath)
	yt := newFakeYouTube(t, "V1")
	hold := make(chan struct{})
	yt.setSubscriptionHold(hold)
	db := openReadDB(t, dbPath)

	srv := startServer(t, dbPath, yt.srv.URL)
	took := srv.waitOverview()
	require.Less(t, took, 2*time.Second, "/api/overview must answer within 2 s of start\n%s", srv.out.String())
	select {
	case <-yt.subReached:
	case <-time.After(20 * time.Second):
		t.Fatalf("the recovered run never started discovery\n%s", srv.out.String())
	}
	require.NotEqual(t, "completed", scalar[string](t, db, `SELECT status FROM run WHERE id=?`, runID), "the run is still executing")

	runs := scalar[int](t, db, `SELECT count(*) FROM run`)
	versions := scalar[int](t, db, `SELECT count(*) FROM config_version`)
	claims := scalar[int](t, db, `SELECT (SELECT count(*) FROM deferred_item)+(SELECT count(*) FROM error_entry)`)
	const refused = `{"error":"startup recovery in progress"}`
	for _, path := range []string{"/api/runs", "/api/errors/1/retry", "/api/deferred/1/retry"} {
		status, body := srv.do(http.MethodPost, path)
		require.Equal(t, http.StatusServiceUnavailable, status, path)
		require.Equal(t, refused, body, path)
	}
	require.Equal(t, runs, scalar[int](t, db, `SELECT count(*) FROM run`), "a refused producer creates no run")
	require.Equal(t, versions, scalar[int](t, db, `SELECT count(*) FROM config_version`), "...and no snapshot")
	require.Equal(t, claims, scalar[int](t, db, `SELECT (SELECT count(*) FROM deferred_item)+(SELECT count(*) FROM error_entry)`), "...and no claim")
	require.NotContains(t, srv.out.String(), "scheduler started", "the scheduler waits for recovery")

	close(hold)
	eventually(t, runDoneBudget, "the recovered run to complete", func() bool {
		return scalar[string](t, db, `SELECT status FROM run WHERE id=?`, runID) == "completed"
	})
	eventually(t, 10*time.Second, "the scheduler to start", func() bool { return strings.Contains(srv.out.String(), "scheduler started") })
	status, body := srv.do(http.MethodPost, "/api/runs")
	require.Equal(t, http.StatusAccepted, status, body)
	var accepted struct {
		RunID int64 `json:"run_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &accepted))
	require.Greater(t, accepted.RunID, runID)
	srv.sigterm()
}

// Required test 3: SIGTERM while a resumed run is in discovery (before any
// admission) stops the process within its budget, leaves the run interrupted
// at its saved phase with a canceled diagnostic, and a restart resumes the
// same run and snapshot.
func TestSigtermDuringResumeInterruptsAndRestartResumesSameRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
	runID, configID := seedInterruptedRun(t, dbPath)
	yt := newFakeYouTube(t, "V1")
	yt.setSubscriptionHold(make(chan struct{})) // never released
	db := openReadDB(t, dbPath)

	srv := startServer(t, dbPath, yt.srv.URL)
	srv.waitOverview()
	select {
	case <-yt.subReached:
	case <-time.After(20 * time.Second):
		t.Fatalf("the recovered run never started discovery\n%s", srv.out.String())
	}
	srv.sigterm()

	require.Equal(t, "interrupted", scalar[string](t, db, `SELECT status FROM run WHERE id=?`, runID))
	require.Contains(t, scalar[string](t, db, `SELECT interrupt_reason FROM run WHERE id=?`, runID), "code=canceled")
	require.Equal(t, "discovery", scalar[string](t, db, `SELECT current_phase FROM run WHERE id=?`, runID))
	require.Zero(t, scalar[int](t, db, `SELECT count(*) FROM run WHERE status='failed'`))
	require.Zero(t, scalar[int](t, db, `SELECT count(*) FROM error_entry`))
	require.NotContains(t, srv.out.String(), "scheduler started")

	yt.setSubscriptionHold(nil)
	restarted := startServer(t, dbPath, yt.srv.URL)
	eventually(t, runDoneBudget, "the same run to complete after restart", func() bool {
		return scalar[string](t, db, `SELECT status FROM run WHERE id=?`, runID) == "completed"
	})
	require.Equal(t, configID, scalar[int64](t, db, `SELECT config_version_id FROM run WHERE id=?`, runID))
	require.Equal(t, 1, scalar[int](t, db, `SELECT count(*) FROM run`), "restart resumed the run, it did not create another")
	restarted.sigterm()
}

// Required test 4: SIGTERM while an admitted POST is outstanding keeps the
// guard, records the uncertain attempt, interrupts the run, and after restart
// the same target is never POSTed again while other targets proceed.
func TestSigtermDuringAdmittedPostKeepsGuardAndNeverReplays(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
	runID, _ := seedInterruptedRun(t, dbPath)
	yt := newFakeYouTube(t, "V1", "V2")
	yt.setPostHold(make(chan struct{})) // the response never arrives
	db := openReadDB(t, dbPath)

	srv := startServer(t, dbPath, yt.srv.URL)
	srv.waitOverview()
	select {
	case video := <-yt.postReached:
		require.Equal(t, "V1", video)
	case <-time.After(30 * time.Second):
		t.Fatalf("no playlist insertion was admitted\n%s", srv.out.String())
	}
	srv.sigterm()

	require.Equal(t, "interrupted", scalar[string](t, db, `SELECT status FROM run WHERE id=?`, runID))
	require.Contains(t, scalar[string](t, db, `SELECT interrupt_reason FROM run WHERE id=?`, runID), "code=canceled")
	require.Equal(t, 1, scalar[int](t, db, `SELECT count(*) FROM playlist_send_guard`), "the guard of the admitted POST is kept")
	require.Equal(t, "failed", scalar[string](t, db, `SELECT outcome FROM executed_action WHERE run_id=?`, runID))
	require.Equal(t, "mutation_application_uncertain", scalar[string](t, db, `SELECT outcome_reason FROM executed_action WHERE run_id=?`, runID))
	require.NotEmpty(t, scalar[sql.NullString](t, db, `SELECT executed_at FROM executed_action WHERE run_id=?`, runID).String, "an admitted attempt keeps its time")
	require.Equal(t, 1, yt.postCount("V1"))
	require.Zero(t, yt.postCount("V2"))

	yt.setPostHold(nil)
	restarted := startServer(t, dbPath, yt.srv.URL)
	eventually(t, runDoneBudget, "the run to complete after restart", func() bool {
		return scalar[string](t, db, `SELECT status FROM run WHERE id=?`, runID) == "completed"
	})
	require.Equal(t, 1, yt.postCount("V1"), "zero further POSTs for the guarded target")
	require.Equal(t, 1, yt.postCount("V2"), "other targets proceed")
	require.Equal(t, 1, scalar[int](t, db, `SELECT count(*) FROM playlist_send_guard`), "the guard is never cleared by a restart")
	restarted.sigterm()
}

// Required test 6, process level: SIGTERM while classification is paused
// inside its transaction exits cleanly, rolls the transaction back, and the
// next start classifies the same run once and finishes it.
func TestSigtermDuringClassificationExitsAndNextStartClassifiesOnce(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
	runID, _ := seedInterruptedRun(t, dbPath)
	yt := newFakeYouTube(t, "V1")
	seed, err := sql.Open("sqlite", dbPath+"?_busy_timeout=5000")
	require.NoError(t, err)
	_, err = seed.Exec(`UPDATE run SET status='running', interrupt_reason=NULL WHERE id=?`, runID)
	require.NoError(t, err)
	// Pause point: the classification UPDATE runs until it is interrupted.
	_, err = seed.Exec(`CREATE TRIGGER pause_startup BEFORE UPDATE ON run BEGIN
		SELECT count(*) FROM (WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c) SELECT x FROM c LIMIT 4000000000);
	END`)
	require.NoError(t, err)
	db := openReadDB(t, dbPath)

	srv := startServer(t, dbPath, yt.srv.URL)
	time.Sleep(1500 * time.Millisecond) // let startup reach the paused transaction
	select {
	case <-srv.exited:
		t.Fatalf("server exited before SIGTERM: %v\n%s", srv.err, srv.out.String())
	default:
	}
	srv.sigterm()
	require.Contains(t, srv.out.String(), "shutdown during startup classification")
	require.Equal(t, "running", scalar[string](t, db, `SELECT status FROM run WHERE id=?`, runID), "the paused classification rolled back")

	_, err = seed.Exec(`DROP TRIGGER pause_startup`)
	require.NoError(t, err)
	require.NoError(t, seed.Close())
	restarted := startServer(t, dbPath, yt.srv.URL)
	eventually(t, runDoneBudget, "the run to be classified once and finished", func() bool {
		return scalar[string](t, db, `SELECT status FROM run WHERE id=?`, runID) == "completed"
	})
	require.Equal(t, 1, strings.Count(restarted.out.String(), `"msg":"recovered runs"`), "one classification, one worker")
	restarted.sigterm()
}

// ADR-007 §3.4 (queue retries): SIGTERM while a queue retry's admitted POST is
// held open. The retry run belongs to the shutdown lifecycle, so it ends
// `interrupted` with a phase/operation-bearing reason, the admitted attempt
// keeps its uncertain evidence and guard, and its quota hold is finalized. The
// server exits within the drain budget instead of timing out HTTP shutdown.
func TestSigtermDuringQueueRetryAdmittedPostInterruptsRetryRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
	yt := newFakeYouTube(t, "V1", "V2")
	yt.setQuotaVideo("V2")
	// seedInterruptedRun leaves one resumable run; the first process finishes
	// it, deferring V2 on the provider's quota refusal.
	firstRun, _ := seedInterruptedRun(t, dbPath)
	db := openReadDB(t, dbPath)
	first := startServer(t, dbPath, yt.srv.URL)
	eventually(t, runDoneBudget, "the seeded run to complete", func() bool {
		return scalar[string](t, db, `SELECT status FROM run WHERE id=?`, firstRun) == "completed"
	})
	first.sigterm()
	deferredID := scalar[int64](t, db, `SELECT d.id FROM deferred_item d JOIN video v ON v.id=d.video_id WHERE v.video_id='V2' AND d.status='pending'`)
	rw, err := sql.Open("sqlite", dbPath+"?_busy_timeout=5000")
	require.NoError(t, err)
	_, err = rw.Exec(`UPDATE deferred_item SET retry_after=NULL`)
	require.NoError(t, err)
	require.NoError(t, rw.Close())

	// Second process: the retry's POST is admitted and then held.
	yt.setQuotaVideo("")
	yt.setPostHold(make(chan struct{}))
	for len(yt.postReached) > 0 {
		<-yt.postReached
	}
	srv := startServer(t, dbPath, yt.srv.URL)
	srv.waitOverview()
	retryDone := make(chan struct{})
	go func() {
		defer close(retryDone)
		client := &http.Client{Timeout: 60 * time.Second}
		req, _ := http.NewRequest(http.MethodPost, "http://"+srv.addr+"/api/deferred/"+strconv.FormatInt(deferredID, 10)+"/retry", nil)
		if resp, err := client.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	select {
	case video := <-yt.postReached:
		require.Equal(t, "V2", video)
	case <-time.After(30 * time.Second):
		t.Fatalf("the retry's playlist insertion was not admitted\n%s", srv.out.String())
	}
	srv.sigterm()
	<-retryDone

	retryRun := scalar[int64](t, db, `SELECT id FROM run WHERE id>? AND run_type='manual' ORDER BY id DESC LIMIT 1`, firstRun)
	require.Equal(t, "interrupted", scalar[string](t, db, `SELECT status FROM run WHERE id=?`, retryRun), srv.out.String())
	reason := scalar[string](t, db, `SELECT interrupt_reason FROM run WHERE id=?`, retryRun)
	require.Equal(t, "diag:v1 phase=action_execution operation=retry code=canceled", reason)
	require.NotContains(t, srv.out.String(), "graceful shutdown failed")
	require.Equal(t, "failed", scalar[string](t, db, `SELECT outcome FROM executed_action WHERE run_id=?`, retryRun))
	require.Equal(t, "mutation_application_uncertain", scalar[string](t, db, `SELECT outcome_reason FROM executed_action WHERE run_id=?`, retryRun))
	require.NotEmpty(t, scalar[sql.NullString](t, db, `SELECT executed_at FROM executed_action WHERE run_id=?`, retryRun).String, "the admitted attempt keeps its time")
	require.Equal(t, 1, scalar[int](t, db, `SELECT count(*) FROM playlist_send_guard WHERE run_id=?`, retryRun), "the admitted attempt keeps its guard")
	require.Equal(t, 1, scalar[int](t, db, `SELECT count(*) FROM quota_ledger WHERE run_id=? AND operation_type LIKE '%insert%'`, retryRun), "the admitted hold is finalized")
	require.Equal(t, 1, yt.postCount("V2")-1, "exactly one POST for V2 in the retry process")
}
