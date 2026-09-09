package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// OAuthInitiateResponse is returned by POST /api/auth/initiate.
type OAuthInitiateResponse struct {
	AuthURL string `json:"auth_url"`
	State   string `json:"state"`
}

// OAuthCallbackResult is returned by GET /api/auth/callback on success.
type OAuthCallbackResult struct {
	Connected bool     `json:"connected"`
	Provider  string   `json:"provider"`
	Scopes    []string `json:"scopes"`
}

// InitiateOAuth begins the YouTube OAuth2 flow and returns the authorization
// URL the caller should redirect the user to.
func (s *Service) InitiateOAuth(ctx context.Context) (*OAuthInitiateResponse, error) {
	if !s.configured {
		return nil, ErrNotConfigured
	}
	state, err := generateState()
	if err != nil {
		return nil, err
	}

	// State lifetime matches common OAuth2 recommendation: short-lived,
	// single-use, bound to the initiation request.
	if err := s.states.Save(ctx, state, now().Add(10*time.Minute)); err != nil {
		return nil, fmt.Errorf("auth: failed to save state: %w", err)
	}

	authURL := s.oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	return &OAuthInitiateResponse{AuthURL: authURL, State: state}, nil
}

// HandleOAuthCallback processes the authorization code returned by Google.
// It validates the state parameter, exchanges the code for tokens, and stores
// the encrypted credentials.
func (s *Service) HandleOAuthCallback(ctx context.Context, code, state string) (*OAuthCallbackResult, error) {
	if !s.configured {
		return nil, ErrNotConfigured
	}
	if strings.TrimSpace(code) == "" {
		return nil, errors.New("auth: authorization code is required")
	}
	if strings.TrimSpace(state) == "" {
		return nil, errors.New("auth: state is required")
	}

	valid, err := s.states.Validate(ctx, state)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to validate state: %w", err)
	}
	if !valid {
		return nil, errors.New("auth: invalid or expired state parameter")
	}

	tokCtx := s.tokenContext(ctx)
	token, err := s.oauthConfig.Exchange(tokCtx, code)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to exchange authorization code: %w", err)
	}

	record := s.tokenToRecord(token)
	if err := s.store.Save(ctx, record); err != nil {
		return nil, fmt.Errorf("auth: failed to save credentials: %w", err)
	}

	return &OAuthCallbackResult{
		Connected: true,
		Provider:  record.Provider,
		Scopes:    record.Scopes,
	}, nil
}

// Status returns the current connection state without exposing tokens.
func (s *Service) Status(ctx context.Context) (*ConnectionStatus, error) {
	if !s.configured {
		return &ConnectionStatus{
			Connected:     false,
			Authenticated: false,
			Configured:    false,
		}, nil
	}
	record, err := s.store.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to load connection status: %w", err)
	}
	if record == nil {
		return &ConnectionStatus{
			Connected:     false,
			Authenticated: false,
			Configured:    true,
		}, nil
	}

	connected := !isExpired(record.Expiry)
	return &ConnectionStatus{
		Connected:     connected,
		Authenticated: connected,
		Configured:    true,
		Provider:      record.Provider,
		Scopes:        record.Scopes,
		ConnectedAt:   record.ConnectedAt,
		ExpiresAt:     record.Expiry,
	}, nil
}

// Disconnect revokes the stored access token with Google (best-effort), then
// removes all stored credentials. Routing data, statistics, and configuration
// are not touched by this method.
func (s *Service) Disconnect(ctx context.Context) error {
	if !s.configured {
		return nil
	}
	record, err := s.store.Load(ctx)
	if err != nil {
		return fmt.Errorf("auth: failed to load credentials for disconnect: %w", err)
	}
	if record == nil {
		return nil
	}

	// Best-effort revocation per RFC 7009. Failure here does not block local
	// credential deletion; the token is also removed from storage.
	if err := s.revokeToken(ctx, record.AccessToken); err != nil {
		// We intentionally do not return this error: local deletion is the
		// authoritative disconnection for a single-user self-hosted tool.
		// A failed revocation leaves a short-lived token in Google's cache.
	}

	if err := s.store.Delete(ctx); err != nil {
		return fmt.Errorf("auth: failed to remove credentials: %w", err)
	}
	return nil
}

// revokeToken calls Google's OAuth2 revocation endpoint.
func (s *Service) revokeToken(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	form := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/revoke", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := s.contextClient(ctx)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("revoke returned status %d", resp.StatusCode)
	}
	return nil
}

// ActiveToken returns the current OAuth2 token if the account is connected and
// the token is not expired. Callers should refresh using the refresh token if
// expired. This is intended for downstream YouTube API clients.
func (s *Service) ActiveToken(ctx context.Context) (*oauth2.Token, error) {
	if !s.configured {
		return nil, ErrNotConfigured
	}
	record, err := s.store.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to load token: %w", err)
	}
	if record == nil {
		return nil, errors.New("auth: no connected account")
	}
	return recordToToken(record), nil
}

// tokenToRecord converts an oauth2.Token into our durable record format.
func (s *Service) tokenToRecord(token *oauth2.Token) *TokenRecord {
	t := now()
	return &TokenRecord{
		Provider:    "youtube",
		AccessToken: token.AccessToken,
		RefreshToken: func() string {
			if token.RefreshToken != "" {
				return token.RefreshToken
			}
			return ""
		}(),
		TokenType:   token.TokenType,
		Expiry:      token.Expiry,
		Scopes:      append([]string(nil), s.oauthConfig.Scopes...),
		ConnectedAt: t,
		UpdatedAt:   t,
	}
}

// recordToToken converts a TokenRecord back to an oauth2.Token.
func recordToToken(record *TokenRecord) *oauth2.Token {
	return &oauth2.Token{
		AccessToken:  record.AccessToken,
		RefreshToken: record.RefreshToken,
		TokenType:    record.TokenType,
		Expiry:       record.Expiry,
	}
}

func isExpired(expiry time.Time) bool {
	if expiry.IsZero() {
		return false // no expiry means no expiration
	}
	return now().After(expiry)
}

// writeJSON writes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Headers already sent; nothing useful to do here except log.
		_ = err
	}
}

// writeError writes a structured error response.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
