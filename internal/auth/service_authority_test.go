package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

type authorityTestTransport func(*http.Request) (*http.Response, error)

func (f authorityTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func authorityTestResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func authorityTestService(t *testing.T, store TokenStore, transport authorityTestTransport) *Service {
	t.Helper()
	key := newTestKey(t)
	s, err := NewService(&ServiceConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(key), HTTPClient: &http.Client{Transport: transport}}, store, NewInMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	s.oauthConfig.Endpoint.AuthStyle = oauth2.AuthStyleInParams
	return s
}

func authorityTestSeed(t *testing.T, store *SQLiteTokenStore, expiry time.Time) {
	t.Helper()
	if err := store.Save(context.Background(), &TokenRecord{Provider: "youtube", AccessToken: "old-access", RefreshToken: "old-refresh", TokenType: "Bearer", Expiry: expiry, Scopes: DefaultScopes(), ConnectedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

type authorityDelayedLoad struct {
	*SQLiteTokenStore
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (s *authorityDelayedLoad) Load(ctx context.Context) (*TokenRecord, error) {
	record, err := s.SQLiteTokenStore.Load(ctx)
	if s.calls.Add(1) == 1 {
		close(s.entered)
		<-s.release
	}
	return record, err
}

func TestActiveTokenRejectsUsableStaleReadAfterCallbackReplacement(t *testing.T) {
	ctx := context.Background()
	base := newTestStore(t, newTestKey(t))
	authorityTestSeed(t, base, time.Now().Add(time.Hour))
	store := &authorityDelayedLoad{SQLiteTokenStore: base, entered: make(chan struct{}), release: make(chan struct{})}
	s := authorityTestService(t, store, func(*http.Request) (*http.Response, error) {
		return authorityTestResponse(`{"access_token":"replacement","token_type":"Bearer","expires_in":3600}`), nil
	})
	attempt, err := s.InitiateOAuth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		token *oauth2.Token
		err   error
	}
	done := make(chan result, 1)
	go func() { token, err := s.ActiveToken(ctx); done <- result{token, err} }()
	<-store.entered
	if _, err := s.HandleOAuthCallback(ctx, "new-code", attempt.State); err != nil {
		t.Fatal(err)
	}
	close(store.release)
	got := <-done
	if got.err == nil && got.token.AccessToken == "old-access" {
		t.Fatal("ActiveToken returned old access after accepted callback replacement")
	}
}

func TestDisconnectDoesNotDeleteLaterAcceptedCallback(t *testing.T) {
	ctx := context.Background()
	base := newTestStore(t, newTestKey(t))
	authorityTestSeed(t, base, time.Now().Add(time.Hour))
	store := &authorityDelayedLoad{SQLiteTokenStore: base, entered: make(chan struct{}), release: make(chan struct{})}
	s := authorityTestService(t, store, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/revoke" {
			return authorityTestResponse(`{}`), nil
		}
		return authorityTestResponse(`{"access_token":"replacement","token_type":"Bearer","expires_in":3600}`), nil
	})
	done := make(chan error, 1)
	go func() { done <- s.Disconnect(ctx) }()
	<-store.entered
	attempt, err := s.InitiateOAuth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandleOAuthCallback(ctx, "new-code", attempt.State); err != nil {
		t.Fatal(err)
	}
	close(store.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	record, err := base.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if record == nil || record.AccessToken != "replacement" {
		t.Fatal("older Disconnect deleted the later accepted connection")
	}
}

func TestOAuthInitiationDoesNotInvalidateInFlightRefreshRotation(t *testing.T) {
	ctx := context.Background()
	base := newTestStore(t, newTestKey(t))
	authorityTestSeed(t, base, time.Now().Add(-time.Hour))
	entered, release := make(chan struct{}), make(chan struct{})
	s := authorityTestService(t, base, func(*http.Request) (*http.Response, error) {
		close(entered)
		<-release
		return authorityTestResponse(`{"access_token":"replacement","refresh_token":"rotated","token_type":"Bearer","expires_in":3600}`), nil
	})
	done := make(chan error, 1)
	go func() { _, err := s.ActiveToken(ctx); done <- err }()
	<-entered
	if _, err := s.InitiateOAuth(ctx); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	record, err := base.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if record.RefreshToken != "rotated" {
		t.Fatal("OAuth initiation alone discarded the accepted refresh rotation")
	}
}

func TestBoundAuthorizationRejectsReplacementAndDisconnect(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, newTestKey(t))
	authorityTestSeed(t, store, time.Now().Add(time.Hour))
	var exchanges atomic.Int32
	s := authorityTestService(t, store, func(*http.Request) (*http.Response, error) {
		exchanges.Add(1)
		return authorityTestResponse(`{"access_token":"replacement","token_type":"Bearer","expires_in":3600}`), nil
	})
	binding, err := s.Bind(ctx)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := s.InitiateOAuth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandleOAuthCallback(ctx, "new-code", attempt.State); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve(ctx, binding); err != ErrAuthorizationChanged {
		t.Fatalf("Resolve after replacement error = %v, want ErrAuthorizationChanged", err)
	}
	if exchanges.Load() != 1 {
		t.Fatalf("token endpoint calls = %d, want callback exchange only", exchanges.Load())
	}

	newBinding, err := s.Bind(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve(ctx, newBinding); err == nil {
		t.Fatal("Resolve after disconnect succeeded")
	}
}

func TestBoundResolveRenewsWithinIdentity(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, newTestKey(t))
	authorityTestSeed(t, store, time.Now().Add(-time.Hour))
	s := authorityTestService(t, store, func(*http.Request) (*http.Response, error) {
		return authorityTestResponse(`{"access_token":"renewed","token_type":"Bearer","expires_in":3600}`), nil
	})
	binding, err := s.Bind(ctx)
	if err != nil {
		t.Fatal(err)
	}
	initial, _ := store.Load(ctx)
	token, err := s.Resolve(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "renewed" {
		t.Fatalf("access token = %q, want renewed", token.AccessToken)
	}
	current, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.AuthorizationID != initial.AuthorizationID || current.CredentialRevision != initial.CredentialRevision+1 {
		t.Fatalf("renewal changed authority identity/revision unexpectedly: before=%+v after=%+v", initial, current)
	}
}

func TestDispatchFencesLateAdmissionAfterDisconnect(t *testing.T) {
	ctx := context.Background()
	base := newTestStore(t, newTestKey(t))
	authorityTestSeed(t, base, time.Now().Add(time.Hour))
	var sends atomic.Int32
	s := authorityTestService(t, base, func(*http.Request) (*http.Response, error) { return authorityTestResponse(`{}`), nil })
	binding, err := s.Bind(ctx)
	if err != nil {
		t.Fatal(err)
	}
	store := &authorityDelayedLoad{SQLiteTokenStore: base, entered: make(chan struct{}), release: make(chan struct{})}
	s.store = store
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://youtube.test/insert", nil)
	done := make(chan error, 1)
	go func() {
		_, _, err := s.Dispatch(ctx, binding, req, authorityTestTransport(func(*http.Request) (*http.Response, error) { sends.Add(1); return authorityTestResponse(`{}`), nil }))
		done <- err
	}()
	<-store.entered
	// Dispatch is paused before its authority check. Disconnect must fence it.
	if err := s.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	close(store.release)
	if err := <-done; !errors.Is(err, ErrAuthorizationChanged) {
		t.Fatalf("Dispatch error = %v, want authority changed", err)
	}
	if sends.Load() != 0 {
		t.Fatalf("transport calls = %d, want 0", sends.Load())
	}
}

func TestDispatchReceiptAndReplacementAreLinearized(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, newTestKey(t))
	authorityTestSeed(t, store, time.Now().Add(time.Hour))
	entered, release := make(chan struct{}), make(chan struct{})
	s := authorityTestService(t, store, func(*http.Request) (*http.Response, error) {
		return authorityTestResponse(`{"access_token":"replacement","token_type":"Bearer","expires_in":3600}`), nil
	})
	binding, err := s.Bind(ctx)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://youtube.test/insert", nil)
	transport := authorityTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer old-access" {
			t.Errorf("unexpected access header")
		}
		close(entered)
		<-release
		return authorityTestResponse(`{}`), nil
	})
	type outcome struct {
		receipt AdmissionReceipt
		err     error
	}
	done := make(chan outcome, 1)
	go func() { _, receipt, err := s.Dispatch(ctx, binding, req, transport); done <- outcome{receipt, err} }()
	<-entered
	// A replacement cannot retroactively revoke a request already accepted by
	// the transport boundary; it only fences subsequent admissions.
	attempt, err := s.InitiateOAuth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandleOAuthCallback(ctx, "new-code", attempt.State); err != nil {
		t.Fatal(err)
	}
	close(release)
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	id, _ := BindingIdentity(binding)
	if got.receipt.AuthorizationID != id || got.receipt.CredentialRevision == 0 || got.receipt.AdmittedAt.IsZero() {
		t.Fatalf("invalid admission receipt: %+v", got.receipt)
	}
	if _, _, err := s.Dispatch(ctx, binding, req, transport); !errors.Is(err, ErrAuthorizationChanged) {
		t.Fatalf("late dispatch error = %v, want authority changed", err)
	}
}
