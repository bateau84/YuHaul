package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	_ "modernc.org/sqlite"
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
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
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

// hashForAudit is a test-only helper illustrating how an audit record might
// pseudonymise a user ID. It is not production code.
func hashForAudit(userID string, pepper []byte) string {
	h := sha256.New()
	h.Write([]byte(userID))
	h.Write(pepper)
	return fmt.Sprintf("%x", h.Sum(nil))
}
