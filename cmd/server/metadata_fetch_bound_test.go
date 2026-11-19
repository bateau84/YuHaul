package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/youtube"
)

func boundMetadataManager(t *testing.T, status int, body string, limit int) (*metadata.Manager, *metadata.Store, auth.AuthorizationBinding, *atomic.Int32, *quota.Ledger) {
	t.Helper()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)
	p, db := newTestPlaylistProvider(t, upstream.URL)
	p.ledger = quota.New(db, quota.WithDefaultLimit(limit))
	binding, err := p.auth.Bind(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &metadataFetcher{auth: p.auth, baseURL: upstream.URL, ledger: p.ledger}
	ms := metadata.NewStore(db)
	return metadata.NewManager(ms, fetcher, p.ledger, metadata.WithBoundFetcher(fetcher)), ms, binding, &calls, p.ledger
}

func evalInputs(ids ...string) []metadata.InputRequirement {
	out := make([]metadata.InputRequirement, len(ids))
	for i, id := range ids {
		out[i] = metadata.InputRequirement{VideoID: id, Title: true, ChannelName: true, Description: true, Duration: true}
	}
	return out
}

func TestFetchForEvaluationRealBoundRead(t *testing.T) {
	// good: every input; empty: valid empty description and zero-length-free duration;
	// baddur: invalid duration; nopub: returned but unparseable publishedAt; gone: omitted.
	body := `{"items":[
	 {"id":"good","snippet":{"title":"T","channelTitle":"C","description":"","publishedAt":"2026-01-01T00:00:00Z"},"contentDetails":{"duration":"PT1M"}},
	 {"id":"baddur","snippet":{"title":"T","channelTitle":"C","description":"d","publishedAt":"2026-01-01T00:00:00Z"},"contentDetails":{"duration":"garbage"}},
	 {"id":"nopub","snippet":{"title":"T","publishedAt":"nope"}}]}`
	m, ms, binding, calls, ledger := boundMetadataManager(t, 200, body, 10)
	out, err := m.FetchForEvaluation(context.Background(), binding, evalInputs("good", "baddur", "nopub", "gone"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]metadata.FetchStatus{"good": metadata.FetchFetched, "baddur": metadata.FetchUnusable, "nopub": metadata.FetchUnusable, "gone": metadata.FetchAbsent}
	for id, status := range want {
		if out.Statuses[id] != status {
			t.Errorf("%s = %s, want %s", id, out.Statuses[id], status)
		}
	}
	if calls.Load() != 1 || out.Read != nil {
		t.Fatalf("calls=%d read=%v", calls.Load(), out.Read)
	}
	var desc, dur, title any
	if err := ms.DB().QueryRow(`SELECT title, description, duration_seconds FROM transient_metadata WHERE video_id='good'`).Scan(&title, &desc, &dur); err != nil {
		t.Fatal(err)
	}
	if title != "T" || desc != "" || dur != int64(60) {
		t.Fatalf("good row = %v %v %v", title, desc, dur)
	}
	if err := ms.DB().QueryRow(`SELECT description, duration_seconds FROM transient_metadata WHERE video_id='baddur'`).Scan(&desc, &dur); err != nil {
		t.Fatal(err)
	}
	if desc != "d" || dur != nil {
		t.Fatalf("invalid duration must be NULL, got %v", dur)
	}
	var rows int
	_ = ms.DB().QueryRow(`SELECT count(*) FROM transient_metadata`).Scan(&rows)
	var ledgerRows int
	_ = ms.DB().QueryRow(`SELECT count(*) FROM quota_ledger WHERE operation_type='videos_list'`).Scan(&ledgerRows)
	if rows != 2 || ledgerRows != 1 {
		t.Fatalf("rows=%d ledger=%d (one accounted attempt via the shared ledger)", rows, ledgerRows)
	}
	_ = ledger
}

// The real bound read carries the live classification through to the outcome
// and reads again even when Store A already holds the row.
func TestFetchForEvaluationRealBoundReadLiveStatus(t *testing.T) {
	body := `{"items":[
	 {"id":"now","snippet":{"title":"T","channelTitle":"C","description":"","publishedAt":"2026-01-01T00:00:00Z","liveBroadcastContent":"live"},"contentDetails":{"duration":"P0D"}},
	 {"id":"soon","snippet":{"title":"T","channelTitle":"C","description":"","publishedAt":"2026-01-01T00:00:00Z","liveBroadcastContent":"upcoming"},"contentDetails":{"duration":"P0D"}},
	 {"id":"vod","snippet":{"title":"T","channelTitle":"C","description":"","publishedAt":"2026-01-01T00:00:00Z","liveBroadcastContent":"none"},"contentDetails":{"duration":"PT1M"}}]}`
	m, _, binding, calls, _ := boundMetadataManager(t, 200, body, 10)
	reqs := []metadata.InputRequirement{{VideoID: "now", Live: true}, {VideoID: "soon", Live: true}, {VideoID: "vod", Live: true}}
	for round := 1; round <= 2; round++ {
		out, err := m.FetchForEvaluation(context.Background(), binding, reqs)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]metadata.LiveStatus{"now": "live", "soon": "upcoming", "vod": "none"}
		for id, live := range want {
			if out.Live[id] != live {
				t.Errorf("round %d: Live[%s] = %q, want %q", round, id, out.Live[id], live)
			}
		}
		if int(calls.Load()) != round {
			t.Fatalf("round %d: calls=%d; a stored row must not satisfy Live", round, calls.Load())
		}
	}
}

func TestFetchForEvaluationRealBoundReadEmptyItemsProvesAbsence(t *testing.T) {
	m, _, binding, _, _ := boundMetadataManager(t, 200, `{"items":[]}`, 10)
	out, err := m.FetchForEvaluation(context.Background(), binding, evalInputs("gone"))
	if err != nil || out.Statuses["gone"] != metadata.FetchAbsent {
		t.Fatalf("status=%v err=%v", out.Statuses, err)
	}
}

func TestFetchForEvaluationRealBoundReadInvalidEnvelopeIsNotAbsence(t *testing.T) {
	for name, body := range map[string]string{"missing items": `{}`, "null items": `{"items":null}`, "token": `{"items":[],"nextPageToken":"x"}`, "truncated": `{"items":[`} {
		t.Run(name, func(t *testing.T) {
			m, ms, binding, calls, _ := boundMetadataManager(t, 200, body, 10)
			out, err := m.FetchForEvaluation(context.Background(), binding, evalInputs("a"))
			if err != nil || out.Statuses["a"] != metadata.FetchUnusable || out.Read != nil {
				t.Fatalf("status=%v read=%v err=%v", out.Statuses, out.Read, err)
			}
			var rows int
			_ = ms.DB().QueryRow(`SELECT count(*) FROM transient_metadata`).Scan(&rows)
			if rows != 0 || calls.Load() != 1 {
				t.Fatalf("rows=%d calls=%d", rows, calls.Load())
			}
		})
	}
}

func TestFetchForEvaluationRealBoundReadKnownRefusalMakesNoCall(t *testing.T) {
	m, ms, binding, calls, ledger := boundMetadataManager(t, 200, `{"items":[]}`, 1)
	hold, err := ledger.Acquire(context.Background(), quota.OpVideosList)
	if err != nil || hold == nil {
		t.Fatalf("hold=%v err=%v", hold, err)
	}
	out, err := m.FetchForEvaluation(context.Background(), binding, evalInputs("a"))
	if err != nil || out.Statuses["a"] != metadata.FetchQuotaRefused || out.Attempts != 0 || calls.Load() != 0 {
		t.Fatalf("status=%v attempts=%d calls=%d err=%v", out.Statuses, out.Attempts, calls.Load(), err)
	}
	var rows int
	_ = ms.DB().QueryRow(`SELECT count(*) FROM transient_metadata`).Scan(&rows)
	if rows != 0 {
		t.Fatalf("rows=%d", rows)
	}
}

func TestFetchForEvaluationRealBoundReadProviderFailuresKeepTypedFacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   metadata.FetchStatus
	}{
		{"quota", 403, `{"error":{"errors":[{"reason":"quotaExceeded"}]}}`, metadata.FetchQuotaRefused},
		{"unauthorized", 401, `{}`, metadata.FetchAuthorizationPending},
		{"rate", 429, `{}`, metadata.FetchRateLimited},
		{"server", 503, `{}`, metadata.FetchFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, ms, binding, calls, _ := boundMetadataManager(t, tc.status, tc.body, 10)
			var ids []string
			for i := 0; i < 51; i++ {
				ids = append(ids, fmt.Sprintf("v%02d", i))
			}
			out, err := m.FetchForEvaluation(context.Background(), binding, evalInputs(ids...))
			if err != nil || out.Read == nil {
				t.Fatalf("err=%v read=%v", err, out.Read)
			}
			if calls.Load() != 1 || out.Attempts != 1 {
				t.Fatalf("calls=%d attempts=%d, want one", calls.Load(), out.Attempts)
			}
			for _, id := range ids {
				if out.Statuses[id] != tc.want {
					t.Fatalf("%s = %s, want %s", id, out.Statuses[id], tc.want)
				}
			}
			var apiErr *youtube.APIError
			if out.Read.HTTPStatus != tc.status {
				t.Fatalf("status fact = %d", out.Read.HTTPStatus)
			}
			_ = apiErr
			var rows int
			_ = ms.DB().QueryRow(`SELECT count(*) FROM transient_metadata`).Scan(&rows)
			if rows != 0 {
				t.Fatalf("rows=%d", rows)
			}
		})
	}
}

func TestFetchForEvaluationRealBoundReadAccountingFaultIsPreserved(t *testing.T) {
	m, ms, binding, calls, _ := boundMetadataManager(t, 200, `{"items":[{"id":"a","snippet":{"title":"T","publishedAt":"2026-01-01T00:00:00Z"}}]}`, 10)
	if _, err := ms.DB().Exec(`CREATE TRIGGER fail_quota BEFORE INSERT ON quota_ledger BEGIN SELECT RAISE(FAIL, 'record failed'); END`); err != nil {
		t.Fatal(err)
	}
	out, err := m.FetchForEvaluation(context.Background(), binding, evalInputs("a"))
	var fault *metadata.ReadFaultError
	if !errors.As(err, &fault) || out.Read == nil || out.Read.Accounting == nil {
		t.Fatalf("read=%+v err=%v, want ReadFaultError", out.Read, err)
	}
	var re *youtube.ReadError
	if !errors.As(err, &re) || re.Accounting == nil {
		t.Fatalf("typed ReadError lost: %v", err)
	}
	if out.Statuses["a"] != metadata.FetchFailed || calls.Load() != 1 {
		t.Fatalf("status=%v calls=%d", out.Statuses, calls.Load())
	}
	var rows int
	_ = ms.DB().QueryRow(`SELECT count(*) FROM transient_metadata`).Scan(&rows)
	if rows != 0 {
		t.Fatalf("no usable response may be saved after failed accounting; rows=%d", rows)
	}
}

// The refresh adapter uses the presence-aware by-ID read: real empty and zero
// values survive a lifecycle refresh, and unsupplied fields become NULL.
func TestRefreshKeepsRealEmptyDescriptionAndZeroDuration(t *testing.T) {
	body := `{"items":[{"id":"v1","snippet":{"title":"T","channelTitle":"C","description":"","publishedAt":"2026-01-01T00:00:00Z"},"contentDetails":{"duration":"P0D"}}]}`
	m, ms, _, _, _ := boundMetadataManager(t, 200, body, 10)
	now := time.Now().UTC()
	d, dur := "old description", 5
	if err := ms.Upsert(context.Background(), metadata.TransientMetadata{VideoID: "v1", Description: &d, DurationSeconds: &dur, FetchedAt: now, ExpiresAt: now.Add(time.Hour), IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if err := m.RefreshOnDemand(context.Background(), "v1"); err != nil {
		t.Fatal(err)
	}
	var desc, duration, channel any
	if err := ms.DB().QueryRow(`SELECT channel_name, description, duration_seconds FROM transient_metadata WHERE video_id='v1'`).Scan(&channel, &desc, &duration); err != nil {
		t.Fatal(err)
	}
	if channel != "C" || desc != "" || duration != int64(0) {
		t.Fatalf("row = %v %#v %#v, want C, '' and 0", channel, desc, duration)
	}
}

func TestRefreshStoresUnsuppliedFieldsAsNull(t *testing.T) {
	body := `{"items":[{"id":"v1","snippet":{"title":"T","publishedAt":"2026-01-01T00:00:00Z"},"contentDetails":{"duration":"garbage"}}]}`
	m, ms, _, _, _ := boundMetadataManager(t, 200, body, 10)
	now := time.Now().UTC()
	d, dur := "old description", 5
	if err := ms.Upsert(context.Background(), metadata.TransientMetadata{VideoID: "v1", Description: &d, DurationSeconds: &dur, FetchedAt: now, ExpiresAt: now.Add(time.Hour), IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if err := m.RefreshOnDemand(context.Background(), "v1"); err != nil {
		t.Fatal(err)
	}
	var desc, duration any
	if err := ms.DB().QueryRow(`SELECT description, duration_seconds FROM transient_metadata WHERE video_id='v1'`).Scan(&desc, &duration); err != nil {
		t.Fatal(err)
	}
	if desc != nil || duration != nil {
		t.Fatalf("description=%v duration=%v, want NULL", desc, duration)
	}
}

func TestFetchMetadataDeduplicatesAndChunksIDs(t *testing.T) {
	var ids []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids = append(ids, r.URL.Query().Get("id"))
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer upstream.Close()
	p, db := newTestPlaylistProvider(t, upstream.URL)
	fetcher := &metadataFetcher{auth: p.auth, baseURL: upstream.URL, ledger: quota.New(db)}
	var req []string
	for i := 0; i < 60; i++ {
		req = append(req, fmt.Sprintf("v%02d", i%55)) // 55 distinct, 5 repeated
	}
	if _, err := fetcher.FetchMetadata(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || len(strings.Split(ids[0], ",")) != 50 || len(strings.Split(ids[1], ",")) != 5 {
		t.Fatalf("calls = %d, want 50 + 5 unique IDs", len(ids))
	}
}
