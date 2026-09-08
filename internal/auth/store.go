package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// SQLiteTokenStore persists encrypted OAuth2 tokens in a SQLite database. The
// single-user design stores at most one active token row (id = 1).
//
// The store is driver-agnostic: the caller opens the *sql.DB using the SQLite
// driver chosen for the project (e.g. mattn/go-sqlite3 or modernc.org/sqlite).
type SQLiteTokenStore struct {
	db  *sql.DB
	key []byte
}

// NewSQLiteTokenStore opens a SQLite-backed token store and creates the
// auth_tokens table if it does not exist. The encryption key must be 32 bytes.
func NewSQLiteTokenStore(db *sql.DB, key []byte) (*SQLiteTokenStore, error) {
	if db == nil {
		return nil, errors.New("auth: db is required")
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("auth: encryption key must be 32 bytes, got %d", len(key))
	}
	s := &SQLiteTokenStore{db: db, key: key}
	if err := s.migrate(context.Background()); err != nil {
		return nil, fmt.Errorf("auth: token store migration failed: %w", err)
	}
	return s, nil
}

func (s *SQLiteTokenStore) migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS auth_tokens (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    provider TEXT NOT NULL,
    encrypted_access_token BLOB NOT NULL,
    encrypted_refresh_token BLOB,
    token_type TEXT NOT NULL,
    expiry TIMESTAMP,
    scopes TEXT NOT NULL,
    connected_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return err
	}
	return nil
}

// encryptedRecord is the serialized and encrypted form stored on disk.
type encryptedRecord struct {
	Provider              string    `json:"provider"`
	EncryptedAccessToken  []byte    `json:"encrypted_access_token"`
	EncryptedRefreshToken []byte    `json:"encrypted_refresh_token,omitempty"`
	TokenType             string    `json:"token_type"`
	Expiry                time.Time `json:"expiry"`
	Scopes                []string  `json:"scopes"`
	ConnectedAt           time.Time `json:"connected_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// Save encrypts record and persists it, replacing any existing row.
func (s *SQLiteTokenStore) Save(ctx context.Context, record *TokenRecord) error {
	enc, err := s.encryptRecord(record)
	if err != nil {
		return err
	}

	scopesJSON, err := json.Marshal(enc.Scopes)
	if err != nil {
		return fmt.Errorf("auth: failed to marshal scopes: %w", err)
	}

	const upsert = `
INSERT INTO auth_tokens
    (id, provider, encrypted_access_token, encrypted_refresh_token, token_type, expiry, scopes, connected_at, updated_at)
VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    provider = excluded.provider,
    encrypted_access_token = excluded.encrypted_access_token,
    encrypted_refresh_token = excluded.encrypted_refresh_token,
    token_type = excluded.token_type,
    expiry = excluded.expiry,
    scopes = excluded.scopes,
    connected_at = excluded.connected_at,
    updated_at = excluded.updated_at
`
	_, err = s.db.ExecContext(ctx, upsert,
		enc.Provider,
		enc.EncryptedAccessToken,
		enc.EncryptedRefreshToken,
		enc.TokenType,
		enc.Expiry,
		string(scopesJSON),
		enc.ConnectedAt,
		enc.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("auth: failed to save token record: %w", err)
	}
	return nil
}

// Load reads the stored record and decrypts it.
func (s *SQLiteTokenStore) Load(ctx context.Context) (*TokenRecord, error) {
	const query = `
SELECT provider, encrypted_access_token, encrypted_refresh_token, token_type, expiry, scopes, connected_at, updated_at
FROM auth_tokens
WHERE id = 1
`
	var enc encryptedRecord
	var scopesJSON string
	row := s.db.QueryRowContext(ctx, query)
	err := row.Scan(
		&enc.Provider,
		&enc.EncryptedAccessToken,
		&enc.EncryptedRefreshToken,
		&enc.TokenType,
		&enc.Expiry,
		&scopesJSON,
		&enc.ConnectedAt,
		&enc.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth: failed to load token record: %w", err)
	}
	if err := json.Unmarshal([]byte(scopesJSON), &enc.Scopes); err != nil {
		return nil, fmt.Errorf("auth: failed to unmarshal scopes: %w", err)
	}
	return s.decryptRecord(&enc)
}

// Delete removes stored credentials.
func (s *SQLiteTokenStore) Delete(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM auth_tokens WHERE id = 1`); err != nil {
		return fmt.Errorf("auth: failed to delete token record: %w", err)
	}
	return nil
}

// Close closes the underlying database connection.
func (s *SQLiteTokenStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteTokenStore) encryptRecord(record *TokenRecord) (*encryptedRecord, error) {
	encAccess, err := encrypt(s.key, []byte(record.AccessToken))
	if err != nil {
		return nil, fmt.Errorf("auth: failed to encrypt access token: %w", err)
	}
	var encRefresh []byte
	if record.RefreshToken != "" {
		encRefresh, err = encrypt(s.key, []byte(record.RefreshToken))
		if err != nil {
			return nil, fmt.Errorf("auth: failed to encrypt refresh token: %w", err)
		}
	}
	return &encryptedRecord{
		Provider:              record.Provider,
		EncryptedAccessToken:  encAccess,
		EncryptedRefreshToken: encRefresh,
		TokenType:             record.TokenType,
		Expiry:                record.Expiry,
		Scopes:                record.Scopes,
		ConnectedAt:           record.ConnectedAt,
		UpdatedAt:             record.UpdatedAt,
	}, nil
}

func (s *SQLiteTokenStore) decryptRecord(enc *encryptedRecord) (*TokenRecord, error) {
	access, err := decrypt(s.key, enc.EncryptedAccessToken)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to decrypt access token: %w", err)
	}
	var refresh []byte
	if len(enc.EncryptedRefreshToken) > 0 {
		refresh, err = decrypt(s.key, enc.EncryptedRefreshToken)
		if err != nil {
			return nil, fmt.Errorf("auth: failed to decrypt refresh token: %w", err)
		}
	}
	return &TokenRecord{
		Provider:     enc.Provider,
		AccessToken:  string(access),
		RefreshToken: string(refresh),
		TokenType:    enc.TokenType,
		Expiry:       enc.Expiry,
		Scopes:       enc.Scopes,
		ConnectedAt:  enc.ConnectedAt,
		UpdatedAt:    enc.UpdatedAt,
	}, nil
}

// compile-time interface check.
var _ TokenStore = (*SQLiteTokenStore)(nil)

// InMemoryStateStore is a non-production state cache used for tests and
// single-process deployments. State entries expire after a short TTL.
type InMemoryStateStore struct {
	mu     sync.Mutex
	states map[string]time.Time
}

// NewInMemoryStateStore returns an empty in-memory state store.
func NewInMemoryStateStore() *InMemoryStateStore {
	return &InMemoryStateStore{states: make(map[string]time.Time)}
}

// Save stores state with an expiration time.
func (m *InMemoryStateStore) Save(ctx context.Context, state string, expiresAt time.Time) error {
	if strings.TrimSpace(state) == "" {
		return errors.New("auth: state cannot be empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[state] = expiresAt
	return nil
}

// Validate returns true if state exists and has not expired, then removes it.
func (m *InMemoryStateStore) Validate(ctx context.Context, state string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	expires, ok := m.states[state]
	if !ok {
		return false, nil
	}
	delete(m.states, state)
	return now().Before(expires), nil
}

// compile-time interface check.
var _ StateStore = (*InMemoryStateStore)(nil)
