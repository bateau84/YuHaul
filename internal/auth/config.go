// Package auth implements the YouTube OAuth2 connection flow and encrypted
// token storage for the single-user YuHaul system.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// Required OAuth2 scopes for YuHaul. The read-only scope is used to list
// subscriptions and retrieve video metadata; the full YouTube scope is
// required to insert videos into playlists.
const (
	ScopeYouTubeReadonly = "https://www.googleapis.com/auth/youtube.readonly"
	ScopeYouTube         = "https://www.googleapis.com/auth/youtube"
)

// DefaultScopes returns the minimal scope set needed for v1 functionality.
func DefaultScopes() []string {
	return []string{ScopeYouTubeReadonly, ScopeYouTube}
}

// ServiceConfig holds the runtime configuration for the auth subsystem.
type ServiceConfig struct {
	// ClientID and ClientSecret are the OAuth2 client credentials issued by
	// the Google Cloud Console.
	ClientID     string
	ClientSecret string

	// RedirectURL is the absolute URL that Google will redirect to after
	// authorization. It must match a registered redirect URI exactly.
	RedirectURL string

	// EncryptionKey is a 32-byte key used for AES-256-GCM token encryption.
	// It can be provided as a raw 32-byte string or base64-encoded.
	EncryptionKey string

	// Scopes overrides the default OAuth2 scopes. Leave nil for DefaultScopes.
	Scopes []string

	// HTTPClient is used for OAuth2 token exchange and revocation. If nil,
	// [http.DefaultClient] is used.
	HTTPClient *http.Client
}

// Validate checks that the configuration is sufficient to start the service.
func (c *ServiceConfig) Validate() error {
	if c.ClientID == "" {
		return errors.New("auth: ClientID is required")
	}
	if c.ClientSecret == "" {
		return errors.New("auth: ClientSecret is required")
	}
	if c.RedirectURL == "" {
		return errors.New("auth: RedirectURL is required")
	}
	if _, err := decodeEncryptionKey(c.EncryptionKey); err != nil {
		return fmt.Errorf("auth: EncryptionKey is invalid: %w", err)
	}
	return nil
}

// Service coordinates OAuth2 flow, token encryption, and credential storage.
type Service struct {
	config      *ServiceConfig
	oauthConfig *oauth2.Config
	encKey      []byte
	store       TokenStore
	states      StateStore
}

// NewService constructs an auth Service. It validates config, derives the
// encryption key, and opens the configured token store.
func NewService(cfg *ServiceConfig, store TokenStore, states StateStore) (*Service, error) {
	if cfg == nil {
		return nil, errors.New("auth: ServiceConfig is nil")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, errors.New("auth: TokenStore is required")
	}
	if states == nil {
		return nil, errors.New("auth: StateStore is required")
	}

	encKey, err := decodeEncryptionKey(cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}

	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = DefaultScopes()
	}

	oauthCfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     google.Endpoint,
		RedirectURL:  cfg.RedirectURL,
		Scopes:       scopes,
	}

	return &Service{
		config:      cfg,
		oauthConfig: oauthCfg,
		encKey:      encKey,
		store:       store,
		states:      states,
	}, nil
}

// decodeEncryptionKey accepts a raw 32-byte key or a base64-encoded key.
func decodeEncryptionKey(key string) ([]byte, error) {
	if len(key) == 32 {
		return []byte(key), nil
	}
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return nil, fmt.Errorf("encryption key must be 32 raw bytes or base64: %w", err)
	}
	if len(decoded) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(decoded))
	}
	return decoded, nil
}

// generateState returns a URL-safe random string suitable for the OAuth2 state
// parameter and CSRF protection.
func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: failed to generate state: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// contextClient returns the HTTP client configured for OAuth2 exchange.
func (s *Service) contextClient(ctx context.Context) *http.Client {
	if s.config.HTTPClient != nil {
		return s.config.HTTPClient
	}
	return http.DefaultClient
}

// tokenContext returns a context bound to the service's HTTP client for OAuth2
// exchange operations.
func (s *Service) tokenContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, s.contextClient(ctx))
}

// now is a test seam for the current time.
var now = time.Now

// TokenStore is satisfied by implementations that persist encrypted OAuth2
// tokens. The service encrypts tokens before passing them to the store.
type TokenStore interface {
	Save(ctx context.Context, record *TokenRecord) error
	Load(ctx context.Context) (*TokenRecord, error)
	Delete(ctx context.Context) error
}

// StateStore persists and validates OAuth2 state parameters.
type StateStore interface {
	// Save stores state with an expiration time.
	Save(ctx context.Context, state string, expiresAt time.Time) error
	// Validate returns true if state exists and has not expired, then removes it.
	Validate(ctx context.Context, state string) (bool, error)
}

// TokenRecord is the durable representation of an OAuth2 token.
type TokenRecord struct {
	Provider       string
	AccessToken    string
	RefreshToken   string
	TokenType      string
	Expiry         time.Time
	Scopes         []string
	ConnectedAt    time.Time
	UpdatedAt      time.Time
	DisconnectedAt *time.Time
}

// ConnectionStatus describes whether the service has usable credentials.
type ConnectionStatus struct {
	Connected   bool      `json:"connected"`
	Provider    string    `json:"provider,omitempty"`
	Scopes      []string  `json:"scopes,omitempty"`
	ConnectedAt time.Time `json:"connected_at,omitempty"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
}
