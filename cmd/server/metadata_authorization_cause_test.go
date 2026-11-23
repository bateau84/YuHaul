package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/store"
)

type oauthRoundTripper func(*http.Request) (*http.Response, error)

func (f oauthRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Expired connected access that cannot be renewed must be read by the real
// production adapter (metadataFetcher over auth.DispatchWithQuota) as an
// authorization refusal, not as an unclassified failure. The same shape
// reached the receiving precondition as failed/unknown_error with no stop
// (review 771112e4, finding F2). Nothing here injects a renewal fact: the
// refusal comes out of the real Resolve step and through the real client.
func TestFetchForEvaluationUnrenewableAccessIsAuthorizationPendingWithItsCause(t *testing.T) {
	cases := []struct {
		name         string
		refreshToken string
		tokenStatus  int
		tokenBody    string
		wantCause    auth.AuthorizationCause
		wantRenewals int32
	}{
		{name: "no refresh credential", wantCause: auth.CauseReconnectRequired},
		{name: "refresh rejected by provider", refreshToken: "refresh", tokenStatus: http.StatusBadRequest,
			tokenBody: `{"error":"invalid_grant"}`, wantCause: auth.CauseReconnectRequired, wantRenewals: 1},
		{name: "provider temporarily unavailable", refreshToken: "refresh", tokenStatus: http.StatusServiceUnavailable,
			tokenBody: `{"error":"temporarily_unavailable"}`, wantCause: auth.CauseTemporaryUnavailable, wantRenewals: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			var dataCalls, renewals atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				dataCalls.Add(1)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"items":[]}`))
			}))
			t.Cleanup(upstream.Close)

			db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			key := strings.Repeat("k", 32)
			tokens, err := auth.NewSQLiteTokenStore(db, []byte(key))
			if err != nil {
				t.Fatal(err)
			}
			rt := oauthRoundTripper(func(r *http.Request) (*http.Response, error) {
				renewals.Add(1)
				return &http.Response{StatusCode: c.tokenStatus, Header: make(http.Header), Request: r,
					Body: io.NopCloser(strings.NewReader(c.tokenBody))}, nil
			})
			svc, err := auth.NewService(&auth.ServiceConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/cb",
				EncryptionKey: key, HTTPClient: &http.Client{Transport: rt}}, tokens, auth.NewInMemoryStateStore())
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			if err := tokens.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "expired", RefreshToken: c.refreshToken,
				TokenType: "Bearer", Expiry: now.Add(-time.Minute), ConnectedAt: now, UpdatedAt: now, Scopes: auth.DefaultScopes()}); err != nil {
				t.Fatal(err)
			}
			binding, err := svc.Bind(ctx)
			if err != nil {
				t.Fatal(err)
			}
			ledger := quota.New(db)
			fetcher := &metadataFetcher{auth: svc, baseURL: upstream.URL, ledger: ledger}
			m := metadata.NewManager(metadata.NewStore(db), fetcher, ledger, metadata.WithBoundFetcher(fetcher))

			// Three chunks: the first is refused, the unsent suffix must not be
			// sent and must carry the same status.
			ids := make([]string, 120)
			for i := range ids {
				ids[i] = fmt.Sprintf("vid%03d", i)
			}
			out, err := m.FetchForEvaluation(ctx, binding, evalInputs(ids...))
			if err != nil {
				t.Fatalf("a refused read is a status, not a local fault: %v", err)
			}
			for _, id := range ids {
				if out.Statuses[id] != metadata.FetchAuthorizationPending {
					t.Fatalf("%s = %s, want authorization_pending", id, out.Statuses[id])
				}
			}
			if out.Read == nil || out.Read.Authorization != c.wantCause {
				t.Fatalf("cause = %+v, want %q", out.Read, c.wantCause)
			}
			if out.Read.Accounting != nil || out.Read.Admitted || out.Attempts != 0 {
				t.Fatalf("a pre-admission refusal is no attempt: %+v attempts=%d", out.Read, out.Attempts)
			}
			if dataCalls.Load() != 0 {
				t.Fatalf("data API requests = %d, want 0", dataCalls.Load())
			}
			if renewals.Load() != c.wantRenewals {
				t.Fatalf("renewal requests = %d, want %d (one attempt, no retry loop)", renewals.Load(), c.wantRenewals)
			}
			var ledgerRows int
			if err := db.QueryRow(`SELECT count(*) FROM quota_ledger`).Scan(&ledgerRows); err != nil {
				t.Fatal(err)
			}
			if ledgerRows != 0 {
				t.Fatalf("ledger rows = %d, want 0: no debit for an unsent request", ledgerRows)
			}
		})
	}
}
