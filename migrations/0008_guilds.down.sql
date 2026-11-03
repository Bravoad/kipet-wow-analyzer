DROP TABLE guild_negative_lookups;
DROP TABLE guild_jobs;
ALTER TABLE guilds DROP CONSTRAINT guild_current_snapshot_fk;
DROP TABLE guild_snapshots;
DROP TABLE guilds;
