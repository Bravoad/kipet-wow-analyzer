CREATE TABLE negative_lookups (
    key_hash bytea PRIMARY KEY CHECK (octet_length(key_hash) = 32),
    reason text NOT NULL CHECK (reason = 'CHARACTER_UNAVAILABLE'),
    expires_at timestamptz NOT NULL
);
CREATE INDEX negative_lookups_expiry ON negative_lookups (expires_at);
