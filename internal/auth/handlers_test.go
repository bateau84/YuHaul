package auth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/oauth2"
)

func TestAuthHandler_Status(t *testing.T) {
	svc := NewDisabledService()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()

	svc.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"connected":false`)
}

func TestOAuthCallbackProviderErrorIsRedacted(t *testing.T) {
	marker := "private-description-7b1f https://identity.example/private/path?token=marker"
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"` + marker + `"}`))
	}))
	defer tokenServer.Close()
	store := newTestStore(t, newTestKey(t))
	svc, err := NewService(&ServiceConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(newTestKey(t)), HTTPClient: tokenServer.Client()}, store, NewInMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	svc.oauthConfig.Endpoint = oauth2.Endpoint{AuthURL: "https://accounts.example/auth", TokenURL: tokenServer.URL}
	init, err := svc.InitiateOAuth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	req := httptest.NewRequest(http.MethodGet, "/callback?code=code&state="+init.State, nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	for name, got := range map[string]string{"response": rec.Body.String(), "logs": logs.String()} {
		if strings.Contains(got, marker) || strings.Contains(got, "identity.example") || strings.Contains(got, "private-description") {
			t.Errorf("%s disclosed provider-controlled diagnostic: %s", name, got)
		}
	}
	assert.Contains(t, rec.Body.String(), "authorization callback failed")
	assert.Contains(t, logs.String(), "code=oauth_error status=400 reason=invalid_grant")
}

func TestAuthHandlerCustodyErrorsDoNotLogRawChains(t *testing.T) {
	marker := "PRIVATE_CUSTODY_CHAIN_98d1"
	store := failingStatusLoadStore{SQLiteTokenStore: newTestStore(t, newTestKey(t)), err: errors.New(marker)}
	svc, err := NewService(&ServiceConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(newTestKey(t))}, store, NewInMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	rec := httptest.NewRecorder()
	svc.AccountHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", rec.Code)
	}
	if strings.Contains(logs.String(), marker) {
		t.Fatalf("raw error logged: %s", logs.String())
	}
}

type failingStatusLoadStore struct {
	*SQLiteTokenStore
	err error
}

func (s failingStatusLoadStore) Load(context.Context) (*TokenRecord, error) { return nil, s.err }

func TestAccountHandler_Root(t *testing.T) {
	svc := NewDisabledService()

	for _, path := range []string{"/", "/status"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			svc.AccountHandler().ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Contains(t, rec.Body.String(), `"connected":false`)
		})
	}
}

func TestAccountHandler_Disconnect(t *testing.T) {
	svc := NewDisabledService()
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	rec := httptest.NewRecorder()

	svc.AccountHandler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"disconnected":true`)
}
