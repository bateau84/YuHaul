package discovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/youtube"

	"github.com/stretchr/testify/require"
)

type presenceRow struct {
	title, channel, description, thumb sql.NullString
	duration                           sql.NullInt64
}

func readPresenceRow(t *testing.T, db *sql.DB, id string) presenceRow {
	t.Helper()
	var r presenceRow
	require.NoError(t, db.QueryRow(`SELECT title, channel_name, description, duration_seconds, thumbnail_url FROM transient_metadata WHERE video_id=?`, id).
		Scan(&r.title, &r.channel, &r.description, &r.duration, &r.thumb))
	return r
}

// boundHarness runs the production RunBound path against a fake YouTube API.
type boundHarness struct {
	db       *sql.DB
	engine   *Engine
	binding  auth.AuthorizationBinding
	ledger   *quota.Ledger
	runID    int64
	mu       sync.Mutex
	videoIDs []string // the id= query of each videos.list call
}

func newBoundHarness(t *testing.T, playlistIDs []string, videos func(w http.ResponseWriter, ids []string), limit int) *boundHarness {
	t.Helper()
	ctx := context.Background()
	h := &boundHarness{db: openTestDB(t)}
	key := []byte("01234567890123456789012345678901")
	ts, err := auth.NewSQLiteTokenStore(h.db, key)
	require.NoError(t, err)
	service, err := auth.NewService(&auth.ServiceConfig{ClientID: "client", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(key)}, ts, auth.NewInMemoryStateStore())
	require.NoError(t, err)
	require.NoError(t, ts.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "tok", RefreshToken: "refresh", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour), Scopes: auth.DefaultScopes()}))
	h.binding, err = service.Bind(ctx)
	require.NoError(t, err)

	now := time.Now().UTC()
	mux := http.NewServeMux()
	mux.HandleFunc("/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"snippet": map[string]any{"resourceId": map[string]any{"channelId": "UCchannel1"}}}}})
	})
	mux.HandleFunc("/channels", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"id": "UCchannel1", "snippet": map[string]any{"title": "Channel One"}, "contentDetails": map[string]any{"relatedPlaylists": map[string]any{"uploads": "UUchannel1"}}}}})
	})
	mux.HandleFunc("/playlistItems", func(w http.ResponseWriter, r *http.Request) {
		var items []map[string]any
		for i, id := range playlistIDs {
			items = append(items, playlistItemJSON(id, "Playlist title "+id, "UCchannel1", now.Add(-time.Duration(i+1)*time.Hour)))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	})
	mux.HandleFunc("/videos", func(w http.ResponseWriter, r *http.Request) {
		ids := strings.Split(r.URL.Query().Get("id"), ",")
		h.mu.Lock()
		h.videoIDs = append(h.videoIDs, r.URL.Query().Get("id"))
		h.mu.Unlock()
		videos(w, ids)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	st, err := state.NewStore(h.db)
	require.NoError(t, err)
	h.runID, err = st.CreateRun(ctx, &state.Run{StartedAt: now, RunType: state.RunTypeManual, Status: state.RunStatusRunning})
	require.NoError(t, err)
	h.ledger = quota.New(h.db)
	require.NoError(t, h.ledger.SetDailyLimit(ctx, limit))
	h.engine = NewEngine(service, state.NewMachine(st), h.ledger, h.db, WithBaseURL(server.URL))
	return h
}

func (h *boundHarness) run(t *testing.T) *Result {
	t.Helper()
	res, err := h.engine.RunBound(context.Background(), h.runID, h.binding)
	require.NoError(t, err)
	return res
}

func jsonItems(items ...map[string]any) func(http.ResponseWriter, []string) {
	return func(w http.ResponseWriter, _ []string) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	}
}

func detailItem(id string, snippet, content map[string]any) map[string]any {
	s := map[string]any{"title": "Details title " + id, "channelTitle": "Details channel", "publishedAt": "2026-01-01T00:00:00Z"}
	for k, v := range snippet {
		if v == nil {
			delete(s, k)
		} else {
			s[k] = v
		}
	}
	return map[string]any{"id": id, "snippet": s, "contentDetails": content}
}

func TestRunBoundKeepsRealZeroDurationAndEmptyDescription(t *testing.T) {
	h := newBoundHarness(t, []string{"live1", "bad1"}, jsonItems(
		// A live or upcoming video has duration P0D and often an empty description.
		detailItem("live1", map[string]any{"description": ""}, map[string]any{"duration": "P0D"}),
		// Missing description and an invalid duration are not validly supplied.
		detailItem("bad1", map[string]any{"description": nil}, map[string]any{"duration": "garbage"}),
	), 100)
	h.run(t)

	live := readPresenceRow(t, h.db, "live1")
	require.True(t, live.description.Valid, "a real empty description must stay ''")
	require.Equal(t, "", live.description.String)
	require.True(t, live.duration.Valid, "P0D is a real zero duration, not NULL")
	require.EqualValues(t, 0, live.duration.Int64)
	require.Equal(t, "Details title live1", live.title.String)

	bad := readPresenceRow(t, h.db, "bad1")
	require.False(t, bad.description.Valid)
	require.False(t, bad.duration.Valid)
	require.Equal(t, "Details title bad1", bad.title.String)
}

// A by-ID channelTitle that is present but "" is a real value and must reach
// transient_metadata as an empty string. An absent one falls back to the channels.list name.
func TestRunBoundKeepsExplicitlyEmptyChannelNameAndFallsBackWhenAbsent(t *testing.T) {
	h := newBoundHarness(t, []string{"empty1", "absent1"}, jsonItems(
		detailItem("empty1", map[string]any{"channelTitle": ""}, map[string]any{"duration": "PT1M"}),
		detailItem("absent1", map[string]any{"channelTitle": nil}, map[string]any{"duration": "PT1M"}),
	), 100)
	h.run(t)

	empty := readPresenceRow(t, h.db, "empty1")
	require.True(t, empty.channel.Valid, "a supplied empty channel name must stay '' and not become NULL")
	require.Equal(t, "", empty.channel.String)

	absent := readPresenceRow(t, h.db, "absent1")
	require.Equal(t, "Channel One", absent.channel.String, "an absent by-ID name keeps the channels.list fallback")
}

// persist keeps presence for the channel name the way it does for description
// and duration: a supplied value (even "") is stored, anything else is NULL.
func TestPersistChannelNamePresence(t *testing.T) {
	ctx := context.Background()
	runStart := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	eng, st, _, db := newTestEngine(t, "http://unused.invalid", 10_000)
	runID, err := st.CreateRun(ctx, &state.Run{StartedAt: runStart, RunType: state.RunTypeManual, Status: state.RunStatusRunning})
	require.NoError(t, err)

	video := func(id, listingName string, d *youtube.VideoDetail) *discoveredVideo {
		return &discoveredVideo{VideoID: id, ChannelID: "UC1", ChannelName: listingName, Title: "t",
			PublishedAt: runStart.Add(-time.Hour), Metadata: d}
	}
	supplied := func(name string) *youtube.VideoDetail {
		d := &youtube.VideoDetail{ChannelTitle: name, PublishedAt: runStart.Add(-time.Hour)}
		d.Present.ChannelTitle = true
		return d
	}
	notSupplied := &youtube.VideoDetail{PublishedAt: runStart.Add(-time.Hour)} // ChannelTitle "" but Present false

	require.NoError(t, eng.persist(ctx, runID, runStart, []*discoveredVideo{
		video("suppliedEmpty", "Listing", supplied("")),
		video("suppliedName", "Listing", supplied("Real name")),
		video("notSupplied", "Listing", notSupplied),
		video("noDetailBlankListing", "", nil),
		video("noDetailListing", "Listing", nil),
	}))

	cases := []struct {
		id    string
		valid bool
		value string
	}{
		{"suppliedEmpty", true, ""},
		{"suppliedName", true, "Real name"},
		{"notSupplied", true, "Listing"},
		{"noDetailBlankListing", false, ""},
		{"noDetailListing", true, "Listing"},
	}
	for _, c := range cases {
		r := readPresenceRow(t, db, c.id)
		require.Equal(t, c.valid, r.channel.Valid, c.id)
		require.Equal(t, c.value, r.channel.String, c.id)
	}
}

func TestRunBoundQuotaRefusedPrefetchStoresNullAndKeepsPlaylistTitle(t *testing.T) {
	// subscriptions + channels + playlistItems use the whole budget.
	h := newBoundHarness(t, []string{"vid1"}, jsonItems(), 3)
	h.run(t)
	require.Empty(t, h.videoIDs, "no videos.list call may be sent once the budget is gone")
	r := readPresenceRow(t, h.db, "vid1")
	require.Equal(t, "Playlist title vid1", r.title.String)
	require.Equal(t, "Channel One", r.channel.String)
	require.False(t, r.description.Valid)
	require.False(t, r.duration.Valid)
}

func TestRunBoundDeduplicatesRequestedIDs(t *testing.T) {
	h := newBoundHarness(t, []string{"vid1", "vid1", "vid2"}, jsonItems(
		detailItem("vid1", map[string]any{"description": "d1"}, map[string]any{"duration": "PT1M"}),
		detailItem("vid2", map[string]any{"description": "d2"}, map[string]any{"duration": "PT2M"}),
	), 100)
	h.run(t)
	require.Equal(t, []string{"vid1,vid2"}, h.videoIDs)
	require.EqualValues(t, 60, readPresenceRow(t, h.db, "vid1").duration.Int64)
	require.EqualValues(t, 120, readPresenceRow(t, h.db, "vid2").duration.Int64)
}

func TestRunBoundInvalidMetadataResponseDoesNotFailDiscovery(t *testing.T) {
	for name, body := range map[string]string{"missing items": `{}`, "truncated": `{"items":[`, "token": `{"items":[],"nextPageToken":"x"}`} {
		t.Run(name, func(t *testing.T) {
			h := newBoundHarness(t, []string{"vid1"}, func(w http.ResponseWriter, _ []string) { _, _ = w.Write([]byte(body)) }, 100)
			res := h.run(t)
			require.Equal(t, 1, res.VideosDiscovered)
			r := readPresenceRow(t, h.db, "vid1")
			require.Equal(t, "Playlist title vid1", r.title.String)
			require.False(t, r.description.Valid)
			require.False(t, r.duration.Valid)
		})
	}
}

// The legacy unguarded Run has no production caller; it cannot read presence,
// so it stores only a non-empty description or non-zero duration.
func TestLegacyRunStoresUnprovenFieldsAsNull(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	mux := http.NewServeMux()
	mux.HandleFunc("/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"snippet": map[string]any{"resourceId": map[string]any{"channelId": "UCchannel1"}}}}})
	})
	mux.HandleFunc("/channels", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"id": "UCchannel1", "snippet": map[string]any{"title": "Channel One"}, "contentDetails": map[string]any{"relatedPlaylists": map[string]any{"uploads": "UUchannel1"}}}}})
	})
	mux.HandleFunc("/playlistItems", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{playlistItemJSON("vid1", "T", "UCchannel1", now.Add(-time.Hour))}})
	})
	mux.HandleFunc("/videos", func(w http.ResponseWriter, r *http.Request) {
		v := videoJSON("vid1", "T", "UCchannel1", "Channel One", "P0D", now.Add(-time.Hour))
		v["snippet"].(map[string]any)["description"] = ""
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{v}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	eng, st, _, db := newTestEngine(t, server.URL, 10_000)
	runID, err := st.CreateRun(context.Background(), &state.Run{StartedAt: now, RunType: state.RunTypeManual, Status: state.RunStatusRunning})
	require.NoError(t, err)
	_, err = eng.Run(context.Background(), runID)
	require.NoError(t, err)
	r := readPresenceRow(t, db, "vid1")
	require.False(t, r.description.Valid)
	require.False(t, r.duration.Valid)
	require.Equal(t, "T", r.title.String)
}
