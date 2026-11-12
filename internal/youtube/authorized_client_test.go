package youtube_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/store/storetest"
	"github.com/bateau84/YuHaul/internal/youtube"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestGuardedAuthRejectionRenewsForNextRequestWithoutMutationReplay(t *testing.T) {
	ctx := context.Background()
	db := storetest.Open(t)
	key := []byte("01234567890123456789012345678901")
	store, err := auth.NewSQLiteTokenStore(db, key)
	require.NoError(t, err)
	tokenCalls, postCalls, getCalls := 0, 0, 0
	tokenHTTP := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		tokenCalls++
		if req.URL.Host != "oauth2.googleapis.com" {
			return nil, errors.New("unexpected OAuth endpoint")
		}
		return response(http.StatusOK, `{"access_token":"fresh","token_type":"Bearer","expires_in":3600}`), nil
	})}
	service, err := auth.NewService(&auth.ServiceConfig{ClientID: "client", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(key), HTTPClient: tokenHTTP}, store, auth.NewInMemoryStateStore())
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "stale", RefreshToken: "refresh", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour), Scopes: auth.DefaultScopes()}))
	binding, err := service.Bind(ctx)
	require.NoError(t, err)
	data := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			postCalls++
			if r.Header.Get("Authorization") != "Bearer stale" {
				t.Errorf("mutation auth=%q", r.Header.Get("Authorization"))
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{}`)
		case http.MethodGet:
			getCalls++
			if r.Header.Get("Authorization") != "Bearer fresh" {
				t.Errorf("read auth=%q", r.Header.Get("Authorization"))
			}
			_, _ = io.WriteString(w, `{"items":[]}`)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer data.Close()
	client := youtube.NewAuthorizedClient(ctx, func(ctx context.Context) (*oauth2.Token, error) { return service.Resolve(ctx, binding) }, youtube.WithBaseURL(data.URL), youtube.WithHTTPClient(data.Client()))
	claim := youtube.PlaylistClaim{RunID: "1", VideoID: "2", RouteDecisionID: "3", ActionType: "playlist_insert", TargetPlaylistID: "playlist", AuthorizationID: mustBindingIdentity(t, binding)}
	result := client.InsertGuardedPlaylistItem(ctx, claim, "external", func(ctx context.Context, req *http.Request, transport http.RoundTripper) (*http.Response, youtube.DispatchAdmission, bool, error) {
		resp, receipt, err := service.Dispatch(ctx, binding, req, transport)
		return resp, youtube.DispatchAdmission{AuthorizationID: receipt.AuthorizationID, CredentialRevision: receipt.CredentialRevision, AdmittedAt: receipt.AdmittedAt}, err != nil && receipt.AuthorizationID == "", err
	}, nil)
	require.Equal(t, youtube.MutationUncertain, result.Application)
	require.True(t, result.AccessRejected)
	require.NotNil(t, result.Admission)
	admitted := result.Admission
	require.NoError(t, service.ObserveAccessRejection(ctx, binding, auth.AdmissionReceipt{AuthorizationID: admitted.AuthorizationID, CredentialRevision: admitted.CredentialRevision, AdmittedAt: admitted.AdmittedAt}))
	_, err = client.ListSubscriptions(ctx, "")
	require.NoError(t, err)
	require.Equal(t, 1, postCalls, "the ambiguous mutation must never be replayed")
	require.Equal(t, 1, tokenCalls)
	require.Equal(t, 1, getCalls)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(body)), Request: &http.Request{}}
}
func mustBindingIdentity(t *testing.T, b auth.AuthorizationBinding) string {
	t.Helper()
	id, err := auth.BindingIdentity(b)
	require.NoError(t, err)
	return id
}

func TestAuthorizedClientUsesBoundResolutionForEachRequest(t *testing.T) {
	ctx := context.Background()
	db := storetest.Open(t)
	key := []byte("01234567890123456789012345678901")
	store, err := auth.NewSQLiteTokenStore(db, key)
	require.NoError(t, err)
	service, err := auth.NewService(&auth.ServiceConfig{ClientID: "client", ClientSecret: "secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(key)}, store, auth.NewInMemoryStateStore())
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "first", RefreshToken: "refresh", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour), Scopes: auth.DefaultScopes()}))
	binding, err := service.Bind(ctx)
	require.NoError(t, err)
	var observed []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed = append(observed, r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{"items":[]}`)
	}))
	defer srv.Close()
	client := youtube.NewAuthorizedClient(ctx, func(ctx context.Context) (*oauth2.Token, error) {
		return service.Resolve(ctx, binding)
	}, youtube.WithBaseURL(srv.URL), youtube.WithHTTPClient(srv.Client()))
	_, err = client.ListSubscriptions(ctx, "")
	require.NoError(t, err)
	// Replacing the stored connection must be rejected before transport.
	require.NoError(t, store.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "second", RefreshToken: "refresh", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour), Scopes: auth.DefaultScopes()}))
	_, err = client.ListSubscriptions(ctx, "")
	require.ErrorIs(t, err, auth.ErrAuthorizationChanged)
	require.Equal(t, []string{"Bearer first"}, observed)
}
