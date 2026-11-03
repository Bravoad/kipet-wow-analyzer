ALTER TABLE provider_state DROP CONSTRAINT provider_state_provider_check;
ALTER TABLE provider_state ADD CONSTRAINT provider_state_provider_check CHECK (provider IN ('blizzard','warcraftlogs'));

CREATE TABLE combat_reports (
    code text PRIMARY KEY CHECK (code ~ '^[A-Za-z0-9]{16}$'),
    version uuid,
    payload jsonb CHECK (payload IS NULL OR jsonb_typeof(payload)='object'),
    fetched_at timestamptz,
    fresh_until timestamptz,
    valid_until timestamptz,
    blocked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK ((payload IS NULL AND version IS NULL AND fetched_at IS NULL AND fresh_until IS NULL AND valid_until IS NULL)
        OR (payload IS NOT NULL AND version IS NOT NULL AND fetched_at IS NOT NULL AND fresh_until>fetched_at
        AND valid_until>=fresh_until AND valid_until<=fetched_at+interval '24 hours'))
);
CREATE INDEX combat_reports_expiry ON combat_reports(valid_until);

CREATE TABLE combat_analyses (
    id uuid PRIMARY KEY,
    report_code text NOT NULL REFERENCES combat_reports(code) ON DELETE CASCADE,
    report_version uuid NOT NULL,
    fight_id integer NOT NULL CHECK (fight_id>0),
    actor_id integer NOT NULL CHECK (actor_id>0),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='object'),
    fetched_at timestamptz NOT NULL,
    fresh_until timestamptz NOT NULL,
    valid_until timestamptz NOT NULL,
    CHECK (fresh_until>fetched_at AND valid_until>=fresh_until AND valid_until<=fetched_at+interval '24 hours'),
    UNIQUE(report_code,report_version,fight_id,actor_id)
);
CREATE INDEX combat_analyses_expiry ON combat_analyses(valid_until);

CREATE TABLE combat_jobs (
    id uuid PRIMARY KEY,
    report_code text NOT NULL REFERENCES combat_reports(code) ON DELETE CASCADE,
    report_version uuid,
    fight_id integer NOT NULL DEFAULT 0,
    actor_id integer NOT NULL DEFAULT 0,
    CHECK ((fight_id=0 AND actor_id=0) OR (fight_id>0 AND actor_id>0)),
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','retry_wait','succeeded','failed','cancelled')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts>=0),
    max_attempts integer NOT NULL DEFAULT 5 CHECK (max_attempts=5),
    run_after timestamptz NOT NULL DEFAULT clock_timestamp(),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    finished_at timestamptz,
    last_error_code text,
    lease_token uuid,
    lease_owner text,
    lease_until timestamptz,
    CHECK ((status='running' AND lease_token IS NOT NULL AND lease_owner IS NOT NULL AND lease_until IS NOT NULL)
        OR (status<>'running' AND lease_token IS NULL AND lease_owner IS NULL AND lease_until IS NULL))
);
CREATE UNIQUE INDEX combat_jobs_active ON combat_jobs(report_code,fight_id,actor_id) WHERE status IN ('queued','running','retry_wait');
CREATE INDEX combat_jobs_ready ON combat_jobs(run_after,created_at) WHERE status IN ('queued','retry_wait');
CREATE TABLE combat_negative_lookups (
    key_hash bytea PRIMARY KEY CHECK (octet_length(key_hash)=32),
    expires_at timestamptz NOT NULL
);
CREATE INDEX combat_negative_expiry ON combat_negative_lookups(expires_at);
