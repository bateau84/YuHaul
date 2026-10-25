package discovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/bateau84/YuHaul/internal/youtube"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newTestEngine(t *testing.T, baseURL string, limit int) (*Engine, *state.Store, *quota.Ledger, *sql.DB) {
	t.Helper()
	db := openTestDB(t)

	st, err := state.NewStore(db)
	require.NoError(t, err)

	ledger := quota.New(db)
	if limit >= 0 {
		require.NoError(t, ledger.SetDailyLimit(context.Background(), limit))
	}

	mach := state.NewMachine(st)
	auth := &stubTokenProvider{token: &oauth2.Token{AccessToken: "test-token"}}
	factory := func(ctx context.Context, token *oauth2.Token, recorder youtube.CostRecorder) *youtube.Client {
		return youtube.NewClient(ctx, token,
			youtube.WithBaseURL(baseURL),
			youtube.WithRecorder(recorder),
		)
	}
	eng := NewEngine(auth, mach, ledger, db, WithClientFactory(factory))
	return eng, st, ledger, db
}

type stubTokenProvider struct {
	token *oauth2.Token
	err   error
}

func (s *stubTokenProvider) ActiveToken(ctx context.Context) (*oauth2.Token, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.token, nil
}

// playlistItemJSON builds a playlistItems.list snippet item.
func playlistItemJSON(videoID, title, channelID string, publishedAt time.Time) map[string]any {
	return map[string]any{
		"snippet": map[string]any{
			"title":       title,
			"channelId":   channelID,
			"publishedAt": publishedAt.Format(time.RFC3339Nano),
			"resourceId":  map[string]any{"videoId": videoID},
			"thumbnails":  map[string]any{"default": map[string]any{"url": "https://i.ytimg.com/default.jpg"}},
		},
	}
}

// videoJSON builds a videos.list item.
func videoJSON(videoID, title, channelID, channelTitle, duration string, publishedAt time.Time) map[string]any {
	return map[string]any{
		"id": videoID,
		"snippet": map[string]any{
			"title":        title,
			"channelId":    channelID,
			"channelTitle": channelTitle,
			"description":  "description for " + title,
			"publishedAt":  publishedAt.Format(time.RFC3339Nano),
			"thumbnails":   map[string]any{"high": map[string]any{"url": "https://i.ytimg.com/high.jpg"}},
		},
		"contentDetails": map[string]any{"duration": duration},
	}
}

func TestDiscoveryEndToEnd(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	mux := http.NewServeMux()
	mux.HandleFunc("/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"snippet": map[string]any{"resourceId": map[string]any{"channelId": "UCchannel1"}}},
			},
		})
	})
	mux.HandleFunc("/channels", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{
					"id":      "UCchannel1",
					"snippet": map[string]any{"title": "Channel One"},
					"contentDetails": map[string]any{
						"relatedPlaylists": map[string]any{"uploads": "UUchannel1"},
					},
				},
			},
		})
	})
	mux.HandleFunc("/playlistItems", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				playlistItemJSON("vid1", "Artist - Track (Official Video)", "UCchannel1", now.Add(-time.Hour)),
			},
		})
	})
	mux.HandleFunc("/videos", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				videoJSON("vid1", "Artist - Track (Official Video)", "UCchannel1", "Channel One", "PT3M30S", now.Add(-time.Hour)),
			},
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	eng, st, ledger, db := newTestEngine(t, server.URL, 10_000)
	ctx := context.Background()

	runID, err := st.CreateRun(ctx, &state.Run{
		StartedAt: now,
		RunType:   state.RunTypeManual,
		Status:    state.RunStatusRunning,
	})
	require.NoError(t, err)

	result, err := eng.Run(ctx, runID)
	require.NoError(t, err)
	require.Equal(t, 1, result.SubscriptionsFetched)
	require.Equal(t, 1, result.ChannelsFetched)
	require.Equal(t, 1, result.VideosDiscovered)

	// Video persisted as not_yet_evaluated.
	vid, err := st.GetVideoByExternalID(ctx, "vid1")
	require.NoError(t, err)
	require.Equal(t, state.VideoStateNotYetEvaluated, vid.ProcessingState)
	require.Equal(t, state.FinalOutcomeNotYetEvaluated, vid.FinalOutcome)

	// Fingerprint persisted.
	var fp string
	err = db.QueryRowContext(ctx, `SELECT fingerprint FROM title_fingerprint WHERE video_id = ?`, "vid1").Scan(&fp)
	require.NoError(t, err)
	require.Equal(t, "artist track", fp)

	// Transient metadata persisted with refreshed details.
	var title string
	var duration int
	err = db.QueryRowContext(ctx, `SELECT title, duration_seconds FROM transient_metadata WHERE video_id = ?`, "vid1").Scan(&title, &duration)
	require.NoError(t, err)
	require.Equal(t, "Artist - Track (Official Video)", title)
	require.Equal(t, 210, duration)

	// Channel watermark and discovered counter updated.
	var lastDiscovered time.Time
	var totalDiscovered int
	err = db.QueryRowContext(ctx, `SELECT last_discovered_at, total_discovered FROM channel WHERE channel_id = ?`, "UCchannel1").Scan(&lastDiscovered, &totalDiscovered)
	require.NoError(t, err)
	require.False(t, lastDiscovered.IsZero())
	require.Equal(t, 1, totalDiscovered, "channel total_discovered should reflect the new video")

	// Quota ledger recorded every operation type.
	consumed, err := ledger.ConsumedToday(ctx)
	require.NoError(t, err)
	require.Equal(t, 4, consumed, "subscriptions(1) + channels(1) + playlistItems(1) + videos(1)")
}

func TestDiscoveryWatermarkSkipsOldVideos(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"snippet": map[string]any{"resourceId": map[string]any{"channelId": "UCchannel1"}}},
			},
		})
	})
	mux.HandleFunc("/channels", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{
					"id":             "UCchannel1",
					"snippet":        map[string]any{"title": "Channel One"},
					"contentDetails": map[string]any{"relatedPlaylists": map[string]any{"uploads": "UUchannel1"}},
				},
			},
		})
	})
	mux.HandleFunc("/playlistItems", func(w http.ResponseWriter, r *http.Request) {
		calls++
		// First run: one old and one new video. Second run: only old video.
		items := []map[string]any{
			playlistItemJSON("vid-old", "Old Song", "UCchannel1", now.Add(-48*time.Hour)),
		}
		if calls == 1 {
			items = append(items, playlistItemJSON("vid-new", "New Song", "UCchannel1", now.Add(-time.Hour)))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	})
	mux.HandleFunc("/videos", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				videoJSON("vid-new", "New Song", "UCchannel1", "Channel One", "PT4M", now.Add(-time.Hour)),
			},
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	eng, st, _, db := newTestEngine(t, server.URL, 10_000)
	ctx := context.Background()

	// Pre-seed the channel with a watermark so the older video is skipped.
	watermark := now.Add(-24 * time.Hour)
	_, err := db.ExecContext(ctx,
		`INSERT INTO channel (channel_id, channel_name, first_discovered_at, last_discovered_at, total_discovered)
		 VALUES (?, ?, ?, ?, ?);`,
		"UCchannel1", "Channel One", watermark, watermark, 0)
	require.NoError(t, err)

	runID, err := st.CreateRun(ctx, &state.Run{StartedAt: now, RunType: state.RunTypeManual, Status: state.RunStatusRunning})
	require.NoError(t, err)

	result1, err := eng.Run(ctx, runID)
	require.NoError(t, err)
	require.Equal(t, 1, result1.VideosDiscovered, "only vid-new should be discovered after the watermark")

	// Second run: no new uploads, so nothing is discovered.
	runID2, err := st.CreateRun(ctx, &state.Run{StartedAt: now.Add(time.Hour), RunType: state.RunTypeManual, Status: state.RunStatusRunning})
	require.NoError(t, err)

	result2, err := eng.Run(ctx, runID2)
	require.NoError(t, err)
	require.Equal(t, 0, result2.VideosDiscovered, "no new videos after watermark")
}

func TestIncompleteUploadsPaginationCanResumeAfterQuotaRecovery(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	pageCalls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"snippet": map[string]any{"resourceId": map[string]any{"channelId": "UCpage"}}}}})
	})
	mux.HandleFunc("/channels", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"id": "UCpage", "snippet": map[string]any{"title": "Page channel"}, "contentDetails": map[string]any{"relatedPlaylists": map[string]any{"uploads": "UUpage"}}}}})
	})
	mux.HandleFunc("/playlistItems", func(w http.ResponseWriter, r *http.Request) {
		pageCalls++
		if r.URL.Query().Get("pageToken") == "older" {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{playlistItemJSON("older-video", "Older", "UCpage", now.Add(-2*time.Hour))}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{playlistItemJSON("first-page-video", "First", "UCpage", now.Add(-time.Hour))}, "nextPageToken": "older"})
	})
	mux.HandleFunc("/videos", func(w http.ResponseWriter, r *http.Request) {
		ids := r.URL.Query().Get("id")
		var items []map[string]any
		for _, id := range strings.Split(ids, ",") {
			items = append(items, videoJSON(id, id, "UCpage", "Page channel", "PT1M", now.Add(-time.Hour)))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	db := openTestDB(t)
	st, err := state.NewStore(db)
	require.NoError(t, err)
	ledger := quota.New(db)
	require.NoError(t, ledger.SetDailyLimit(context.Background(), 3)) // subscriptions, channels, first uploads page
	eng := newEngineForDB(t, db, server.URL, ledger)
	ctx := context.Background()
	runID, err := st.CreateRun(ctx, &state.Run{StartedAt: now, RunType: state.RunTypeManual, Status: state.RunStatusRunning})
	require.NoError(t, err)
	result, err := eng.Run(ctx, runID)
	require.NoError(t, err)
	require.Equal(t, 1, result.VideosDiscovered)
	var watermark sql.NullTime
	require.NoError(t, db.QueryRowContext(ctx, `SELECT last_discovered_at FROM channel WHERE channel_id = ?`, "UCpage").Scan(&watermark))
	require.False(t, watermark.Valid, "partial pagination must not advance the watermark")
	require.NoError(t, ledger.SetDailyLimit(ctx, 100))

	// Reconstruct the engine to prove recovery uses durable state, not in-memory page progress.
	eng = newEngineForDB(t, db, server.URL, ledger)
	runID, err = st.CreateRun(ctx, &state.Run{StartedAt: now.Add(time.Minute), RunType: state.RunTypeManual, Status: state.RunStatusRunning})
	require.NoError(t, err)
	result, err = eng.Run(ctx, runID)
	require.NoError(t, err)
	require.Equal(t, 1, result.VideosDiscovered)
	_, err = st.GetVideoByExternalID(ctx, "older-video")
	require.NoError(t, err)
	_, err = st.GetVideoByExternalID(ctx, "first-page-video")
	require.NoError(t, err)
	require.Equal(t, 3, pageCalls, "first run fetches one page; resumed run fetches both pages")
}

func newEngineForDB(t *testing.T, db *sql.DB, baseURL string, ledger *quota.Ledger) *Engine {
	t.Helper()
	st, err := state.NewStore(db)
	require.NoError(t, err)
	factory := func(ctx context.Context, token *oauth2.Token, recorder youtube.CostRecorder) *youtube.Client {
		return youtube.NewClient(ctx, token, youtube.WithBaseURL(baseURL), youtube.WithRecorder(recorder))
	}
	return NewEngine(&stubTokenProvider{token: &oauth2.Token{AccessToken: "test-token"}}, state.NewMachine(st), ledger, db, WithClientFactory(factory))
}

func TestDiscoveryWithoutAuthFails(t *testing.T) {
	eng, _, _, _ := newTestEngine(t, "http://example.com", 10_000)
	eng.auth = &stubTokenProvider{err: fmt.Errorf("no token")}

	_, err := eng.Run(context.Background(), 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no active token")
}

func TestWithBaseURLUsesOverrideURL(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	var requestPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{},
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	db := openTestDB(t)
	st, err := state.NewStore(db)
	require.NoError(t, err)
	ledger := quota.New(db)
	mach := state.NewMachine(st)
	auth := &stubTokenProvider{token: &oauth2.Token{AccessToken: "test-token"}}
	eng := NewEngine(auth, mach, ledger, db, WithBaseURL(server.URL))

	runID, err := st.CreateRun(context.Background(), &state.Run{
		StartedAt: now,
		RunType:   state.RunTypeManual,
		Status:    state.RunStatusRunning,
	})
	require.NoError(t, err)

	_, err = eng.Run(context.Background(), runID)
	require.NoError(t, err)
	require.Equal(t, "/subscriptions", requestPath)
}

func TestDiscoveryRespectsQuotaBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no API calls should be made when quota is exhausted")
	}))
	defer server.Close()

	eng, st, ledger, _ := newTestEngine(t, server.URL, 1)
	ctx := context.Background()

	runID, err := st.CreateRun(ctx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning})
	require.NoError(t, err)

	// Consume the only available unit so the pre-flight check fails.
	require.NoError(t, ledger.Record(ctx, sql.NullInt64{Int64: runID, Valid: true}, quota.OpSubscriptionsList))

	_, err = eng.Run(ctx, runID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "insufficient quota")
}
