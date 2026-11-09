package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/store/storetest"

	"golang.org/x/oauth2"
)

// newTestKey returns a deterministic 32-byte key for tests.
func newTestKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}
	return key
}

// newTestStore returns an in-memory SQLite token store for tests.
func newTestStore(t *testing.T, key []byte) *SQLiteTokenStore {
	t.Helper()
	db := storetest.Open(t)
	store, err := NewSQLiteTokenStore(db, key)
	if err != nil {
		t.Fatalf("failed to create token store: %v", err)
	}
	return store
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := newTestKey(t)
	plaintext := []byte("sensitive-token-value")

	ciphertext, err := encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}
	if len(ciphertext) == 0 {
		t.Fatal("ciphertext is empty")
	}

	decrypted, err := decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Fatalf("decrypted %q, want %q", decrypted, plaintext)
	}
}

func TestDecryptTamperedCiphertext(t *testing.T) {
	key := newTestKey(t)
	plaintext := []byte("secret")

	ciphertext, err := encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}
	ciphertext[len(ciphertext)-1] ^= 0xFF

	_, err = decrypt(key, ciphertext)
	if err == nil {
		t.Fatal("expected error for tampered ciphertext, got nil")
	}
	if !strings.Contains(err.Error(), "decryption failed") {
		t.Fatalf("expected decryption failed error, got %v", err)
	}
}

func TestSQLiteTokenStoreSaveLoadDelete(t *testing.T) {
	key := newTestKey(t)
	store := newTestStore(t, key)
	ctx := context.Background()

	record := &TokenRecord{
		Provider:     "youtube",
		AccessToken:  "access-token-123",
		RefreshToken: "refresh-token-456",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
		Scopes:       []string{ScopeYouTubeReadonly, ScopeYouTube},
		ConnectedAt:  time.Now().Add(-time.Hour),
		UpdatedAt:    time.Now(),
	}

	if err := store.Save(ctx, record); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected loaded record, got nil")
	}
	if loaded.AccessToken != record.AccessToken {
		t.Errorf("AccessToken = %q, want %q", loaded.AccessToken, record.AccessToken)
	}
	if loaded.RefreshToken != record.RefreshToken {
		t.Errorf("RefreshToken = %q, want %q", loaded.RefreshToken, record.RefreshToken)
	}
	if len(loaded.Scopes) != len(record.Scopes) {
		t.Errorf("Scopes length = %d, want %d", len(loaded.Scopes), len(record.Scopes))
	}

	if err := store.Delete(ctx); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	afterDelete, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load after delete failed: %v", err)
	}
	if afterDelete != nil {
		t.Fatal("expected nil record after delete")
	}
}

func TestSQLiteTokenStoreConditionalCredentialWrites(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, newTestKey(t))
	base := &TokenRecord{Provider: "youtube", AccessToken: "access-one", RefreshToken: "refresh-one", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour), Scopes: []string{ScopeYouTube}, ConnectedAt: time.Now(), UpdatedAt: time.Now()}
	first, err := store.ReplaceAuthorization(ctx, nil, base)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AuthorizationID != first.AuthorizationID || loaded.CredentialRevision != 1 {
		t.Fatalf("loaded version = %+v, want %+v", loaded, first)
	}

	rotated := *loaded
	rotated.AccessToken = "access-two"
	rotated.RefreshToken = "refresh-two"
	rotated.UpdatedAt = time.Now()
	second, err := store.SaveRefresh(ctx, first, &rotated)
	if err != nil {
		t.Fatal(err)
	}
	if second.AuthorizationID != first.AuthorizationID || second.Revision != 2 {
		t.Fatalf("refresh version = %+v", second)
	}
	stale := *loaded
	stale.AccessToken = "obsolete-access"
	if _, err := store.SaveRefresh(ctx, first, &stale); !errors.Is(err, ErrAuthorizationChanged) {
		t.Fatalf("stale refresh error = %v, want ErrAuthorizationChanged", err)
	}

	withoutRotatedRefresh := rotated
	withoutRotatedRefresh.AccessToken = "access-three"
	withoutRotatedRefresh.RefreshToken = ""
	third, err := store.SaveRefresh(ctx, second, &withoutRotatedRefresh)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RefreshToken != "refresh-two" || loaded.CredentialRevision != third.Revision {
		t.Fatalf("omitted refresh was not preserved: %+v", loaded)
	}

	replacement, err := store.ReplaceAuthorization(ctx, &third, &TokenRecord{Provider: "youtube", AccessToken: "new-account", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour), ConnectedAt: time.Now(), UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.AuthorizationID == third.AuthorizationID || replacement.Revision != 1 {
		t.Fatalf("replacement version = %+v", replacement)
	}
	if err := store.DeleteIfCurrent(ctx, third); !errors.Is(err, ErrAuthorizationChanged) {
		t.Fatalf("stale delete error = %v", err)
	}
	if err := store.DeleteIfCurrent(ctx, replacement); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteTokenStoreLoadWithWrongKey(t *testing.T) {
	key := newTestKey(t)
	wrongKey := newTestKey(t)
	store := newTestStore(t, key)
	ctx := context.Background()

	if err := store.Save(ctx, &TokenRecord{Provider: "youtube", AccessToken: "x", TokenType: "Bearer", ConnectedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	wrongStore, err := NewSQLiteTokenStore(store.db, wrongKey)
	if err != nil {
		t.Fatalf("NewSQLiteTokenStore failed: %v", err)
	}
	_, err = wrongStore.Load(ctx)
	if err == nil {
		t.Fatal("expected decryption error with wrong key, got nil")
	}
}

func TestServiceConfigValidation(t *testing.T) {
	key := newTestKey(t)
	cases := []struct {
		name    string
		cfg     *ServiceConfig
		wantErr string
	}{
		{
			name:    "missing client id",
			cfg:     &ServiceConfig{ClientSecret: "s", RedirectURL: "http://localhost/callback", EncryptionKey: string(key)},
			wantErr: "ClientID",
		},
		{
			name:    "missing client secret",
			cfg:     &ServiceConfig{ClientID: "id", RedirectURL: "http://localhost/callback", EncryptionKey: string(key)},
			wantErr: "ClientSecret",
		},
		{
			name:    "missing redirect url",
			cfg:     &ServiceConfig{ClientID: "id", ClientSecret: "s", EncryptionKey: string(key)},
			wantErr: "RedirectURL",
		},
		{
			name:    "invalid encryption key",
			cfg:     &ServiceConfig{ClientID: "id", ClientSecret: "s", RedirectURL: "http://localhost/callback", EncryptionKey: "short"},
			wantErr: "EncryptionKey",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewService(tc.cfg, nil, nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestOAuthFlowEndToEnd(t *testing.T) {
	key := newTestKey(t)
	store := newTestStore(t, key)
	states := NewInMemoryStateStore()

	// Mock Google's token endpoint.
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form failed: %v", err)
		}
		if got := r.FormValue("code"); got != "test-auth-code" {
			t.Errorf("code = %q, want test-auth-code", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "mock-access-token",
			"refresh_token": "mock-refresh-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer tokenServer.Close()

	cfg := &ServiceConfig{
		ClientID:      "test-client-id",
		ClientSecret:  "test-client-secret",
		RedirectURL:   "http://localhost:8080/api/auth/callback",
		EncryptionKey: string(key),
		Scopes:        DefaultScopes(),
		HTTPClient:    tokenServer.Client(),
	}

	svc, err := NewService(cfg, store, states)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	// Override the OAuth2 endpoint to point at our mock token server.
	svc.oauthConfig.Endpoint = oauth2.Endpoint{
		AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL: tokenServer.URL,
	}

	ctx := context.Background()

	// 1. Initiate flow.
	initResp, err := svc.InitiateOAuth(ctx)
	if err != nil {
		t.Fatalf("InitiateOAuth failed: %v", err)
	}
	if initResp.AuthURL == "" {
		t.Fatal("expected auth URL")
	}
	if initResp.State == "" {
		t.Fatal("expected state")
	}

	// 2. Simulate callback.
	callbackResp, err := svc.HandleOAuthCallback(ctx, "test-auth-code", initResp.State)
	if err != nil {
		t.Fatalf("HandleOAuthCallback failed: %v", err)
	}
	if !callbackResp.Connected {
		t.Fatal("expected connected true")
	}
	if callbackResp.Provider != "youtube" {
		t.Errorf("provider = %q, want youtube", callbackResp.Provider)
	}

	// 3. Verify status.
	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if !status.Connected {
		t.Fatal("expected status connected")
	}

	// 4. Verify active token.
	tok, err := svc.ActiveToken(ctx)
	if err != nil {
		t.Fatalf("ActiveToken failed: %v", err)
	}
	if tok.AccessToken != "mock-access-token" {
		t.Errorf("access token = %q, want mock-access-token", tok.AccessToken)
	}

	// 5. Disconnect.
	if err := svc.Disconnect(ctx); err != nil {
		t.Fatalf("Disconnect failed: %v", err)
	}
	status, err = svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status after disconnect failed: %v", err)
	}
	if status.Connected {
		t.Fatal("expected disconnected status")
	}
}

func TestOAuthCallbackInvalidState(t *testing.T) {
	key := newTestKey(t)
	store := newTestStore(t, key)
	states := NewInMemoryStateStore()
	cfg := &ServiceConfig{
		ClientID:      "id",
		ClientSecret:  "secret",
		RedirectURL:   "http://localhost/callback",
		EncryptionKey: string(key),
	}
	svc, err := NewService(cfg, store, states)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	_, err = svc.HandleOAuthCallback(context.Background(), "code", "invalid-state")
	if err == nil {
		t.Fatal("expected error for invalid state")
	}
}

func TestOAuthCallbackInitiatedBeforeDisconnectCannotReconnect(t *testing.T) {
	ctx := context.Background()
	key := newTestKey(t)
	store := newTestStore(t, key)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","token_type":"Bearer","expires_in":3600}`))
	}))
	defer tokenServer.Close()
	svc, err := NewService(&ServiceConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(key), HTTPClient: tokenServer.Client()}, store, NewInMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	svc.oauthConfig.Endpoint = oauth2.Endpoint{TokenURL: tokenServer.URL}
	init, err := svc.InitiateOAuth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.HandleOAuthCallback(ctx, "old-code", init.State); !errors.Is(err, ErrAuthorizationChanged) {
		t.Fatalf("stale callback error = %v, want ErrAuthorizationChanged", err)
	}
	status, err := svc.Status(ctx)
	if err != nil || status.Connected {
		t.Fatalf("stale callback restored authority: status=%+v err=%v", status, err)
	}
}

type failingDisconnectStore struct{ *SQLiteTokenStore }

func (s failingDisconnectStore) DeleteIfCurrent(context.Context, CredentialVersion) error {
	return errors.New("injected delete failure")
}

type authRoundTripFunc func(*http.Request) (*http.Response, error)

func (f authRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDisconnectFenceSurvivesCredentialDeletionFailure(t *testing.T) {
	ctx := context.Background()
	key := newTestKey(t)
	base := newTestStore(t, key)
	if err := base.Save(ctx, &TokenRecord{Provider: "youtube", AccessToken: "access", RefreshToken: "refresh", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour), Scopes: DefaultScopes(), ConnectedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	store := failingDisconnectStore{base}
	client := &http.Client{Transport: authRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}, nil
	})}
	svc, err := NewService(&ServiceConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(key), HTTPClient: client}, store, NewInMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Disconnect(ctx); err == nil {
		t.Fatal("Disconnect succeeded despite injected local deletion failure")
	}
	if token, err := svc.ActiveToken(ctx); err == nil || token != nil {
		t.Fatalf("fenced token remained usable: token=%v err=%v", token, err)
	}
	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Connected || status.AuthorizationState != "disconnected" {
		t.Fatalf("fence not reflected after deletion failure: %+v", status)
	}
}

func TestHandlerInitiateAndCallback(t *testing.T) {
	key := newTestKey(t)
	store := newTestStore(t, key)
	states := NewInMemoryStateStore()

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "mock-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer tokenServer.Close()

	cfg := &ServiceConfig{
		ClientID:      "id",
		ClientSecret:  "secret",
		RedirectURL:   "http://localhost/callback",
		EncryptionKey: string(key),
		Scopes:        DefaultScopes(),
		HTTPClient:    tokenServer.Client(),
	}
	svc, err := NewService(cfg, store, states)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	svc.oauthConfig.Endpoint = oauth2.Endpoint{
		AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL: tokenServer.URL,
	}

	authHandler := svc.Handler()

	// Initiate.
	req := httptest.NewRequest(http.MethodPost, "/initiate", nil)
	rec := httptest.NewRecorder()
	authHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("initiate status = %d, want 200", rec.Code)
	}
	var initiateResp OAuthInitiateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &initiateResp); err != nil {
		t.Fatalf("unmarshal initiate response: %v", err)
	}

	// Callback.
	callbackURL := fmt.Sprintf("/callback?code=code&state=%s", url.QueryEscape(initiateResp.State))
	req = httptest.NewRequest(http.MethodGet, callbackURL, nil)
	rec = httptest.NewRecorder()
	authHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("callback status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// Account status.
	accountHandler := svc.AccountHandler()
	req = httptest.NewRequest(http.MethodGet, "/status", nil)
	rec = httptest.NewRecorder()
	accountHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status status = %d, want 200", rec.Code)
	}
	var status ConnectionStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if !status.Connected {
		t.Fatal("expected connected")
	}

	// Disconnect.
	req = httptest.NewRequest(http.MethodDelete, "/", nil)
	rec = httptest.NewRecorder()
	accountHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("disconnect status = %d, want 200", rec.Code)
	}
}

func TestHandlerCallbackRedirectsBrowser(t *testing.T) {
	key := newTestKey(t)
	store := newTestStore(t, key)
	states := NewInMemoryStateStore()

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "mock-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer tokenServer.Close()

	cfg := &ServiceConfig{
		ClientID:      "id",
		ClientSecret:  "secret",
		RedirectURL:   "http://localhost/callback",
		EncryptionKey: string(key),
		Scopes:        DefaultScopes(),
		HTTPClient:    tokenServer.Client(),
	}
	svc, err := NewService(cfg, store, states)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	svc.oauthConfig.Endpoint = oauth2.Endpoint{
		AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL: tokenServer.URL,
	}

	authHandler := svc.Handler()

	// Initiate to obtain a valid state.
	req := httptest.NewRequest(http.MethodPost, "/initiate", nil)
	rec := httptest.NewRecorder()
	authHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("initiate status = %d, want 200", rec.Code)
	}
	var initiateResp OAuthInitiateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &initiateResp); err != nil {
		t.Fatalf("unmarshal initiate response: %v", err)
	}

	// Browser callback: must redirect with 303 and auth=connected.
	callbackURL := fmt.Sprintf("/callback?code=code&state=%s", url.QueryEscape(initiateResp.State))
	req = httptest.NewRequest(http.MethodGet, callbackURL, nil)
	req.Header.Set("Accept", "text/html")
	rec = httptest.NewRecorder()
	authHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("callback status = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "/settings?auth=connected" {
		t.Fatalf("location = %q, want /settings?auth=connected", loc)
	}

	// Browser callback with invalid state: must redirect with auth=error.
	req = httptest.NewRequest(http.MethodGet, "/callback?code=code&state=bad", nil)
	req.Header.Set("Accept", "text/html")
	rec = httptest.NewRecorder()
	authHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("error callback status = %d, want 303", rec.Code)
	}
	loc = rec.Header().Get("Location")
	if loc != "/settings?auth=error" {
		t.Fatalf("error location = %q, want /settings?auth=error", loc)
	}
}

func TestHandlerCallbackInvalidState(t *testing.T) {
	key := newTestKey(t)
	store := newTestStore(t, key)
	states := NewInMemoryStateStore()
	cfg := &ServiceConfig{
		ClientID:      "id",
		ClientSecret:  "secret",
		RedirectURL:   "http://localhost/callback",
		EncryptionKey: string(key),
	}
	svc, err := NewService(cfg, store, states)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/callback?code=code&state=bad", nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestDisabledServiceStatus(t *testing.T) {
	svc := NewDisabledService()
	if svc.Configured() {
		t.Fatal("expected disabled service to report not configured")
	}

	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if status.Connected {
		t.Errorf("Connected = true, want false")
	}
	if status.Authenticated {
		t.Errorf("Authenticated = true, want false")
	}
	if status.Configured {
		t.Errorf("Configured = true, want false")
	}
}

func TestDisabledServiceHandlers(t *testing.T) {
	svc := NewDisabledService()

	// Status endpoint returns the not-configured payload.
	accountHandler := svc.AccountHandler()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	accountHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status status = %d, want 200", rec.Code)
	}
	var status ConnectionStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if status.Configured {
		t.Errorf("Configured = true, want false")
	}
	if status.Authenticated {
		t.Errorf("Authenticated = true, want false")
	}

	// Initiate endpoint returns service unavailable.
	authHandler := svc.Handler()
	req = httptest.NewRequest(http.MethodPost, "/initiate", nil)
	rec = httptest.NewRecorder()
	authHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("initiate status = %d, want 503", rec.Code)
	}

	// Disconnect endpoint succeeds as a no-op.
	req = httptest.NewRequest(http.MethodDelete, "/", nil)
	rec = httptest.NewRecorder()
	accountHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("disconnect status = %d, want 200", rec.Code)
	}
}

func TestStatusIncludesConfiguredAndAuthenticated(t *testing.T) {
	key := newTestKey(t)
	store := newTestStore(t, key)
	ctx := context.Background()

	cfg := &ServiceConfig{
		ClientID:      "id",
		ClientSecret:  "secret",
		RedirectURL:   "http://localhost/callback",
		EncryptionKey: string(key),
	}
	svc, err := NewService(cfg, store, NewInMemoryStateStore())
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if !status.Configured {
		t.Error("Configured = false, want true")
	}
	if status.Authenticated {
		t.Error("Authenticated = true for empty store, want false")
	}
	if status.Connected {
		t.Error("Connected = true for empty store, want false")
	}
}

func TestSecureCompare(t *testing.T) {
	if !secureCompare("abc", "abc") {
		t.Error("expected equal strings to compare true")
	}
	if secureCompare("abc", "abd") {
		t.Error("expected different strings to compare false")
	}
	if secureCompare("abc", "abcd") {
		t.Error("expected different lengths to compare false")
	}
}

func TestTokenRecordScopesPersisted(t *testing.T) {
	key := newTestKey(t)
	store := newTestStore(t, key)
	ctx := context.Background()

	cfg := &ServiceConfig{
		ClientID:      "id",
		ClientSecret:  "secret",
		RedirectURL:   "http://localhost/callback",
		EncryptionKey: string(key),
		Scopes:        DefaultScopes(),
	}
	svc, err := NewService(cfg, store, NewInMemoryStateStore())
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	record := svc.tokenToRecord(&oauth2.Token{AccessToken: "a", TokenType: "Bearer"})
	if err := store.Save(ctx, record); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(loaded.Scopes) != len(DefaultScopes()) {
		t.Fatalf("scopes = %v, want %v", loaded.Scopes, DefaultScopes())
	}
}

func TestStatusReflectsExpiry(t *testing.T) {
	key := newTestKey(t)
	store := newTestStore(t, key)
	ctx := context.Background()

	cfg := &ServiceConfig{
		ClientID:      "id",
		ClientSecret:  "secret",
		RedirectURL:   "http://localhost/callback",
		EncryptionKey: string(key),
	}
	svc, err := NewService(cfg, store, NewInMemoryStateStore())
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	// Expired token.
	expired := &TokenRecord{
		Provider:    "youtube",
		AccessToken: "expired",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(-time.Hour),
		Scopes:      DefaultScopes(),
		ConnectedAt: time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := store.Save(ctx, expired); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if status.Connected {
		t.Fatal("expected expired token to report disconnected")
	}
}

func TestActiveTokenRenewsAndPersistsBeforeReturning(t *testing.T) {
	key := newTestKey(t)
	store := newTestStore(t, key)
	ctx := context.Background()
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("refresh_token") != "refresh-old" {
			t.Errorf("refresh token = %q", r.Form.Get("refresh_token"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access-new","refresh_token":"refresh-new","token_type":"Bearer","expires_in":3600}`))
	}))
	defer server.Close()
	svc, err := NewService(&ServiceConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(key), HTTPClient: server.Client()}, store, NewInMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	svc.oauthConfig.Endpoint = oauth2.Endpoint{TokenURL: server.URL}
	if err := store.Save(ctx, &TokenRecord{Provider: "youtube", AccessToken: "access-old", RefreshToken: "refresh-old", TokenType: "Bearer", Expiry: time.Now().Add(-time.Minute), Scopes: DefaultScopes(), ConnectedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	token, err := svc.ActiveToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "access-new" {
		t.Fatalf("access token = %q", token.AccessToken)
	}
	if calls != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AccessToken != "access-new" || loaded.RefreshToken != "refresh-new" || loaded.CredentialRevision != 2 {
		t.Fatalf("renewal not committed: %+v", loaded)
	}
}

func TestActiveTokenForcesRenewalForRejectedUnexpiredAccess(t *testing.T) {
	key := newTestKey(t)
	store := newTestStore(t, key)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"renewed","refresh_token":"refresh","token_type":"Bearer","expires_in":3600}`))
	}))
	defer server.Close()
	svc, err := NewService(&ServiceConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(key), HTTPClient: server.Client()}, store, NewInMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	svc.oauthConfig.Endpoint = oauth2.Endpoint{TokenURL: server.URL}
	ctx := context.Background()
	if err := store.Save(ctx, &TokenRecord{Provider: "youtube", AccessToken: "rejected", RefreshToken: "refresh", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour), Scopes: DefaultScopes(), ConnectedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	record, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRenewalFailure(ctx, CredentialVersion{record.AuthorizationID, record.CredentialRevision}, "access_rejected"); err != nil {
		t.Fatal(err)
	}
	token, err := svc.ActiveToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "renewed" || calls != 1 {
		t.Fatalf("token=%q refresh calls=%d, want renewed and one exchange", token.AccessToken, calls)
	}
}

func TestStatusAndActiveTokenShareValidityBoundary(t *testing.T) {
	key := newTestKey(t)
	store := newTestStore(t, key)
	svc, err := NewService(&ServiceConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(key)}, store, NewInMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	baseNow := time.Now()
	now = func() time.Time { return baseNow }
	t.Cleanup(func() { now = time.Now })
	for _, tc := range []struct {
		name   string
		expiry time.Time
		usable bool
	}{
		{name: "zero expiry", usable: false},
		{name: "inside ten second margin", expiry: baseNow.Add(9 * time.Second), usable: false},
		{name: "at ten second margin", expiry: baseNow.Add(10 * time.Second), usable: false},
		{name: "outside ten second margin", expiry: baseNow.Add(11 * time.Second), usable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.Save(context.Background(), &TokenRecord{Provider: "youtube", AccessToken: "access", TokenType: "Bearer", Expiry: tc.expiry, Scopes: DefaultScopes(), ConnectedAt: baseNow, UpdatedAt: baseNow}); err != nil {
				t.Fatal(err)
			}
			status, err := svc.Status(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if status.Connected != tc.usable {
				t.Fatalf("status connected=%v want %v", status.Connected, tc.usable)
			}
			// Without a refresh token, unusable credentials must not be returned.
			token, err := svc.ActiveToken(context.Background())
			if tc.usable {
				if err != nil || token == nil {
					t.Fatalf("ActiveToken=(%v,%v), want token", token, err)
				}
			} else if err == nil || token != nil {
				t.Fatalf("ActiveToken=(%v,%v), want unavailable", token, err)
			}
		})
	}
}

func TestRefreshScopeValidationRejectsBroadening(t *testing.T) {
	token := &oauth2.Token{AccessToken: "access", TokenType: "Bearer"}
	token = token.WithExtra(map[string]any{"scope": ScopeYouTubeReadonly + " https://www.googleapis.com/auth/youtube"})
	if !refreshScopesCompatible(token, []string{ScopeYouTubeReadonly, ScopeYouTube}) {
		t.Fatal("same returned scopes should be accepted")
	}
	token = token.WithExtra(map[string]any{"scope": ScopeYouTubeReadonly + " https://www.googleapis.com/auth/youtube https://www.googleapis.com/auth/drive"})
	if refreshScopesCompatible(token, []string{ScopeYouTubeReadonly, ScopeYouTube}) {
		t.Fatal("broader returned scopes must be rejected")
	}
}

func TestActiveTokenPersistsSafeRefreshRejection(t *testing.T) {
	key := newTestKey(t)
	store := newTestStore(t, key)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"PRIVATE_PROVIDER_DETAIL"}`))
	}))
	defer server.Close()
	svc, err := NewService(&ServiceConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(key), HTTPClient: server.Client()}, store, NewInMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	svc.oauthConfig.Endpoint = oauth2.Endpoint{TokenURL: server.URL}
	ctx := context.Background()
	if err := store.Save(ctx, &TokenRecord{Provider: "youtube", AccessToken: "old", RefreshToken: "refresh", TokenType: "Bearer", Expiry: time.Now().Add(-time.Minute), Scopes: DefaultScopes(), ConnectedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ActiveToken(ctx); err == nil || strings.Contains(err.Error(), "PRIVATE_PROVIDER_DETAIL") {
		t.Fatalf("expected safe error, got %v", err)
	}
	record, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if record.RenewalFailure != "refresh_rejected" {
		t.Fatalf("failure = %q", record.RenewalFailure)
	}
	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.AuthorizationState != "reconnect_required" || status.AuthorizationReason == nil || *status.AuthorizationReason != "refresh_rejected" {
		t.Fatalf("status = %+v", status)
	}
}

func TestRefreshRejectionSurvivesLaterTemporaryFailure(t *testing.T) {
	key := newTestKey(t)
	store := newTestStore(t, key)
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer server.Close()
	svc, err := NewService(&ServiceConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(key), HTTPClient: server.Client()}, store, NewInMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	svc.oauthConfig.Endpoint = oauth2.Endpoint{TokenURL: server.URL}
	if err := store.Save(ctx, &TokenRecord{Provider: "youtube", AccessToken: "old", RefreshToken: "refresh", TokenType: "Bearer", Expiry: time.Now().Add(-time.Minute), Scopes: DefaultScopes(), ConnectedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ActiveToken(ctx); err == nil {
		t.Fatal("expected rejected refresh")
	}
	current, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	version := CredentialVersion{current.AuthorizationID, current.CredentialRevision}
	if err := store.SaveRenewalFailure(ctx, version, "temporary_network"); err != nil {
		t.Fatal(err)
	}
	current, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.RenewalFailure != "refresh_rejected" {
		t.Fatalf("failure=%q", current.RenewalFailure)
	}
	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.AuthorizationState != "reconnect_required" || status.AuthorizationReason == nil || *status.AuthorizationReason != "refresh_rejected" {
		t.Fatalf("status=%+v", status)
	}
}

type delayedFirstTokenLoad struct {
	*SQLiteTokenStore
	loaded  chan struct{}
	release chan struct{}
}

func (s *delayedFirstTokenLoad) Load(ctx context.Context) (*TokenRecord, error) {
	record, err := s.SQLiteTokenStore.Load(ctx)
	select {
	case <-s.loaded:
	default:
		close(s.loaded)
		<-s.release
	}
	return record, err
}

func TestActiveTokenDoesNotExchangeSupersededGrantAfterStaleLoad(t *testing.T) {
	ctx := context.Background()
	key := newTestKey(t)
	base := newTestStore(t, key)
	if err := base.Save(ctx, &TokenRecord{Provider: "youtube", AccessToken: "old", RefreshToken: "old-refresh", TokenType: "Bearer", Expiry: time.Now().Add(-time.Minute), Scopes: DefaultScopes(), ConnectedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	store := &delayedFirstTokenLoad{SQLiteTokenStore: base, loaded: make(chan struct{}), release: make(chan struct{})}
	var exchanges atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exchanges.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new","refresh_token":"rotated","token_type":"Bearer","expires_in":3600}`))
	}))
	defer server.Close()
	svc, err := NewService(&ServiceConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(key), HTTPClient: server.Client()}, store, NewInMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	svc.oauthConfig.Endpoint = oauth2.Endpoint{TokenURL: server.URL}
	firstDone := make(chan error, 1)
	go func() { _, err := svc.ActiveToken(ctx); firstDone <- err }()
	<-store.loaded
	if _, err := svc.ActiveToken(ctx); err != nil {
		t.Fatal(err)
	}
	close(store.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("stale reader should use current durable access without another exchange: %v", err)
	}
	if got := exchanges.Load(); got != 1 {
		t.Fatalf("token exchanges = %d, want one exchange for the current grant", got)
	}
}

type failRefreshSaveStore struct {
	*SQLiteTokenStore
	fail bool
}

func (s *failRefreshSaveStore) SaveRefresh(ctx context.Context, version CredentialVersion, record *TokenRecord) (CredentialVersion, error) {
	if s.fail {
		return CredentialVersion{}, errors.New("injected save failure")
	}
	return s.SQLiteTokenStore.SaveRefresh(ctx, version, record)
}

func TestInitiateOAuthPreservesPendingRotatedRenewal(t *testing.T) {
	ctx := context.Background()
	key := newTestKey(t)
	base := newTestStore(t, key)
	if err := base.Save(ctx, &TokenRecord{Provider: "youtube", AccessToken: "old", RefreshToken: "old-refresh", TokenType: "Bearer", Expiry: time.Now().Add(-time.Minute), Scopes: DefaultScopes(), ConnectedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	store := &failRefreshSaveStore{SQLiteTokenStore: base, fail: true}
	var exchanges atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exchanges.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new","refresh_token":"rotated-refresh","token_type":"Bearer","expires_in":3600}`))
	}))
	defer server.Close()
	svc, err := NewService(&ServiceConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(key), HTTPClient: server.Client()}, store, NewInMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	svc.oauthConfig.Endpoint = oauth2.Endpoint{TokenURL: server.URL}
	if _, err := svc.ActiveToken(ctx); !errors.Is(err, ErrRenewalPersistence) {
		t.Fatalf("initial renewal error = %v, want persistence failure", err)
	}
	if svc.pending == nil {
		t.Fatal("failed save did not retain private rotated candidate")
	}
	if _, err := svc.InitiateOAuth(ctx); err != nil {
		t.Fatal(err)
	}
	store.fail = false
	if token, err := svc.ActiveToken(ctx); err != nil || token.AccessToken != "new" {
		t.Fatalf("retry pending renewal = (%v, %v), want new access token", token, err)
	}
	current, err := base.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.RefreshToken != "rotated-refresh" || current.CredentialRevision != 2 || exchanges.Load() != 1 {
		t.Fatalf("pending replacement lost or re-exchanged: revision=%d refresh=%q exchanges=%d", current.CredentialRevision, current.RefreshToken, exchanges.Load())
	}
}

func TestSaveRenewalFailureDoesNotModifyReplacement(t *testing.T) {
	store := newTestStore(t, newTestKey(t))
	ctx := context.Background()
	if err := store.Save(ctx, &TokenRecord{Provider: "youtube", AccessToken: "one", RefreshToken: "refresh", TokenType: "Bearer", Expiry: time.Now(), Scopes: DefaultScopes(), ConnectedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	old, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	version := CredentialVersion{old.AuthorizationID, old.CredentialRevision}
	if _, err := store.ReplaceAuthorization(ctx, &version, &TokenRecord{Provider: "youtube", AccessToken: "replacement", RefreshToken: "new-refresh", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour), Scopes: DefaultScopes(), ConnectedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRenewalFailure(ctx, version, "refresh_rejected"); !errors.Is(err, ErrAuthorizationChanged) {
		t.Fatalf("stale failure write error = %v", err)
	}
	current, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.AccessToken != "replacement" || current.RenewalFailure != "" || current.AccessRejected {
		t.Fatalf("replacement changed: %+v", current)
	}
}

// hashForAudit is a test-only helper illustrating how an audit record might
// pseudonymise a user ID. It is not production code.
func hashForAudit(userID string, pepper []byte) string {
	h := sha256.New()
	h.Write([]byte(userID))
	h.Write(pepper)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// x/oauth2 flattens transport errors with %v, so cancellation during a real
// refresh is only visible on the caller's context. It must not be classified,
// persisted, or remembered as a renewal failure.
func TestActiveTokenCancelledDuringRefreshLeavesNoFailureAndNextCallRenews(t *testing.T) {
	ctx := context.Background()
	key := newTestKey(t)
	store := newTestStore(t, key)
	if err := store.Save(ctx, &TokenRecord{Provider: "youtube", AccessToken: "old", RefreshToken: "old-refresh", TokenType: "Bearer", Expiry: time.Now().Add(-time.Minute), Scopes: DefaultScopes(), ConnectedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	held := make(chan struct{}, 1)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if hits.Add(1) == 1 {
			held <- struct{}{}
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new","token_type":"Bearer","expires_in":3600}`))
	}))
	defer server.Close()
	svc, err := NewService(&ServiceConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(key), HTTPClient: &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}}, store, NewInMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	svc.oauthConfig.Endpoint = oauth2.Endpoint{TokenURL: server.URL}

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := svc.ActiveToken(cctx); done <- err }()
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh never reached the server")
	}
	cancel()
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ActiveToken did not return after cancellation")
	}
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrRenewalPersistence) {
		t.Fatalf("error = %v, want context.Canceled and not a persistence failure", err)
	}
	if svc.knownFailure != nil {
		t.Fatalf("cancellation set a sticky failure: %+v", svc.knownFailure)
	}
	if record, err := store.Load(ctx); err != nil || record.RenewalFailure != "" {
		t.Fatalf("cancellation persisted a renewal failure: %v / %v", record, err)
	}
	token, err := svc.ActiveToken(ctx)
	if err != nil || token.AccessToken != "new" {
		t.Fatalf("next call = (%v, %v), want renewed token", token, err)
	}
	if status, err := svc.Status(ctx); err != nil || status.AuthorizationState != "usable" {
		t.Fatalf("status = (%v, %v), want usable", status, err)
	}
}

// failingStore injects renewal persistence failures; both are switchable.
type failingStore struct {
	*SQLiteTokenStore
	failRefresh, failRenewalFailure atomic.Bool
}

func (s *failingStore) SaveRefresh(ctx context.Context, v CredentialVersion, r *TokenRecord) (CredentialVersion, error) {
	if s.failRefresh.Load() {
		return CredentialVersion{}, errors.New("injected save failure")
	}
	return s.SQLiteTokenStore.SaveRefresh(ctx, v, r)
}

func (s *failingStore) SaveRenewalFailure(ctx context.Context, v CredentialVersion, reason string) error {
	if s.failRenewalFailure.Load() {
		return errors.New("injected failure-record failure")
	}
	return s.SQLiteTokenStore.SaveRenewalFailure(ctx, v, reason)
}

func newExpiredRotatingService(t *testing.T, store ConditionalTokenStore, key []byte, exchanges *atomic.Int32, onExchange func()) *Service {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		exchanges.Add(1)
		if onExchange != nil {
			onExchange()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new","refresh_token":"rotated-refresh","token_type":"Bearer","expires_in":3600}`))
	}))
	t.Cleanup(server.Close)
	svc, err := NewService(&ServiceConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(key), HTTPClient: server.Client()}, store, NewInMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	svc.oauthConfig.Endpoint = oauth2.Endpoint{TokenURL: server.URL}
	return svc
}

func seedExpired(t *testing.T, store *SQLiteTokenStore) {
	t.Helper()
	if err := store.Save(context.Background(), &TokenRecord{Provider: "youtube", AccessToken: "old", RefreshToken: "old-refresh", TokenType: "Bearer", Expiry: time.Now().Add(-time.Minute), Scopes: DefaultScopes(), ConnectedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

// Cancellation while SaveRefresh waits on a held database connection must keep
// the rotated candidate, record no failure, and let the next call persist it
// without a second exchange (ARS-001).
func TestActiveTokenCancelledDuringSaveRefreshRetainsCandidateAndRecovers(t *testing.T) {
	ctx := context.Background()
	key := newTestKey(t)
	store := newTestStore(t, key)
	store.db.SetMaxOpenConns(1)
	seedExpired(t, store)

	var exchanges atomic.Int32
	atExchange := make(chan struct{}, 1)
	proceed := make(chan struct{})
	svc := newExpiredRotatingService(t, store, key, &exchanges, func() {
		atExchange <- struct{}{}
		<-proceed
	})

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := svc.ActiveToken(cctx); done <- err }()
	select {
	case <-atExchange:
	case <-time.After(10 * time.Second):
		t.Fatal("exchange never started")
	}
	// Hold the only connection so the post-exchange save must wait for it.
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waits := store.db.Stats().WaitCount
	close(proceed)
	deadline := time.Now().Add(10 * time.Second)
	for store.db.Stats().WaitCount == waits {
		if time.Now().After(deadline) {
			t.Fatal("SaveRefresh never waited on the held connection")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ActiveToken did not return after cancellation")
	}
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrRenewalPersistence) {
		t.Fatalf("error = %v, want context.Canceled and not persistence failure", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if svc.knownFailure != nil {
		t.Fatalf("cancellation recorded a sticky failure: %+v", svc.knownFailure)
	}
	if svc.pending == nil {
		t.Fatal("rotated candidate was lost")
	}

	token, err := svc.ActiveToken(ctx)
	if err != nil || token.AccessToken != "new" {
		t.Fatalf("next call = (%v, %v), want persisted candidate", token, err)
	}
	if got := exchanges.Load(); got != 1 {
		t.Fatalf("exchanges = %d, want 1 (pending candidate retried, no new exchange)", got)
	}
	record, err := store.Load(ctx)
	if err != nil || record.RefreshToken != "rotated-refresh" || record.RenewalFailure != "" {
		t.Fatalf("stored record = %+v / %v", record, err)
	}
	if status, err := svc.Status(ctx); err != nil || status.AuthorizationState != "usable" {
		t.Fatalf("status = (%v, %v), want usable", status, err)
	}
}

// A genuine save failure, including a failure to record it (the sticky
// in-memory state), heals on its own once the store recovers.
func TestActiveTokenGenuineSaveFailureRecoversWithoutRestart(t *testing.T) {
	ctx := context.Background()
	key := newTestKey(t)
	store := &failingStore{SQLiteTokenStore: newTestStore(t, key)}
	seedExpired(t, store.SQLiteTokenStore)
	store.failRefresh.Store(true)
	store.failRenewalFailure.Store(true)
	var exchanges atomic.Int32
	svc := newExpiredRotatingService(t, store, key, &exchanges, nil)

	if _, err := svc.ActiveToken(ctx); !errors.Is(err, ErrRenewalPersistence) {
		t.Fatalf("first call = %v, want persistence failure", err)
	}
	if svc.knownFailure == nil || svc.knownFailure.reason != "persistence_failed" || svc.pending == nil {
		t.Fatalf("expected sticky persistence failure with pending candidate: %+v", svc.knownFailure)
	}
	// Still failing: stays a persistence failure and does not re-exchange.
	if _, err := svc.ActiveToken(ctx); !errors.Is(err, ErrRenewalPersistence) {
		t.Fatalf("second call = %v, want persistence failure", err)
	}
	store.failRefresh.Store(false)
	store.failRenewalFailure.Store(false)
	token, err := svc.ActiveToken(ctx)
	if err != nil || token.AccessToken != "new" {
		t.Fatalf("recovered call = (%v, %v)", token, err)
	}
	if got := exchanges.Load(); got != 1 {
		t.Fatalf("exchanges = %d, want 1", got)
	}
	if svc.knownFailure != nil || svc.pending != nil {
		t.Fatalf("state not cleared: %+v / %+v", svc.knownFailure, svc.pending)
	}
	if status, err := svc.Status(ctx); err != nil || status.AuthorizationState != "usable" {
		t.Fatalf("status = (%v, %v), want usable", status, err)
	}
}
