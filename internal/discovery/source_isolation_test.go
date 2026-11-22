package discovery

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/diag"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/youtube"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// Tests for discovery ADR-001 (interim channel-granular isolation of
// unavailable uploads sources). They run the production RunBound path against
// a fake YouTube API.

const playlistNotFoundBody = `{"error":{"errors":[{"reason":"playlistNotFound"}]}}`

func chID(c byte) string { return "UC" + strings.Repeat(string(c), 22) }

// srcSpec describes one subscribed channel's uploads playlist.
type srcSpec struct {
	id    string
	pages [][]string // video IDs per page; every item carries this channel as snippet channel
	// failAt is the 1-based page whose request fails with failStatus/failBody (0: none).
	failAt     int
	failStatus int
	failBody   string
	// snippetChannel overrides the channel ID carried by each playlist item.
	snippetChannel string
	// noUploads makes /channels report no uploads playlist for this channel.
	noUploads bool
}

func (s srcSpec) playlist() string { return "UU" + s.id[2:] }

type isoHarness struct {
	db      *sql.DB
	engine  *Engine
	binding auth.AuthorizationBinding
	ledger  *quota.Ledger
	runID   int64
	logs    *bytes.Buffer
	mu      sync.Mutex
	reqs    map[string]int // playlist ID -> playlistItems requests
	specs   map[string]srcSpec
	baseURL string
	// videosFail makes videos.list answer 500 (a later prefetch failure).
	videosFail bool
	state      *state.Store
	// subsFail makes subscriptions.list answer 403 quotaExceeded.
	subsFail bool
	// channelsFailCall is the 1-based channels.list call that answers 403
	// quotaExceeded (0: none). channelCalls counts the calls received.
	channelsFailCall int
	channelCalls     int
	// breakLedgerAfterChannels drops the quota settings table once channels.list
	// answered, so the next budget check hits a storage error.
	breakLedgerAfterChannels bool
}

const quotaExceededBody = `{"error":{"errors":[{"reason":"quotaExceeded"}]}}`

func newIsoHarness(t *testing.T, specs []srcSpec, limit int) *isoHarness {
	t.Helper()
	ctx := context.Background()
	h := &isoHarness{db: openTestDB(t), logs: &bytes.Buffer{}, reqs: map[string]int{}}
	key := []byte("01234567890123456789012345678901")
	ts, err := auth.NewSQLiteTokenStore(h.db, key)
	require.NoError(t, err)
	service, err := auth.NewService(&auth.ServiceConfig{ClientID: "client", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(key)}, ts, auth.NewInMemoryStateStore())
	require.NoError(t, err)
	require.NoError(t, ts.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "tok", RefreshToken: "refresh", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour), Scopes: auth.DefaultScopes()}))
	h.binding, err = service.Bind(ctx)
	require.NoError(t, err)

	now := time.Now().UTC()
	h.specs = map[string]srcSpec{}
	for _, s := range specs {
		h.specs[s.playlist()] = s
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		if h.subsFail {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(quotaExceededBody))
			return
		}
		var items []map[string]any
		for _, s := range specs {
			items = append(items, map[string]any{"snippet": map[string]any{"resourceId": map[string]any{"channelId": s.id}}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	})
	mux.HandleFunc("/channels", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.channelCalls++
		call := h.channelCalls
		h.mu.Unlock()
		if h.channelsFailCall == call {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(quotaExceededBody))
			return
		}
		if h.breakLedgerAfterChannels {
			defer func() { _, _ = h.db.Exec(`DROP TABLE quota_settings`) }()
		}
		var items []map[string]any
		for _, s := range specs {
			uploads := s.playlist()
			if s.noUploads {
				uploads = ""
			}
			items = append(items, map[string]any{"id": s.id, "snippet": map[string]any{"title": "Title " + s.id}, "contentDetails": map[string]any{"relatedPlaylists": map[string]any{"uploads": uploads}}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	})
	mux.HandleFunc("/playlistItems", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		s := h.specs[r.URL.Query().Get("playlistId")]
		h.reqs[s.playlist()]++
		h.mu.Unlock()
		page := 1
		if tok := r.URL.Query().Get("pageToken"); tok != "" {
			_, _ = fmt.Sscanf(tok, "p%d", &page)
		}
		if s.failAt == page {
			w.WriteHeader(s.failStatus)
			_, _ = w.Write([]byte(s.failBody))
			return
		}
		var items []map[string]any
		snippetChannel := s.id
		if s.snippetChannel != "" {
			snippetChannel = s.snippetChannel
		}
		for i, id := range s.pages[page-1] {
			items = append(items, playlistItemJSON(id, "Title "+id, snippetChannel, now.Add(-time.Duration(page*10+i+1)*time.Hour)))
		}
		resp := map[string]any{"items": items}
		if page < len(s.pages) {
			resp["nextPageToken"] = fmt.Sprintf("p%d", page+1)
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/videos", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		fail := h.videosFail
		h.mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"errors":[{"reason":"backendError"}]}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{}})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	h.baseURL = server.URL

	st, err := state.NewStore(h.db)
	require.NoError(t, err)
	h.state = st
	h.runID = h.newRun(t)
	h.ledger = quota.New(h.db)
	require.NoError(t, h.ledger.SetDailyLimit(ctx, limit))
	logger := slog.New(slog.NewTextHandler(h.logs, nil))
	h.engine = NewEngine(service, state.NewMachine(st), h.ledger, h.db, WithBaseURL(server.URL), WithLogger(logger))
	return h
}

func (h *isoHarness) newRun(t *testing.T) int64 {
	t.Helper()
	id, err := h.state.CreateRun(context.Background(), &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning})
	require.NoError(t, err)
	return id
}

// setFailAt changes when (1-based page; 0 never) the channel's playlist fails.
func (h *isoHarness) setFailAt(c byte, failAt int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.specs["UU"+strings.Repeat(string(c), 22)]
	s.failAt = failAt
	h.specs[s.playlist()] = s
}

func (h *isoHarness) requests(c byte) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reqs["UU"+strings.Repeat(string(c), 22)]
}

type failureRow struct {
	ID                        int64
	RunID                     int64
	Channel, At, Category, Op string
	Status                    int
	Reason                    string
}

func (h *isoHarness) failureRows(t *testing.T) []failureRow {
	t.Helper()
	rows, err := h.db.Query(`SELECT id, run_id, channel_id, occurred_at, category, operation, http_status, reason FROM discovery_source_failure ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	var out []failureRow
	for rows.Next() {
		var r failureRow
		require.NoError(t, rows.Scan(&r.ID, &r.RunID, &r.Channel, &r.At, &r.Category, &r.Op, &r.Status, &r.Reason))
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

func (h *isoHarness) run() (*Result, error) {
	return h.engine.RunBound(context.Background(), h.runID, h.binding)
}

func (h *isoHarness) videoCount(t *testing.T, id string) int {
	t.Helper()
	var n int
	require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM video WHERE video_id = ?`, id).Scan(&n))
	return n
}

type channelRow struct {
	exists     bool
	floor      sql.NullTime
	discovered int
}

func (h *isoHarness) channel(t *testing.T, id string) channelRow {
	t.Helper()
	var r channelRow
	err := h.db.QueryRow(`SELECT last_discovered_at, total_discovered FROM channel WHERE channel_id = ?`, id).Scan(&r.floor, &r.discovered)
	if errors.Is(err, sql.ErrNoRows) {
		return r
	}
	require.NoError(t, err)
	r.exists = true
	return r
}

func (h *isoHarness) notFoundLedgerRows(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM quota_ledger WHERE run_id = ? AND operation_type = 'playlist_items_list' AND outcome_category = 'permanent' AND http_status = 404`, h.runID).Scan(&n))
	return n
}

func okSpec(c byte, pages ...[]string) srcSpec { return srcSpec{id: chID(c), pages: pages} }
func notFoundSpec(c byte, failAt int, pages ...[]string) srcSpec {
	return srcSpec{id: chID(c), pages: pages, failAt: failAt, failStatus: http.StatusNotFound, failBody: playlistNotFoundBody}
}

func TestUnavailableSourceIsSkippedWhereverItSits(t *testing.T) {
	a := func() srcSpec { return okSpec('A', []string{"vA1", "vA2"}) }
	c := func() srcSpec { return okSpec('C', []string{"vC1"}) }
	b := func() srcSpec { return notFoundSpec('B', 1, []string{"vB1"}) }
	for name, specs := range map[string][]srcSpec{
		"first":  {b(), a(), c()},
		"middle": {a(), b(), c()},
		"last":   {a(), c(), b()},
	} {
		t.Run(name, func(t *testing.T) {
			h := newIsoHarness(t, specs, 1000)
			res, err := h.run()
			require.NoError(t, err)
			require.Equal(t, 3, res.SourcesAttempted)
			require.Equal(t, 2, res.SourcesCompleted)
			require.Equal(t, 1, res.SourcesFailedLocal)
			require.False(t, res.ScanStopped)
			require.Equal(t, 3, res.VideosDiscovered)

			for _, id := range []string{"vA1", "vA2", "vC1"} {
				require.Equal(t, 1, h.videoCount(t, id), id)
			}
			require.Equal(t, 0, h.videoCount(t, "vB1"))
			require.True(t, h.channel(t, chID('A')).floor.Valid)
			require.True(t, h.channel(t, chID('C')).floor.Valid)
			require.Equal(t, channelRow{}, h.channel(t, chID('B')), "B's channel row and floor must be untouched")

			require.Equal(t, 1, h.reqs["UU"+strings.Repeat("B", 22)], "no further playlistItems request for B after the 404")
			require.Equal(t, 1, h.notFoundLedgerRows(t))
		})
	}
}

func TestLaterPageFailureDiscardsTheChannelAndLeavesItsFloor(t *testing.T) {
	// B has pages 1 and 2, then 404 on page 3.
	b := notFoundSpec('B', 3, []string{"vB1"}, []string{"vB2"}, []string{"vB3"})
	a := okSpec('A', []string{"vA1"})
	h := newIsoHarness(t, []srcSpec{a, b}, 1000)
	res, err := h.run()
	require.NoError(t, err)
	require.Equal(t, 1, res.SourcesFailedLocal)
	require.Equal(t, 0, h.videoCount(t, "vB1"))
	require.Equal(t, 0, h.videoCount(t, "vB2"))
	require.Equal(t, channelRow{}, h.channel(t, chID('B')))
	require.Equal(t, 1, h.videoCount(t, "vA1"))

	// A later run where B succeeds discovers its items, including ones older
	// than A's floor, because B's floor never moved.
	b.failAt = 0
	h2 := newIsoHarness(t, []srcSpec{b}, 1000)
	_, err = h2.run()
	require.NoError(t, err)
	for _, id := range []string{"vB1", "vB2", "vB3"} {
		require.Equal(t, 1, h2.videoCount(t, id))
	}
	require.True(t, h2.channel(t, chID('B')).floor.Valid)
}

func TestCrossChannelDuplicateIsPersistedOnce(t *testing.T) {
	dupB := func() srcSpec { return notFoundSpec('B', 2, []string{"vShared", "vB1"}, []string{"vB2"}) }
	t.Run("failed channel first, later channel lists it", func(t *testing.T) {
		h := newIsoHarness(t, []srcSpec{dupB(), okSpec('C', []string{"vShared"})}, 1000)
		_, err := h.run()
		require.NoError(t, err)
		require.Equal(t, 1, h.videoCount(t, "vShared"))
		require.Equal(t, 0, h.videoCount(t, "vB1"))
	})
	t.Run("completed channel first, failed channel after", func(t *testing.T) {
		h := newIsoHarness(t, []srcSpec{okSpec('C', []string{"vShared"}), dupB()}, 1000)
		_, err := h.run()
		require.NoError(t, err)
		require.Equal(t, 1, h.videoCount(t, "vShared"))
	})
	t.Run("twice on one page", func(t *testing.T) {
		h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vDup", "vDup", "vA2"})}, 1000)
		res, err := h.run()
		require.NoError(t, err)
		require.Equal(t, 2, res.VideosDiscovered)
		require.Equal(t, 1, h.videoCount(t, "vDup"))
	})
}

func TestNonLocalErrorsStillFailDiscovery(t *testing.T) {
	cases := map[string]srcSpec{
		"404 notFound":                {failStatus: 404, failBody: `{"error":{"errors":[{"reason":"notFound"}]}}`},
		"403 playlistItemsNotAccess.": {failStatus: 403, failBody: `{"error":{"errors":[{"reason":"playlistItemsNotAccessible"}]}}`},
		"mixed reasons":               {failStatus: 404, failBody: `{"error":{"errors":[{"reason":"playlistNotFound"},{"reason":"quotaExceeded"}]}}`},
		"404 unregistered reason":     {failStatus: 404, failBody: `{"error":{"errors":[{"reason":"somethingElse"}]}}`},
		"404 no body":                 {failStatus: 404, failBody: `{}`},
		"500 backendError":            {failStatus: 500, failBody: `{"error":{"errors":[{"reason":"backendError"}]}}`},
		"401 authError":               {failStatus: 401, failBody: `{"error":{"errors":[{"reason":"authError"}]}}`},
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			bad.id, bad.pages, bad.failAt = chID('B'), [][]string{{"vB1"}}, 1
			h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"}), bad, okSpec('C', []string{"vC1"})}, 1000)
			_, err := h.run()
			require.Error(t, err)
			require.NotErrorIs(t, err, ErrNoSourceScanned)
			require.NotContains(t, h.logs.String(), "uploads source skipped")
			require.Equal(t, 0, h.videoCount(t, "vC1"), "discovery fails before any persistence")
		})
	}
}

func TestIsSourceLocalPredicate(t *testing.T) {
	api := func(status int, complete bool, reasons ...string) *youtube.APIError {
		e := &youtube.APIError{Status: status, Reasons: reasons, Complete: complete}
		if len(reasons) == 1 {
			e.Reason = reasons[0]
		}
		return e
	}
	live := context.Background()
	canceled, cancel := context.WithCancel(live)
	cancel()
	boom := errors.New("boom")
	local := api(404, true, "playlistNotFound")

	require.True(t, isSourceLocal(live, local), "the guarded client returns a bare APIError for a clean 404")
	require.True(t, isSourceLocal(live, fmt.Errorf("list playlist items UUx: %w", local)))
	require.True(t, isSourceLocal(live, api(404, true, "playlistNotFound", "playlistNotFound")))
	require.False(t, isSourceLocal(canceled, local), "canceled run context")
	require.False(t, isSourceLocal(live, api(404, true, "playlistNotFound", "quotaExceeded")))
	require.False(t, isSourceLocal(live, api(404, true, "notFound")))
	require.False(t, isSourceLocal(live, api(403, true, "playlistNotFound")))
	require.False(t, isSourceLocal(live, api(404, false)), "incomplete provider reasons")
	require.False(t, isSourceLocal(live, api(404, true)), "no reason at all")
	require.False(t, isSourceLocal(live, errors.New("404 playlistNotFound")), "never classify by text")
	for name, re := range map[string]*youtube.ReadError{
		"accounting":   {Primary: local, Accounting: boom, Status: 404},
		"renewal":      {Primary: local, Renewal: boom, Status: 404},
		"interruption": {Primary: local, Interruption: boom, Status: 404},
		"bare":         {Primary: local, Status: 404},
	} {
		require.False(t, isSourceLocal(live, fmt.Errorf("wrapped: %w", re)), "any ReadError fails the run: "+name)
	}
}

func TestLegacyUnguardedRunNeverIsolatesASource(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"snippet": map[string]any{"resourceId": map[string]any{"channelId": chID('A')}}}}})
	})
	mux.HandleFunc("/channels", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"id": chID('A'), "snippet": map[string]any{"title": "A"}, "contentDetails": map[string]any{"relatedPlaylists": map[string]any{"uploads": "UUa"}}}}})
	})
	mux.HandleFunc("/playlistItems", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(playlistNotFoundBody))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	eng, st, _, _ := newTestEngine(t, server.URL, 10_000)
	runID, err := st.CreateRun(context.Background(), &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning})
	require.NoError(t, err)
	_, err = eng.Run(context.Background(), runID)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNoSourceScanned)
	var api *youtube.APIError
	require.ErrorAs(t, err, &api, "the unisolated 404 keeps failing discovery as before")
}

func TestAllAttemptedSourcesUnavailableFailsTheRun(t *testing.T) {
	h := newIsoHarness(t, []srcSpec{notFoundSpec('A', 1, []string{"vA1"}), notFoundSpec('B', 1, []string{"vB1"})}, 1000)
	res, err := h.run()
	require.Nil(t, res)
	require.ErrorIs(t, err, ErrNoSourceScanned)
	require.Equal(t, "diag:v1 phase=discovery operation=discover code=no_source_scanned", diag.Reason(err, "discovery phase failed"))
}

func TestMixedRunsAreNotNoSourceScanned(t *testing.T) {
	t.Run("one channel succeeds with zero uploads", func(t *testing.T) {
		h := newIsoHarness(t, []srcSpec{notFoundSpec('A', 1, []string{"vA1"}), okSpec('B', []string{})}, 1000)
		res, err := h.run()
		require.NoError(t, err)
		require.Equal(t, 1, res.SourcesCompleted)
		require.Equal(t, 1, res.SourcesFailedLocal)
	})
	t.Run("no subscriptions", func(t *testing.T) {
		h := newIsoHarness(t, nil, 1000)
		res, err := h.run()
		require.NoError(t, err)
		require.Zero(t, res.SourcesAttempted)
	})
	t.Run("no channel has an uploads playlist", func(t *testing.T) {
		spec := okSpec('A', []string{"vA1"})
		spec.noUploads = true
		h := newIsoHarness(t, []srcSpec{spec}, 1000)
		res, err := h.run()
		require.NoError(t, err)
		require.Zero(t, res.SourcesAttempted)
	})
	t.Run("budget refusal after a skipped source sets ScanStopped", func(t *testing.T) {
		// subscriptions + channels + A's playlist request = 3 units; B has no budget left.
		h := newIsoHarness(t, []srcSpec{notFoundSpec('A', 1, []string{"vA1"}), okSpec('B', []string{"vB1"})}, 3)
		res, err := h.run()
		require.NoError(t, err)
		require.True(t, res.ScanStopped)
		require.Equal(t, 1, res.SourcesFailedLocal)
		require.Zero(t, res.SourcesCompleted)
	})
}

func TestQuotaStopMidChannelKeepsPartialBufferWithoutFloor(t *testing.T) {
	// subscriptions + channels + page 1 = 3 units; page 2 is refused.
	h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"}, []string{"vA2"})}, 3)
	res, err := h.run()
	require.NoError(t, err)
	require.True(t, res.ScanStopped)
	require.Equal(t, 1, res.VideosDiscovered)
	require.False(t, h.channel(t, chID('A')).floor.Valid, "a partially enumerated channel keeps no floor")
}

func TestSkipLogRecordIsSafe(t *testing.T) {
	h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"}), notFoundSpec('B', 2, []string{"vB1"}, []string{"vB2"})}, 1000)
	_, err := h.run()
	require.NoError(t, err)
	out := h.logs.String()
	require.Equal(t, 1, strings.Count(out, "discovery: uploads source skipped"))
	require.Contains(t, out, "diagnostic=\"diag:v1 phase=discovery operation=playlist_items_list code=provider_error status=404 reason=playlistNotFound\"")
	require.Contains(t, out, "pages_discarded=1")
	require.Contains(t, out, "source_channel_id="+chID('B'))
	require.NotContains(t, out, "source_channel_id_valid")
	require.NotContains(t, out, "UU"+strings.Repeat("B", 22), "uploads playlist ID")
	require.NotContains(t, out, "p2", "page token")
	require.NotContains(t, out, "Title "+chID('B'), "channel title")
	require.NotContains(t, out, "http://", "URL")
	require.NotContains(t, out, "list playlist items", "error text")
}

func TestInvalidChannelIDIsNotIsolated(t *testing.T) {
	bad := notFoundSpec('B', 1, []string{"vB1"})
	bad.id = "UCshort"
	h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"}), bad}, 1000)
	_, err := h.run()
	require.Error(t, err, "a skip that cannot be recorded must not happen")
	require.NotErrorIs(t, err, ErrNoSourceScanned)
	require.Empty(t, h.failureRows(t))
	require.NotContains(t, h.logs.String(), "uploads source skipped")
	require.Equal(t, 0, h.videoCount(t, "vA1"))
}

func TestSkipLogCarriesLocalRefWhenChannelRowExists(t *testing.T) {
	b := notFoundSpec('B', 1, []string{"vB1"})
	// Seed an earlier discovery of B (floor in the past) so its row exists.
	h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"}), b}, 1000)
	_, err := h.db.Exec(`INSERT INTO channel (channel_id, channel_name, first_discovered_at, last_discovered_at, total_discovered) VALUES (?, 'B', '2020-01-01T00:00:00Z', '2020-01-01T00:00:00Z', 4)`, chID('B'))
	require.NoError(t, err)
	_, err = h.run()
	require.NoError(t, err)
	require.Contains(t, h.logs.String(), "source_channel_ref=")
	row := h.channel(t, chID('B'))
	require.Equal(t, 4, row.discovered, "B's counters are unchanged")
}

func TestFloorIsWrittenOnlyForTheScannedChannelsOwnVideos(t *testing.T) {
	// A's uploads list a video whose snippet channel is C (a re-upload or
	// collaboration). Scanning A must not advance C's floor, though the video
	// is still discovered.
	a := okSpec('A', []string{"vOwn"})
	foreign := okSpec('F', []string{"vForeign"})
	foreign.snippetChannel = chID('C')
	h := newIsoHarness(t, []srcSpec{a, foreign}, 1000)
	res, err := h.run()
	require.NoError(t, err)
	require.Equal(t, 2, res.VideosDiscovered)
	require.True(t, h.channel(t, chID('A')).floor.Valid, "own videos set the floor on a completed scan")
	c := h.channel(t, chID('C'))
	require.True(t, c.exists)
	require.False(t, c.floor.Valid, "a foreign snippet channel's floor is not advanced by another channel's scan")
}

func TestIncidentRowIsCommittedBeforeTheNextRequestAndSkipLogsAfter(t *testing.T) {
	h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"}), notFoundSpec('B', 1, []string{"vB1"}), okSpec('C', []string{"vC1"})}, 1000)
	_, err := h.run()
	require.NoError(t, err)
	rows := h.failureRows(t)
	require.Len(t, rows, 1)
	r := rows[0]
	require.Equal(t, h.runID, r.RunID)
	require.Equal(t, chID('B'), r.Channel)
	require.Equal(t, "permanent", r.Category)
	require.Equal(t, "playlist_items_list", r.Op)
	require.Equal(t, 404, r.Status)
	require.Equal(t, "playlistNotFound", r.Reason)
	require.NotEmpty(t, r.At)
	require.Equal(t, 1, h.notFoundLedgerRows(t), "the ledger row stays and is separate")
	require.True(t, diag.ProviderReasonAdmitted(r.Reason))
}

func TestFailedIncidentWriteFailsTheRunWithoutALogOrACount(t *testing.T) {
	h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"}), notFoundSpec('B', 1, []string{"vB1"}), okSpec('C', []string{"vC1"})}, 1000)
	_, err := h.db.Exec(`CREATE TRIGGER fail_incident BEFORE INSERT ON discovery_source_failure BEGIN SELECT RAISE(ABORT, 'injected'); END`)
	require.NoError(t, err)
	res, err := h.run()
	require.Error(t, err)
	require.Nil(t, res)
	require.Equal(t, "diag:v1 phase=discovery operation=discover code=sqlite_error sqlite=SQLITE_CONSTRAINT", diag.Reason(err, "discovery phase failed"))
	require.NotContains(t, h.logs.String(), "uploads source skipped")
	require.Empty(t, h.failureRows(t))
	require.Equal(t, 0, h.requests('C'), "no further provider request after the failed write")
}

func TestSameRunResumeDoesNotAskARecordedSourceAgain(t *testing.T) {
	h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"}), notFoundSpec('B', 1, []string{"vB1"})}, 1000)
	_, err := h.run()
	require.NoError(t, err)
	require.Equal(t, 1, h.requests('B'))
	first := h.failureRows(t)
	h.logs.Reset()

	res, err := h.run() // the same run id: a resumed discovery
	require.NoError(t, err)
	require.Equal(t, 1, h.requests('B'), "no uploads request for a recorded source")
	require.Equal(t, 2, res.SourcesAttempted)
	require.Equal(t, 1, res.SourcesFailedLocal)
	require.Equal(t, first, h.failureRows(t), "no second row, and the earlier row stands")
	require.NotContains(t, h.logs.String(), "uploads source skipped", "not logged again")
}

func TestSameRunResumeWithOnlyRecordedSourcesStillEndsNoSourceScanned(t *testing.T) {
	h := newIsoHarness(t, []srcSpec{notFoundSpec('B', 1, []string{"vB1"})}, 1000)
	_, err := h.run()
	require.ErrorIs(t, err, ErrNoSourceScanned)
	_, err = h.run()
	require.ErrorIs(t, err, ErrNoSourceScanned)
	require.Equal(t, 1, h.requests('B'))
	require.Len(t, h.failureRows(t), 1)
}

func TestCrashBeforeTheInsertLeavesTheSourceToBePolledOnResume(t *testing.T) {
	h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"}), notFoundSpec('B', 1, []string{"vB1"})}, 1000)
	_, err := h.db.Exec(`CREATE TRIGGER fail_incident BEFORE INSERT ON discovery_source_failure BEGIN SELECT RAISE(ABORT, 'injected'); END`)
	require.NoError(t, err)
	_, err = h.run()
	require.Error(t, err)
	_, err = h.db.Exec(`DROP TRIGGER fail_incident`)
	require.NoError(t, err)

	_, err = h.run()
	require.NoError(t, err)
	require.Equal(t, 2, h.requests('B'), "a source with no row is polled again")
	require.Len(t, h.failureRows(t), 1)
}

func TestFreshRunPollsAgainAndNeverRewritesHistory(t *testing.T) {
	h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"}), notFoundSpec('B', 1, []string{"vB1"})}, 1000)
	_, err := h.run()
	require.NoError(t, err)
	run1 := h.failureRows(t)
	require.Len(t, run1, 1)

	// Run 2: B still fails. It is polled again and adds its own row.
	h.runID = h.newRun(t)
	_, err = h.run()
	require.NoError(t, err)
	require.Equal(t, 2, h.requests('B'))
	rows := h.failureRows(t)
	require.Len(t, rows, 2)
	require.Equal(t, run1[0], rows[0], "the earlier row is unchanged")
	require.Equal(t, h.runID, rows[1].RunID)

	// Run 3: B recovers. It is scanned, no row is added, and history stays.
	h.setFailAt('B', 0)
	h.runID = h.newRun(t)
	_, err = h.run()
	require.NoError(t, err)
	require.Equal(t, 3, h.requests('B'))
	require.Equal(t, rows, h.failureRows(t))
	require.Equal(t, 1, h.videoCount(t, "vB1"))
}

func TestPlaylistNotFoundIsInDiagsProviderReasonVocabulary(t *testing.T) {
	require.True(t, diag.ProviderReasonAdmitted("playlistNotFound"))
	require.False(t, diag.ProviderReasonAdmitted("not-a-reason"))
}

func (h *isoHarness) scanOutcome(t *testing.T) *string {
	t.Helper()
	var v sql.NullString
	require.NoError(t, h.db.QueryRow(`SELECT discovery_scan_outcome FROM run WHERE id = ?`, h.runID).Scan(&v))
	if !v.Valid {
		return nil
	}
	return &v.String
}

func outcomeIs(t *testing.T, got *string, want string) {
	t.Helper()
	require.NotNil(t, got)
	require.Equal(t, want, *got)
}

// ARS-002 section 4.5 / section 7 test 7: the saved scan outcome.

func TestScanOutcomeCompletedForANormalMixedScan(t *testing.T) {
	h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"}), notFoundSpec('B', 1, []string{"vB1"})}, 1000)
	require.Nil(t, h.scanOutcome(t), "unknown before the scan")
	_, err := h.run()
	require.NoError(t, err)
	outcomeIs(t, h.scanOutcome(t), "completed")
}

func TestScanOutcomeCutShortWhenABudgetStopFollowsLocalFailures(t *testing.T) {
	// subscriptions + channels + A + B = 4 units; C is refused.
	h := newIsoHarness(t, []srcSpec{notFoundSpec('A', 1, []string{"vA1"}), notFoundSpec('B', 1, []string{"vB1"}), okSpec('C', []string{"vC1"})}, 4)
	res, err := h.run()
	require.NoError(t, err)
	require.True(t, res.ScanStopped)
	require.Equal(t, 2, res.SourcesFailedLocal)
	outcomeIs(t, h.scanOutcome(t), "cut_short")
}

func TestScanOutcomeStaysCutShortOnARunWideProviderError(t *testing.T) {
	for name, bad := range map[string]srcSpec{
		"authError":    {failStatus: 401, failBody: `{"error":{"errors":[{"reason":"authError"}]}}`},
		"backendError": {failStatus: 500, failBody: `{"error":{"errors":[{"reason":"backendError"}]}}`},
	} {
		t.Run(name, func(t *testing.T) {
			bad.id, bad.pages, bad.failAt = chID('B'), [][]string{{"vB1"}}, 1
			h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"}), bad}, 1000)
			_, err := h.run()
			require.Error(t, err)
			outcomeIs(t, h.scanOutcome(t), "cut_short")
		})
	}
}

func TestScanOutcomeCanceledBeforeTheScanHasNone(t *testing.T) {
	h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"})}, 1000)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := h.engine.RunBound(ctx, h.runID, h.binding)
	require.Error(t, err)
	require.Nil(t, h.scanOutcome(t), "a run canceled before the scan started has no outcome")
}

func TestScanOutcomeResumeThatFinishesReplacesCutShort(t *testing.T) {
	h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"}), okSpec('B', []string{"vB1"})}, 3)
	res, err := h.run() // stops after A
	require.NoError(t, err)
	require.True(t, res.ScanStopped)
	outcomeIs(t, h.scanOutcome(t), "cut_short")

	require.NoError(t, h.ledger.SetDailyLimit(context.Background(), 1000))
	res, err = h.run()
	require.NoError(t, err)
	require.False(t, res.ScanStopped)
	outcomeIs(t, h.scanOutcome(t), "completed")
}

func TestScanOutcomeSurvivesALaterPrefetchFailure(t *testing.T) {
	h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"})}, 1000)
	h.videosFail = true
	_, err := h.run()
	require.Error(t, err)
	outcomeIs(t, h.scanOutcome(t), "completed")
}

func TestScanOutcomeAllLocalFailuresLeavesCompletedForThePipelineToReplace(t *testing.T) {
	h := newIsoHarness(t, []srcSpec{notFoundSpec('A', 1, []string{"vA1"})}, 1000)
	_, err := h.run()
	require.ErrorIs(t, err, ErrNoSourceScanned)
	outcomeIs(t, h.scanOutcome(t), "completed")
}

func TestLegacyPathIsolatesNothingEvenWithByIDForced(t *testing.T) {
	h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"}), notFoundSpec('B', 1, []string{"vB1"})}, 1000)
	client := youtube.NewClient(context.Background(), &oauth2.Token{AccessToken: "t"}, youtube.WithBaseURL(h.baseURL))
	_, err := h.engine.runWithClient(context.Background(), h.runID, client, true, false)
	require.Error(t, err, "byID must not enable isolation")
	require.NotErrorIs(t, err, ErrNoSourceScanned)
	require.Empty(t, h.failureRows(t))
	require.NotContains(t, h.logs.String(), "uploads source skipped")
}
