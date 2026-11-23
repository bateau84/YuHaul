package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/store/storetest"
)

func TestClassifyAuthorizationCause(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want AuthorizationCause
		pre  bool // a pre-admission authorization refusal
	}{
		{"refresh missing", errRefreshMissing, CauseReconnectRequired, true},
		{"refresh rejected", errRefreshRejected, CauseReconnectRequired, true},
		{"no connected account", ErrNoConnectedAccount, CauseReconnectRequired, true},
		{"temporary network", errTemporaryNetwork, CauseTemporaryUnavailable, true},
		{"temporary provider", errTemporaryProvider, CauseTemporaryUnavailable, true},
		{"renewal persistence", ErrRenewalPersistence, CauseTemporaryUnavailable, true},
		{"access expired before admission", ErrAccessExpiredBeforeAdmission, CauseTemporaryUnavailable, true},
		{"authorization changed", ErrAuthorizationChanged, CauseAuthorizationChanged, true},
		{"wrapped like the read client", fmt.Errorf("get videos: %w", fmt.Errorf("execute request: %w", errRefreshMissing)), CauseReconnectRequired, true},
		{"reconnect outranks temporary", errors.Join(errTemporaryProvider, errRefreshRejected), CauseReconnectRequired, true},
		{"temporary outranks changed", errors.Join(ErrAuthorizationChanged, errTemporaryNetwork), CauseTemporaryUnavailable, true},
		// A repeated stop has no cause of its own, but is still a refusal.
		{"run authorization stop", ErrRunAuthorizationStopped, "", true},
		// Not authorization conditions: configuration, malformed renewal,
		// cancellation and untyped errors stay out.
		{"client configuration", errClientConfiguration, "", false},
		{"invalid refresh response", errInvalidRefreshResponse, "", false},
		{"cancelled", context.Canceled, "", false},
		{"cancelled with a cause", errors.Join(context.Canceled, errRefreshMissing), CauseReconnectRequired, false},
		{"untyped", errors.New("auth: refresh authorization is unavailable"), "", false},
		{"nil", nil, "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := ClassifyAuthorizationCause(c.err); got != c.want {
				t.Fatalf("cause = %q, want %q", got, c.want)
			}
			if got := IsPreAdmissionAuthorizationRefusal(c.err); got != c.pre {
				t.Fatalf("pre-admission refusal = %v, want %v", got, c.pre)
			}
		})
	}
	if got := RenewalFailureCategory(errRefreshMissing); got != "refresh_missing" {
		t.Fatalf("category = %q, want refresh_missing", got)
	}
}

// Review 771112e4 F2: connected access that has expired, with no refresh
// credential, returned an untyped error from Resolve. It must be a typed
// reconnect-required refusal that the real dispatch classifies as a
// pre-admission authorization refusal: no request, no renewal, no hold.
func TestDispatchExpiredAccessWithoutRefreshIsATypedReconnectRefusal(t *testing.T) {
	ctx := context.Background()
	db := storetest.Open(t)
	var tokenCalls, sends atomic.Int32
	store, err := NewSQLiteTokenStore(db, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(&ServiceConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/cb",
		EncryptionKey: "01234567890123456789012345678901",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			tokenCalls.Add(1)
			return nil, errors.New("no renewal may be attempted")
		})}}, store, NewInMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, &TokenRecord{Provider: "youtube", AccessToken: "expired", TokenType: "Bearer",
		Expiry: time.Now().Add(-time.Minute), Scopes: DefaultScopes()}); err != nil {
		t.Fatal(err)
	}
	binding, err := svc.Bind(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ledger := quota.New(db)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:1/videos", nil)
	resp, receipt, hold, err := svc.DispatchWithQuota(ctx, binding, req, roundTripFunc(func(*http.Request) (*http.Response, error) {
		sends.Add(1)
		return nil, errors.New("must not be sent")
	}), ledger, quota.OpVideosList)
	if err == nil || resp != nil || hold != nil || receipt.AuthorizationID != "" {
		t.Fatalf("resp=%v hold=%v receipt=%+v err=%v", resp, hold, receipt, err)
	}
	if !errors.Is(err, errRefreshMissing) || ClassifyAuthorizationCause(err) != CauseReconnectRequired || !IsPreAdmissionAuthorizationRefusal(err) {
		t.Fatalf("not a typed reconnect-required refusal: %v", err)
	}
	if got := RenewalFailureCategory(err); got != "refresh_missing" {
		t.Fatalf("category = %q", got)
	}
	if sends.Load() != 0 || tokenCalls.Load() != 0 || ledgerRows(t, db) != 0 {
		t.Fatalf("sends=%d renewals=%d ledger=%d, want all zero", sends.Load(), tokenCalls.Load(), ledgerRows(t, db))
	}
	status, err := svc.Status(ctx)
	if err != nil || status.AuthorizationState != "reconnect_required" || status.AuthorizationReason == nil || *status.AuthorizationReason != "refresh_missing" {
		t.Fatalf("status must agree with the refusal: %+v %v", status, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
