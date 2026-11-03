-- Reports are immutable for a snapshot/catalog/context while their cache deadline
-- is valid. The caller supplies localization keys rather than translated text.
CREATE TABLE analysis_results (
    snapshot_id uuid NOT NULL REFERENCES character_snapshots(id) ON DELETE CASCADE,
    engine_digest text NOT NULL CHECK (engine_digest ~ '^[a-f0-9]{64}$'),
    content text NOT NULL CHECK (content IN ('raid', 'mythic_plus', 'general_pve')),
    payload jsonb NOT NULL CHECK (
        jsonb_typeof(payload) = 'object'
        AND payload ? 'snapshot_id'
        AND jsonb_typeof(payload->'snapshot_id') = 'string'
        AND payload->>'snapshot_id' = snapshot_id::text
    ),
    valid_until timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (snapshot_id, engine_digest, content)
);
CREATE INDEX analysis_results_retention ON analysis_results(valid_until);
