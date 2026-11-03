ALTER TABLE provider_state DROP CONSTRAINT provider_state_provider_check;
ALTER TABLE provider_state ADD CONSTRAINT provider_state_provider_check CHECK(provider IN ('blizzard','warcraftlogs','raiderio'));

CREATE TABLE raiderio_profiles (
    id uuid PRIMARY KEY,
    region text NOT NULL CHECK(region IN ('eu','us')),
    realm text NOT NULL,
    name text NOT NULL,
    version uuid,
    payload jsonb CHECK(jsonb_typeof(payload)='object'),
    fetched_at timestamptz,
    fresh_until timestamptz,
    valid_until timestamptz,
    blocked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(region,realm,name),
    CHECK((payload IS NULL AND version IS NULL AND fetched_at IS NULL AND fresh_until IS NULL AND valid_until IS NULL)
       OR (payload IS NOT NULL AND version IS NOT NULL AND fetched_at IS NOT NULL AND fresh_until IS NOT NULL
           AND valid_until IS NOT NULL AND fresh_until>fetched_at AND valid_until>=fresh_until
           AND valid_until<=fetched_at+interval '24 hours'))
);
CREATE INDEX raiderio_profiles_expiry ON raiderio_profiles(valid_until) WHERE payload IS NOT NULL;

CREATE TABLE raiderio_jobs (
    id uuid PRIMARY KEY,
    profile_id uuid NOT NULL REFERENCES raiderio_profiles(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','running','retry_wait','succeeded','failed','cancelled')),
    attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 5),
    run_after timestamptz NOT NULL DEFAULT clock_timestamp(),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    finished_at timestamptz,
    last_error_code text,
    lease_token uuid,
    lease_until timestamptz,
    CHECK((status='running' AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
       OR (status<>'running' AND lease_token IS NULL AND lease_until IS NULL))
);
CREATE UNIQUE INDEX raiderio_jobs_active ON raiderio_jobs(profile_id) WHERE status IN ('queued','running','retry_wait');
CREATE INDEX raiderio_jobs_ready ON raiderio_jobs(run_after,created_at) WHERE status IN ('queued','retry_wait');
CREATE INDEX raiderio_jobs_history ON raiderio_jobs(profile_id,created_at DESC);

CREATE TABLE raiderio_negative_lookups (
    key_hash bytea PRIMARY KEY CHECK(octet_length(key_hash)=32),
    expires_at timestamptz NOT NULL
);
CREATE INDEX raiderio_negative_expiry ON raiderio_negative_lookups(expires_at);
