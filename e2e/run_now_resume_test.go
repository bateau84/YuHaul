//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/server"
	"github.com/bateau84/YuHaul/internal/state"
)

// Run Now resume through the registered POST /api/runs handler (run-initiation
// ADR-001, ARS-001 §8.4 H1/H2 and §8.6 E1): real binary path pieces — handler,
// pipeline, executor, auth service, migrated SQLite — with only the YouTube
// endpoint controlled. The time limit is shortened through
// server.WithRunTimeLimit, which keeps the production WithTimeoutCause path.

type runNowHarness struct {
	env     *testEnv
	api     *server.Server
	http    *httptest.Server
	client  *http.Client
	base    string
	blocked chan string
	release chan struct{}
	once    sync.Once
}

func newRunNowHarness(t *testing.T, limit time.Duration) *runNowHarness {
	t.Helper()
	published := time.Now().UTC().Add(-time.Hour)
	var videos []mockVideo
	for i := 1; i <= 3; i++ {
		v := makeVideo(fmt.Sprintf("V%d", i), fmt.Sprintf("Jazz Session %d", i), published.Add(time.Duration(i)*time.Minute))
		videos = append(videos, v)
	}
	env := setupEnv(t, []mockChannel{{ID: "UC1", Title: "Test Channel", UploadsPlaylistID: "UUPL1"}}, videos)
	env.createRoute(t, "Jazz", "jazz", "PLjazz")
	h := &runNowHarness{env: env, blocked: make(chan string, 16), release: make(chan struct{})}
	h.api = server.NewServer(env.pipeline, env.broadcast,
		server.WithRunStore(env.stateStore), server.WithRunVideoStore(env.stateStore),
		server.WithConfigVersionStore(env.configStore), server.WithRunTimeLimit(limit))
	h.http = httptest.NewServer(h.api.Handler())
	t.Cleanup(h.http.Close)
	h.client, h.base = guardClient(t, h.http.Client()), h.http.URL
	t.Cleanup(h.unblock) // runs before the endpoint's Close
	return h
}

// stallOn holds the playlist POST for videoID in flight until the run's
// context ends, then answers a failure so nothing is recorded as sent.
func (h *runNowHarness) stallOn(videoID string) {
	h.env.server.mu.Lock()
	defer h.env.server.mu.Unlock()
	h.env.server.insertHook = func(_, v string) (int, string) {
		if v != videoID {
			return 0, ""
		}
		h.blocked <- v
		select {
		case <-h.release:
		case <-time.After(30 * time.Second):
		}
		return http.StatusInternalServerError, `{"error":{"errors":[{"reason":"backendError"}],"code":500}}`
	}
}

func (h *runNowHarness) noStall() {
	h.env.server.mu.Lock()
	defer h.env.server.mu.Unlock()
	h.env.server.insertHook = nil
}

type pressResult struct {
	Status    int
	RunID     int64  `json:"run_id"`
	RunStatus string `json:"status"`
	Admission string `json:"admission"`
	Code      string `json:"code"`
	Reason    string `json:"reason"`
	Error     string `json:"error"`
	raw       string
}

func (h *runNowHarness) press(t *testing.T) pressResult {
	t.Helper()
	status, body := postJSON(t, h.client, h.base+"/api/runs", nil)
	var r pressResult
	require.NoError(t, json.Unmarshal(body, &r), string(body))
	r.Status, r.raw = status, string(body)
	return r
}

func (h *runNowHarness) waitStatus(t *testing.T, runID int64, want state.RunStatus) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		run, err := h.env.stateStore.GetRun(context.Background(), runID)
		require.NoError(t, err)
		if run.Status == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("run %d never reached %s", runID, want)
}

func (h *runNowHarness) runCount(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, h.env.db.QueryRow(`SELECT COUNT(*) FROM run`).Scan(&n))
	return n
}

func (h *runNowHarness) causeOf(t *testing.T, runID int64) (cause *string) {
	t.Helper()
	var c *string
	require.NoError(t, h.env.db.QueryRow(`SELECT interrupt_cause FROM run WHERE id=?`, runID).Scan(&c))
	return c
}

// E1 + H2: the first press times out mid-actions through the handler's real
// time-limit context; the second press returns the same run with
// admission=resumed, sends only what remains, and completes it.
func TestE2E_RunNowResumesTimeLimitedRun(t *testing.T) {
	h := newRunNowHarness(t, 4*time.Second)
	h.stallOn("V2")

	first := h.press(t)
	require.Equal(t, http.StatusAccepted, first.Status, first.raw)
	require.Equal(t, "started", first.Admission)
	require.Equal(t, "running", first.RunStatus)
	h.waitStatus(t, first.RunID, state.RunStatusInterrupted)
	require.Len(t, h.blocked, 1, "the limit must expire at V2's in-flight POST, not earlier")
	cause := h.causeOf(t, first.RunID)
	require.NotNil(t, cause, "the handler's context must carry ErrRunTimeLimit")
	require.Equal(t, "time_limit", *cause)
	require.Equal(t, []playlistInsertion{{PlaylistID: "PLjazz", VideoID: "V1"}}, h.env.server.Insertions())
	require.Equal(t, 1, h.runCount(t))

	h.noStall()
	second := h.press(t)
	require.Equal(t, http.StatusAccepted, second.Status, second.raw)
	require.Equal(t, "resumed", second.Admission)
	require.Equal(t, first.RunID, second.RunID, "the same run")
	require.Equal(t, "running", second.RunStatus)
	h.waitStatus(t, second.RunID, state.RunStatusCompleted)

	require.Equal(t, 1, h.runCount(t), "the run count rose by exactly one in total")
	require.Nil(t, h.causeOf(t, first.RunID), "a completed run keeps no stale cause after the resume cleared it")
	require.Equal(t, []playlistInsertion{
		{PlaylistID: "PLjazz", VideoID: "V1"},
		{PlaylistID: "PLjazz", VideoID: "V3"},
	}, h.env.server.Insertions(), "V1 already sent, V2 held by its send guard, only V3 remained")

	drain, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, h.api.WaitRuns(drain), "every press released its run slot")
}

// H1: through the registered handler a press during an active run is refused
// at once with 409 production_run_active, an unusable candidate with 409
// resume_unavailable (run ID and closed reason), and neither changes a run.
func TestE2E_RunNowRefusals(t *testing.T) {
	h := newRunNowHarness(t, 30*time.Second)
	h.stallOn("V1")

	active := h.press(t)
	require.Equal(t, http.StatusAccepted, active.Status, active.raw)
	require.Equal(t, "started", active.Admission)
	select {
	case <-h.blocked:
	case <-time.After(15 * time.Second):
		t.Fatal("the first run never reached its provider call")
	}
	runs := h.runCount(t)

	start := time.Now()
	busy := h.press(t)
	require.Less(t, time.Since(start), 3*time.Second, "a busy press must not wait for the active run")
	require.Equal(t, http.StatusConflict, busy.Status, busy.raw)
	require.Equal(t, "production_run_active", busy.Code)
	require.Equal(t, "a production run is already active", busy.Error)
	require.Equal(t, runs, h.runCount(t))

	// Let the active run finish: release the provider and wait for completion.
	h.noStall()
	h.unblock()
	h.waitStatus(t, active.RunID, state.RunStatusCompleted)

	// An eligible but unusable candidate refuses the press and starts nothing.
	var cfg int64
	require.NoError(t, h.env.db.QueryRow(`SELECT config_version_id FROM run WHERE id=?`, active.RunID).Scan(&cfg))
	res, err := h.env.db.Exec(`INSERT INTO run (started_at, run_type, status, config_version_id, action_authority_version,
 mutation_safety_version, initiated_by, interrupt_cause, current_phase)
 VALUES (?, 'manual', 'interrupted', ?, 1, 1, 'run_now', 'time_limit', 'bogus')`,
		time.Now().UTC().Add(-time.Minute).Format(state.RunTimestampLayout), cfg)
	require.NoError(t, err)
	badID, err := res.LastInsertId()
	require.NoError(t, err)
	runs = h.runCount(t)

	refused := h.press(t)
	require.Equal(t, http.StatusConflict, refused.Status, refused.raw)
	require.Equal(t, "resume_unavailable", refused.Code)
	require.Equal(t, badID, refused.RunID)
	require.Equal(t, "checkpoint_unrecognized", refused.Reason)
	require.Equal(t, fmt.Sprintf("run %d stopped at its time limit but cannot be resumed safely; no new run was started", badID), refused.Error)
	require.Equal(t, runs, h.runCount(t), "no run was started")
	var status string
	require.NoError(t, h.env.db.QueryRow(`SELECT status FROM run WHERE id=?`, badID).Scan(&status))
	require.Equal(t, "interrupted", status)

	drain, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, h.api.WaitRuns(drain), "refusals released their run slots")
}

// unblock releases every stalled provider call; it is safe to call twice.
func (h *runNowHarness) unblock() { h.once.Do(func() { close(h.release) }) }
