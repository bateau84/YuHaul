-- Auth token storage: encrypted OAuth2 tokens for the single YouTube account.
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
