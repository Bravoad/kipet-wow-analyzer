ALTER TABLE characters DROP CONSTRAINT characters_current_snapshot_fk;
DROP TABLE provider_state, talent_loadouts, equipment_items, item_catalog,
    snapshot_sections, character_snapshots, refresh_jobs, characters, realms;
