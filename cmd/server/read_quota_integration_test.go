package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/youtube"
)

type localOAuthTransport struct {
	target http.RoundTripper
	url    string
}

func (t localOAuthTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	local, err := http.NewRequestWithContext(r.Context(), r.Method, t.url, r.Body)
	if err != nil {
		return nil, err
	}
	clone.URL = local.URL
	clone.Host = local.Host
	return t.target.RoundTrip(clone)
}

func TestMetadataRenewalLosesFinalQuotaUnit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var refreshes, metadataCalls, playlistCalls atomic.Int32
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshes.Add(1)
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"rotated-access","refresh_token":"rotated-refresh","token_type":"Bearer","expires_in":3600}`)
	}))
	defer oauth.Close()
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "playlists") {
			playlistCalls.Add(1)
			fmt.Fprint(w, `{"items":[]}`)
			return
		}
		metadataCalls.Add(1)
		fmt.Fprint(w, `{"items":[]}`)
	}))
	defer upstream.Close()
	p, db := newTestPlaylistProvider(t, upstream.URL)
	key := strings.Repeat("k", 32)
	tokenStore, err := auth.NewSQLiteTokenStore(db, []byte(key))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := tokenStore.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "expired", RefreshToken: "old-refresh", TokenType: "Bearer", Expiry: now.Add(-time.Hour), ConnectedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	p.auth, err = auth.NewService(&auth.ServiceConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: key, HTTPClient: &http.Client{Transport: localOAuthTransport{target: oauth.Client().Transport, url: oauth.URL}}}, tokenStore, auth.NewInMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	p.ledger = quota.New(db, quota.WithDefaultLimit(1))
	manager := metadata.NewManager(metadata.NewStore(db), &metadataFetcher{auth: p.auth, baseURL: upstream.URL, ledger: p.ledger}, p.ledger)
	result := make(chan error, 1)
	go func() { result <- manager.RefreshOnDemand(ctx, "v1") }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("renewal did not start")
	}
	// A separate active request authority represents another production spender;
	// both adapters share the same actual ledger and SQLite database for quota.
	competitor, _ := newTestPlaylistProvider(t, upstream.URL)
	competitor.ledger = p.ledger
	_, spendErr := competitor.ListPlaylists(ctx)
	unblock()
	if spendErr != nil {
		t.Fatalf("competing playlist read: %v", spendErr)
	}
	if err := <-result; !errors.Is(err, auth.ErrQuotaRefused) {
		t.Fatalf("metadata result = %v, want quota refusal", err)
	}
	rotated, err := tokenStore.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.AccessToken != "rotated-access" || rotated.RefreshToken != "rotated-refresh" {
		t.Fatal("rotated credentials not committed")
	}
	var rows, videos, playlists, units int
	if err := db.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(operation_type='videos_list'),0), coalesce(sum(operation_type='playlists_list'),0), coalesce(sum(units_consumed),0) FROM quota_ledger`).Scan(&rows, &videos, &playlists, &units); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || videos != 0 || playlists != 1 || units != 1 || metadataCalls.Load() != 0 || playlistCalls.Load() != 1 || refreshes.Load() != 1 {
		t.Fatalf("rows=%d videos=%d playlists=%d units=%d metadata calls=%d playlist calls=%d refreshes=%d", rows, videos, playlists, units, metadataCalls.Load(), playlistCalls.Load(), refreshes.Load())
	}
}

func TestMetadataManagerConcurrentFinalQuotaUnit(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(entered)
		<-release
		fmt.Fprint(w, `{"items":[{"id":"v1","snippet":{"title":"title","publishedAt":"2024-01-01T00:00:00Z"}}]}`)
	}))
	defer upstream.Close()
	p, db := newTestPlaylistProvider(t, upstream.URL)
	p.ledger = quota.New(db, quota.WithDefaultLimit(1))
	fetcher := &metadataFetcher{auth: p.auth, baseURL: upstream.URL, ledger: p.ledger}
	manager := metadata.NewManager(metadata.NewStore(db), fetcher, p.ledger)
	first := make(chan error, 1)
	go func() { first <- manager.RefreshOnDemand(context.Background(), "v1") }()
	<-entered
	if err := manager.RefreshOnDemand(context.Background(), "v2"); err != nil {
		close(release)
		t.Fatalf("manager should defer: %v", err)
	}
	_, err := fetcher.FetchMetadata(context.Background(), []string{"v2"})
	if err == nil || !errors.Is(err, auth.ErrQuotaRefused) {
		close(release)
		t.Fatalf("second read = %v, want quota refusal", err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM quota_ledger WHERE operation_type='videos_list'`).Scan(&n); err != nil || n != 1 || calls.Load() != 1 {
		t.Fatalf("rows=%d calls=%d err=%v", n, calls.Load(), err)
	}
}

func TestMetadataManagerReadFailureAccounting(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		failRecord bool
	}{{"unauthorized", 401, false}, {"no response", 0, false}, {"recording failure", 200, true}} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status == 0 {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						conn.Close()
					}
					return
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, `{"items":[{"id":"v1"}]}`)
			}))
			defer upstream.Close()
			p, db := newTestPlaylistProvider(t, upstream.URL)
			if tc.failRecord {
				if _, err := db.Exec(`CREATE TRIGGER reject_read BEFORE INSERT ON quota_ledger BEGIN SELECT RAISE(FAIL, 'injected'); END`); err != nil {
					t.Fatal(err)
				}
			}
			manager := metadata.NewManager(metadata.NewStore(db), &metadataFetcher{auth: p.auth, baseURL: upstream.URL, ledger: p.ledger}, p.ledger)
			err := manager.RefreshOnDemand(context.Background(), "v1")
			if err == nil {
				t.Fatal("expected failure")
			}
			var re *youtube.ReadError
			if tc.failRecord && (!errors.As(err, &re) || re.Accounting == nil) {
				t.Fatalf("missing accounting error: %v", err)
			}
			var count int
			if e := db.QueryRow(`SELECT count(*) FROM quota_ledger WHERE operation_type='videos_list'`).Scan(&count); e != nil {
				t.Fatal(e)
			}
			want := 1
			if tc.failRecord {
				want = 0
			}
			if count != want {
				t.Fatalf("rows=%d want=%d", count, want)
			}
		})
	}
}
