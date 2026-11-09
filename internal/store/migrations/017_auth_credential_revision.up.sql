-- Fence credential updates by opaque connection identity and monotonic revision.
ALTER TABLE auth_tokens ADD COLUMN authorization_id TEXT NOT NULL DEFAULT '';
ALTER TABLE auth_tokens ADD COLUMN credential_revision INTEGER NOT NULL DEFAULT 1 CHECK (credential_revision >= 1);
ALTER TABLE auth_tokens ADD COLUMN access_rejected INTEGER NOT NULL DEFAULT 0 CHECK (access_rejected IN (0, 1));
ALTER TABLE auth_tokens ADD COLUMN renewal_failure TEXT CHECK (renewal_failure IS NULL OR renewal_failure IN (
    'refresh_rejected', 'temporary_network', 'temporary_provider',
    'persistence_failed', 'invalid_response', 'client_configuration',
    'access_rejected'
));

-- Give a pre-existing credential one opaque UUIDv4 identity without touching
-- its encrypted values or any existing metadata.
UPDATE auth_tokens SET authorization_id =
    lower(substr(hex(randomblob(16)), 1, 8) || '-' ||
          substr(hex(randomblob(16)), 9, 4) || '-4' ||
          substr(hex(randomblob(16)), 14, 3) || '-a' ||
          substr(hex(randomblob(16)), 18, 3) || '-' ||
          substr(hex(randomblob(16)), 21, 12))
WHERE id = 1;
