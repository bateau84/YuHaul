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

	"github.com/google/uuid"
)

// SQLiteTokenStore persists encrypted OAuth2 tokens in a SQLite database. The
// single-user design stores at most one active token row (id = 1).
//
// The caller is responsible for opening db with the project's chosen SQLite
// driver and applying the canonical schema (see internal/store).
type SQLiteTokenStore struct {
	db  *sql.DB
	key []byte
}

// NewSQLiteTokenStore opens a SQLite-backed token store. The encryption key
// must be 32 bytes. The auth_tokens table is owned by the central migration
// system and must already exist.
func NewSQLiteTokenStore(db *sql.DB, key []byte) (*SQLiteTokenStore, error) {
	if db == nil {
		return nil, errors.New("auth: db is required")
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("auth: encryption key must be 32 bytes, got %d", len(key))
	}
	return &SQLiteTokenStore{db: db, key: key}, nil
}

// encryptedRecord is the serialized and encrypted form stored on disk.
type encryptedRecord struct {
	AuthorizationID       string    `json:"authorization_id"`
	CredentialRevision    int64     `json:"credential_revision"`
	AccessRejected        bool      `json:"access_rejected"`
	RenewalFailure        string    `json:"renewal_failure,omitempty"`
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
    (id, provider, encrypted_access_token, encrypted_refresh_token, token_type, expiry, scopes, connected_at, updated_at, authorization_id, credential_revision, access_rejected, renewal_failure)
VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 0, NULL)
ON CONFLICT(id) DO UPDATE SET
    provider = excluded.provider,
    encrypted_access_token = excluded.encrypted_access_token,
    encrypted_refresh_token = excluded.encrypted_refresh_token,
    token_type = excluded.token_type,
    expiry = excluded.expiry,
    scopes = excluded.scopes,
    connected_at = excluded.connected_at,
    updated_at = excluded.updated_at,
    authorization_id = excluded.authorization_id,
    credential_revision = 1,
    access_rejected = 0,
    renewal_failure = NULL
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
		uuid.NewString(),
	)
	if err != nil {
		return fmt.Errorf("auth: failed to save token record: %w", err)
	}
	return nil
}

// CredentialVersion identifies the exact durable credential snapshot used as
// the basis of a conditional credential operation.
type CredentialVersion struct {
	AuthorizationID string
	Revision        int64
}

// ErrAuthorizationChanged means the durable credential no longer matches the
// snapshot on which a caller based its operation. The caller must not retry
// with an unconditional write.
var ErrAuthorizationChanged = errors.New("auth: authorization changed")

// SaveRefresh atomically replaces credentials only if the same authorization
// identity and revision are still current. An omitted refresh token preserves
// its existing ciphertext. Revision overflow and stale snapshots fail closed.
func (s *SQLiteTokenStore) SaveRefresh(ctx context.Context, expected CredentialVersion, record *TokenRecord) (CredentialVersion, error) {
	enc, err := s.encryptRecord(record)
	if err != nil {
		return CredentialVersion{}, err
	}
	scopes, err := json.Marshal(enc.Scopes)
	if err != nil {
		return CredentialVersion{}, fmt.Errorf("auth: failed to marshal scopes: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE auth_tokens SET
 provider=?, encrypted_access_token=?,
 encrypted_refresh_token=CASE WHEN length(?) > 0 THEN ? ELSE encrypted_refresh_token END,
 token_type=?, expiry=?, scopes=?, updated_at=?, credential_revision=credential_revision+1,
 access_rejected=0, renewal_failure=NULL
WHERE id=1 AND authorization_id=? AND credential_revision=? AND credential_revision < 9223372036854775807`,
		enc.Provider, enc.EncryptedAccessToken, enc.EncryptedRefreshToken, enc.EncryptedRefreshToken,
		enc.TokenType, enc.Expiry, string(scopes), enc.UpdatedAt, expected.AuthorizationID, expected.Revision)
	if err != nil {
		return CredentialVersion{}, fmt.Errorf("auth: failed to save refreshed credentials: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return CredentialVersion{}, fmt.Errorf("auth: failed to inspect refreshed credential write: %w", err)
	}
	if n != 1 {
		return CredentialVersion{}, ErrAuthorizationChanged
	}
	return CredentialVersion{AuthorizationID: expected.AuthorizationID, Revision: expected.Revision + 1}, nil
}

// SaveRenewalFailure records only a closed application classification on the
// credential snapshot that produced it. It never changes credential revision.
func (s *SQLiteTokenStore) SaveRenewalFailure(ctx context.Context, expected CredentialVersion, failure string) error {
	if !validRenewalFailure(failure) {
		return errors.New("auth: invalid renewal failure classification")
	}
	rejected := failure == "refresh_rejected" || failure == "access_rejected"
	result, err := s.db.ExecContext(ctx, `UPDATE auth_tokens SET renewal_failure=CASE WHEN renewal_failure='refresh_rejected' AND ?<>'refresh_rejected' THEN renewal_failure ELSE ? END, access_rejected=CASE WHEN ? THEN 1 ELSE access_rejected END, updated_at=? WHERE id=1 AND authorization_id=? AND credential_revision=?`, failure, failure, rejected, now(), expected.AuthorizationID, expected.Revision)
	if err != nil {
		return fmt.Errorf("auth: failed to save renewal failure state: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("auth: failed to inspect renewal failure write: %w", err)
	}
	if n != 1 {
		return ErrAuthorizationChanged
	}
	return nil
}

func validRenewalFailure(value string) bool {
	switch value {
	case "refresh_rejected", "temporary_network", "temporary_provider", "persistence_failed", "invalid_response", "client_configuration", "access_rejected":
		return true
	default:
		return false
	}
}

// ReplaceAuthorization commits a newly accepted OAuth connection only while
// the previously observed credential snapshot (or absence) is still current.
// The new identity is generated here and starts at revision 1.
func (s *SQLiteTokenStore) ReplaceAuthorization(ctx context.Context, expected *CredentialVersion, record *TokenRecord) (CredentialVersion, error) {
	enc, err := s.encryptRecord(record)
	if err != nil {
		return CredentialVersion{}, err
	}
	scopes, err := json.Marshal(enc.Scopes)
	if err != nil {
		return CredentialVersion{}, fmt.Errorf("auth: failed to marshal scopes: %w", err)
	}
	id := uuid.NewString()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CredentialVersion{}, fmt.Errorf("auth: failed to begin authorization replacement: %w", err)
	}
	defer tx.Rollback()
	var current CredentialVersion
	err = tx.QueryRowContext(ctx, `SELECT authorization_id, credential_revision FROM auth_tokens WHERE id=1`).Scan(&current.AuthorizationID, &current.Revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return CredentialVersion{}, fmt.Errorf("auth: failed to inspect current authorization: %w", err)
	}
	if expected == nil {
		if err == nil {
			return CredentialVersion{}, ErrAuthorizationChanged
		}
	} else if err != nil || current != *expected {
		return CredentialVersion{}, ErrAuthorizationChanged
	}
	if expected == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO auth_tokens
(id,provider,encrypted_access_token,encrypted_refresh_token,token_type,expiry,scopes,connected_at,updated_at,authorization_id,credential_revision,access_rejected,renewal_failure)
VALUES (1,?,?,?,?,?,?,?,?,?,1,0,NULL)`, enc.Provider, enc.EncryptedAccessToken, enc.EncryptedRefreshToken, enc.TokenType, enc.Expiry, string(scopes), enc.ConnectedAt, enc.UpdatedAt, id)
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE auth_tokens SET provider=?,encrypted_access_token=?,encrypted_refresh_token=?,token_type=?,expiry=?,scopes=?,connected_at=?,updated_at=?,authorization_id=?,credential_revision=1,access_rejected=0,renewal_failure=NULL WHERE id=1 AND authorization_id=? AND credential_revision=?`, enc.Provider, enc.EncryptedAccessToken, enc.EncryptedRefreshToken, enc.TokenType, enc.Expiry, string(scopes), enc.ConnectedAt, enc.UpdatedAt, id, expected.AuthorizationID, expected.Revision)
		if err == nil {
			var n int64
			n, err = result.RowsAffected()
			if err == nil && n != 1 {
				err = ErrAuthorizationChanged
			}
		}
	}
	if err != nil {
		return CredentialVersion{}, fmt.Errorf("auth: failed to replace authorization: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CredentialVersion{}, fmt.Errorf("auth: failed to commit authorization replacement: %w", err)
	}
	return CredentialVersion{AuthorizationID: id, Revision: 1}, nil
}

// DeleteIfCurrent removes credentials only if their identity and revision
// still match the caller's snapshot.
func (s *SQLiteTokenStore) DeleteIfCurrent(ctx context.Context, expected CredentialVersion) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM auth_tokens WHERE id=1 AND authorization_id=? AND credential_revision=?`, expected.AuthorizationID, expected.Revision)
	if err != nil {
		return fmt.Errorf("auth: failed to delete current token record: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("auth: failed to inspect token deletion: %w", err)
	}
	if n != 1 {
		return ErrAuthorizationChanged
	}
	return nil
}

// Load reads the stored record and decrypts it.
func (s *SQLiteTokenStore) Load(ctx context.Context) (*TokenRecord, error) {
	const query = `
SELECT provider, encrypted_access_token, encrypted_refresh_token, token_type, expiry, scopes, connected_at, updated_at, authorization_id, credential_revision, access_rejected, COALESCE(renewal_failure,'')
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
		&enc.AuthorizationID,
		&enc.CredentialRevision,
		&enc.AccessRejected,
		&enc.RenewalFailure,
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
		AuthorizationID:       record.AuthorizationID,
		CredentialRevision:    record.CredentialRevision,
		AccessRejected:        record.AccessRejected,
		RenewalFailure:        record.RenewalFailure,
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
		AuthorizationID:    enc.AuthorizationID,
		CredentialRevision: enc.CredentialRevision,
		AccessRejected:     enc.AccessRejected,
		RenewalFailure:     enc.RenewalFailure,
		Provider:           enc.Provider,
		AccessToken:        string(access),
		RefreshToken:       string(refresh),
		TokenType:          enc.TokenType,
		Expiry:             enc.Expiry,
		Scopes:             enc.Scopes,
		ConnectedAt:        enc.ConnectedAt,
		UpdatedAt:          enc.UpdatedAt,
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
