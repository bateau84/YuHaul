package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
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

type readSignalBody struct {
	io.ReadCloser
	started chan struct{}
	once    sync.Once
}

func (b *readSignalBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	return b.ReadCloser.Read(p)
}

type readSignalTransport struct {
	base    http.RoundTripper
	started chan struct{}
}

func (t readSignalTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if resp != nil {
		resp.Body = &readSignalBody{ReadCloser: resp.Body, started: t.started}
	}
	return resp, err
}

func TestBoundReadInterruptedRejection(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			started := make(chan struct{})
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"authError"`)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer upstream.Close()
			p, db := newTestPlaylistProvider(t, upstream.URL)
			p.ledger = quota.New(db, quota.WithDefaultLimit(1))
			binding, err := p.auth.Bind(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			opts := append(boundReadOptions(p.auth, binding, p.ledger),
				youtube.WithBaseURL(upstream.URL),
				youtube.WithSingleAttemptTransport(readSignalTransport{base: upstream.Client().Transport, started: started}))
			client := youtube.NewAuthorizedClient(context.Background(), nil, opts...)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			type result struct {
				ids youtube.VideoIDs
				err error
			}
			done := make(chan result, 1)
			go func() {
				_, ids, err := client.GetVideosWithReturnedIDs(ctx, []string{"v1"})
				done <- result{ids, err}
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("body read did not start")
			}
			cancel()
			r := <-done
			var re *youtube.ReadError
			var api *youtube.APIError
			if !errors.As(r.err, &re) || re.Status != status || !errors.Is(re.Interruption, context.Canceled) ||
				re.Accounting == nil || r.ids.Complete {
				t.Fatalf("result=%+v read=%+v api=%+v", r, re, api)
			}
			if status == http.StatusOK {
				if errors.As(r.err, &api) {
					t.Fatalf("success status misrepresented as provider rejection: %+v", api)
				}
			} else if !errors.As(r.err, &api) || api.Status != status || api.Complete || api.Reason != "" {
				t.Fatalf("provider status/reason lost: %+v", api)
			}
			if status == http.StatusUnauthorized && re.Renewal == nil {
				t.Fatal("401 rejection observation did not report canceled context")
			}
			if status == http.StatusForbidden && re.Renewal != nil {
				t.Fatalf("incomplete 403 observed rejection: %v", re.Renewal)
			}
			var rows int
			if err := db.QueryRow(`SELECT count(*) FROM quota_ledger`).Scan(&rows); err != nil || rows != 0 {
				t.Fatalf("rows=%d err=%v", rows, err)
			}
			_, _, err = client.GetVideosWithReturnedIDs(context.Background(), []string{"v2"})
			if !errors.Is(err, auth.ErrQuotaRefused) || calls.Load() != 1 {
				t.Fatalf("held cost: err=%v calls=%d", err, calls.Load())
			}
		})
	}
}

func TestBoundReadRejectionFinalizesBeforeRenewal(t *testing.T) {
	for _, tc := range []struct {
		status     int
		failAppend bool
	}{{401, false}, {401, true}, {403, false}, {403, true}} {
		t.Run(fmt.Sprintf("status_%d_append_failure_%t", tc.status, tc.failAppend), func(t *testing.T) {
			var calls, renewals, rowsAtRenewal atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"authError"}]}}`)
			}))
			defer upstream.Close()
			p, db := newTestPlaylistProvider(t, upstream.URL)
			if tc.failAppend {
				if _, err := db.Exec(`CREATE TRIGGER reject_bound_read BEFORE INSERT ON quota_ledger BEGIN SELECT RAISE(FAIL, 'append failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				renewals.Add(1)
				var rows int
				if err := db.QueryRow(`SELECT count(*) FROM quota_ledger`).Scan(&rows); err != nil {
					t.Errorf("query during renewal: %v", err)
				}
				rowsAtRenewal.Store(int32(rows))
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"access_token":"renewed","token_type":"Bearer","expires_in":3600}`)
			}))
			defer oauth.Close()
			key := strings.Repeat("k", 32)
			tokenStore, err := auth.NewSQLiteTokenStore(db, []byte(key))
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			if err := tokenStore.Save(context.Background(), &auth.TokenRecord{Provider: "youtube", AccessToken: "old", RefreshToken: "refresh", TokenType: "Bearer", Expiry: now.Add(time.Hour), ConnectedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			p.auth, err = auth.NewService(&auth.ServiceConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: key, HTTPClient: &http.Client{Transport: localOAuthTransport{target: oauth.Client().Transport, url: oauth.URL}}}, tokenStore, auth.NewInMemoryStateStore())
			if err != nil {
				t.Fatal(err)
			}
			p.ledger = quota.New(db, quota.WithDefaultLimit(1))
			ctx := context.Background()
			binding, err := p.auth.Bind(ctx)
			if err != nil {
				t.Fatal(err)
			}
			opts := append(boundReadOptions(p.auth, binding, p.ledger), youtube.WithBaseURL(upstream.URL))
			client := youtube.NewAuthorizedClient(ctx, nil, opts...)
			_, ids, err := client.GetVideosWithReturnedIDs(ctx, []string{"v1"})
			var api *youtube.APIError
			if !errors.As(err, &api) || api.Status != tc.status || api.Reason != "authError" || ids.Complete {
				t.Fatalf("result ids=%+v err=%v", ids, err)
			}
			var re *youtube.ReadError
			if tc.failAppend {
				if !errors.As(err, &re) || re.Accounting == nil || re.Renewal != nil {
					t.Fatalf("expected independent accounting failure: %v", err)
				}
			} else if errors.As(err, &re) {
				t.Fatalf("unexpected auxiliary failure: %v", err)
			}
			wantRows := int32(1)
			if tc.failAppend {
				wantRows = 0
			}
			if renewals.Load() != 1 || rowsAtRenewal.Load() != wantRows || calls.Load() != 1 {
				t.Fatalf("renewals=%d rows at renewal=%d calls=%d", renewals.Load(), rowsAtRenewal.Load(), calls.Load())
			}
			rotated, err := tokenStore.Load(ctx)
			if err != nil || rotated.AccessToken != "renewed" {
				t.Fatalf("renewal not stored: %v", err)
			}
			_, _, err = client.GetVideosWithReturnedIDs(ctx, []string{"v2"})
			if !errors.Is(err, auth.ErrQuotaRefused) || calls.Load() != 1 {
				t.Fatalf("quota after finalize: err=%v calls=%d", err, calls.Load())
			}
		})
	}
}

func TestBoundReadForbiddenRequiresUnambiguousReasons(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason, category string
		complete                     bool
	}{
		{"duplicate_reason", `{"error":{"errors":[{"reason":"quotaExceeded","reason":"authError"}]}}`, "", "permanent", false},
		{"duplicate_errors", `{"error":{"errors":[{"reason":"quotaExceeded"}],"errors":[{"reason":"authError"}]}}`, "", "permanent", false},
		{"case_reason", `{"error":{"errors":[{"reason":"quotaExceeded","REASON":"authError"}]}}`, "", "permanent", false},
		{"case_errors", `{"error":{"errors":[{"reason":"quotaExceeded"}],"ERRORS":[{"reason":"authError"}]}}`, "", "permanent", false},
		{"case_error", `{"error":{"errors":[{"reason":"quotaExceeded"}]},"ERROR":{"errors":[{"reason":"authError"}]}}`, "", "permanent", false},
		{"single_case_variant", `{"ERROR":{"ERRORS":[{"REASON":"quotaExceeded"}]}}`, "quotaExceeded", "quota", true},
		{"quota", `{"error":{"errors":[{"reason":"quotaExceeded"}]}}`, "quotaExceeded", "quota", true},
		{"forbidden", `{"error":{"errors":[{"reason":"forbidden"}]}}`, "forbidden", "permanent", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			p, db := newTestPlaylistProvider(t, upstream.URL)
			ctx := context.Background()
			binding, err := p.auth.Bind(ctx)
			if err != nil {
				t.Fatal(err)
			}
			client := youtube.NewAuthorizedClient(ctx, nil, append(boundReadOptions(p.auth, binding, p.ledger), youtube.WithBaseURL(upstream.URL))...)
			_, ids, err := client.GetVideosWithReturnedIDs(ctx, []string{"v1"})
			var api *youtube.APIError
			if !errors.As(err, &api) || api.Status != 403 || api.Reason != tc.reason || api.Complete != tc.complete || ids.Complete {
				t.Fatalf("ids=%+v error=%v api=%+v", ids, err, api)
			}
			var category string
			var count int
			if err := db.QueryRow(`SELECT count(*), coalesce(max(outcome_category),'') FROM quota_ledger`).Scan(&count, &category); err != nil || count != 1 || category != tc.category {
				t.Fatalf("ledger count=%d category=%q error=%v", count, category, err)
			}
			tokenStore, err := auth.NewSQLiteTokenStore(db, []byte(strings.Repeat("k", 32)))
			if err != nil {
				t.Fatal(err)
			}
			token, err := tokenStore.Load(ctx)
			if err != nil || token.AccessRejected || token.RenewalFailure != "" || calls.Load() != 1 {
				t.Fatalf("unexpected rejection or replay: token=%+v err=%v calls=%d", token, err, calls.Load())
			}
		})
	}
}

func TestBoundReadQuotaUnavailableIsAccountingFailure(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	defer upstream.Close()
	p, _ := newTestPlaylistProvider(t, upstream.URL)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	ledger := quota.New(db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	p.ledger = ledger
	ctx := context.Background()
	binding, err := p.auth.Bind(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opts := append(boundReadOptions(p.auth, binding, ledger), youtube.WithBaseURL(upstream.URL))
	client := youtube.NewAuthorizedClient(ctx, nil, opts...)
	_, ids, err := client.GetVideosWithReturnedIDs(ctx, []string{"v1"})
	var readErr *youtube.ReadError
	if !errors.As(err, &readErr) || !errors.Is(readErr.Accounting, auth.ErrQuotaAccounting) {
		t.Fatalf("read error = %v, want accounting failure", err)
	}
	if errors.Is(err, auth.ErrQuotaRefused) || ids.Complete || calls.Load() != 0 {
		t.Fatalf("unexpected refusal or dispatch: err=%v calls=%d", err, calls.Load())
	}
}

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
