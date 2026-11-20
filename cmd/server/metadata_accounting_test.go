package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/youtube"
)

func TestMetadataManagerAccountsActualReads(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		body          string
		category      string
		wantError     bool
		saveFailure   bool
		recordFailure bool
	}{
		{"success", 200, `{"items":[{"id":"v1","snippet":{"title":"title","publishedAt":"2026-01-01T00:00:00Z"}}]}`, "success", false, false, false},
		{"empty", 200, `{"items":[]}`, "success", true, false, false},
		{"invalid", 200, `{`, "success", true, false, false},
		{"unauthorized", 401, `{}`, "auth", true, false, false},
		{"auth forbidden", 403, `{"error":{"errors":[{"reason":"authError"}]}}`, "auth", true, false, false},
		{"quota forbidden", 403, `{"error":{"errors":[{"reason":"quotaExceeded"}]}}`, "quota", true, false, false},
		{"permanent forbidden", 403, `{}`, "permanent", true, false, false},
		{"server failure", 503, `{}`, "transient", true, false, false},
		{"no response", 0, ``, "transient", true, false, false},
		{"local save failure", 200, `{"items":[{"id":"v1","snippet":{"publishedAt":"2026-01-01T00:00:00Z"}}]}`, "success", true, true, false},
		{"record failure", 401, `{}`, "", true, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if tt.status == 0 {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
					return
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer upstream.Close()
			provider, db := newTestPlaylistProvider(t, upstream.URL)
			ms := metadata.NewStore(db)
			now := time.Now().UTC()
			if err := ms.Upsert(context.Background(), metadata.TransientMetadata{VideoID: "v1", FetchedAt: now, ExpiresAt: now.Add(time.Hour), IsActive: true}); err != nil {
				t.Fatal(err)
			}
			if tt.saveFailure {
				if _, err := db.Exec(`CREATE TRIGGER fail_metadata BEFORE UPDATE ON transient_metadata BEGIN SELECT RAISE(FAIL, 'save failed'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if tt.recordFailure {
				if _, err := db.Exec(`CREATE TRIGGER fail_quota BEFORE INSERT ON quota_ledger BEGIN SELECT RAISE(FAIL, 'record failed'); END`); err != nil {
					t.Fatal(err)
				}
			}
			fetcher := &metadataFetcher{auth: provider.auth, baseURL: upstream.URL, ledger: provider.ledger}
			manager := metadata.NewManager(ms, fetcher, provider.ledger)
			err := manager.RefreshOnDemand(context.Background(), "v1")
			if (err != nil) != tt.wantError {
				t.Fatalf("error = %v", err)
			}
			if calls.Load() != 1 {
				t.Fatalf("calls = %d", calls.Load())
			}
			if tt.name == "success" {
				var title string
				if err := db.QueryRow(`SELECT title FROM transient_metadata WHERE video_id = 'v1'`).Scan(&title); err != nil || title != "title" {
					t.Fatalf("saved title = %q, error = %v", title, err)
				}
			}
			if tt.recordFailure {
				var report *youtube.ReadError
				var api *youtube.APIError
				if !errors.As(err, &report) || report.Accounting == nil || !errors.As(err, &api) || api.Status != 401 {
					t.Fatalf("missing primary/accounting: %v", err)
				}
				if strings.Contains(err.Error(), "record failed") {
					t.Fatalf("unsafe accounting message: %v", err)
				}
			}
			var count int
			var category, operation string
			var units, callCount int
			if err := db.QueryRow(`SELECT count(*) FROM quota_ledger`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if tt.recordFailure {
				if count != 0 {
					t.Fatalf("rows = %d", count)
				}
				return
			}
			if count != 1 {
				t.Fatalf("rows = %d", count)
			}
			if err := db.QueryRow(`SELECT outcome_category, operation_type, units_consumed, call_count FROM quota_ledger`).Scan(&category, &operation, &units, &callCount); err != nil {
				t.Fatal(err)
			}
			if category != tt.category || operation != "videos_list" || units != 1 || callCount != 1 {
				t.Fatalf("ledger: %q %q %d %d", category, operation, units, callCount)
			}
		})
	}
}

func TestMetadataManagerPreAdmissionRefusalHasNoConsumption(t *testing.T) {
	provider, db := newTestPlaylistProvider(t, "http://127.0.0.1:1")
	fetcher := &metadataFetcher{auth: auth.NewDisabledService(), baseURL: provider.baseURL, ledger: provider.ledger}
	manager := metadata.NewManager(metadata.NewStore(db), fetcher, provider.ledger)
	if err := manager.RefreshOnDemand(context.Background(), "v1"); err == nil {
		t.Fatal("expected authorization refusal")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM quota_ledger`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("ledger rows = %d, error = %v", count, err)
	}
}
