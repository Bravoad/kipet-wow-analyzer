-- Identity metadata has its own source deadline; operational updates must not
-- extend the lifetime of names or provider IDs after a profile expires.
ALTER TABLE characters ADD COLUMN data_valid_until timestamptz;
UPDATE characters c SET data_valid_until=COALESCE(
    (SELECT s.valid_until FROM character_snapshots s WHERE s.id=c.current_snapshot_id),
    c.created_at+interval '720 hours');
CREATE INDEX characters_retention ON characters(data_valid_until);
