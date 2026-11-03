CREATE TABLE guilds (
    id uuid PRIMARY KEY,
    region text NOT NULL CHECK(region IN ('eu','us')),
    realm_id uuid NOT NULL REFERENCES realms(id) ON DELETE CASCADE,
    slug text NOT NULL,
    provider_id text,
    current_snapshot_id uuid,
    blocked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(region,realm_id,slug)
);
CREATE TABLE guild_snapshots (
    id uuid PRIMARY KEY,
    guild_id uuid NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
    payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
    fetched_at timestamptz NOT NULL,
    fresh_until timestamptz NOT NULL,
    valid_until timestamptz NOT NULL,
    CHECK(fresh_until>fetched_at AND valid_until>=fresh_until AND valid_until<=fetched_at+interval '720 hours')
);
ALTER TABLE guilds ADD CONSTRAINT guild_current_snapshot_fk FOREIGN KEY(current_snapshot_id) REFERENCES guild_snapshots(id) ON DELETE SET NULL;
CREATE INDEX guild_snapshots_expiry ON guild_snapshots(valid_until);
CREATE TABLE guild_jobs (
    id uuid PRIMARY KEY,
    guild_id uuid NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
    snapshot_id uuid REFERENCES guild_snapshots(id) ON DELETE SET NULL,
    schedule_cursor integer NOT NULL DEFAULT 0 CHECK(schedule_cursor BETWEEN 0 AND 1000),
    status text NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','running','retry_wait','succeeded','failed','cancelled')),
    attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
    max_attempts integer NOT NULL DEFAULT 5 CHECK(max_attempts=5),
    run_after timestamptz NOT NULL DEFAULT clock_timestamp(),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    finished_at timestamptz,
    last_error_code text,
    lease_token uuid,
    lease_owner text,
    lease_until timestamptz,
    CHECK((status='running' AND lease_token IS NOT NULL AND lease_owner IS NOT NULL AND lease_until IS NOT NULL)
        OR(status<>'running' AND lease_token IS NULL AND lease_owner IS NULL AND lease_until IS NULL))
);
CREATE UNIQUE INDEX guild_jobs_active ON guild_jobs(guild_id) WHERE status IN ('queued','running','retry_wait');
CREATE INDEX guild_jobs_ready ON guild_jobs(run_after,created_at) WHERE status IN ('queued','retry_wait');
CREATE TABLE guild_negative_lookups (
    key_hash bytea PRIMARY KEY CHECK(octet_length(key_hash)=32),
    expires_at timestamptz NOT NULL
);
CREATE INDEX guild_negative_expiry ON guild_negative_lookups(expires_at);
