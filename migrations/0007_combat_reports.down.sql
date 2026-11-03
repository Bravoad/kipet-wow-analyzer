DROP TABLE combat_negative_lookups;
DROP TABLE combat_jobs;
DROP TABLE combat_analyses;
DROP TABLE combat_reports;
DELETE FROM app_settings WHERE key='combat_provider_mode';
DELETE FROM provider_state WHERE provider='warcraftlogs';
ALTER TABLE provider_state DROP CONSTRAINT provider_state_provider_check;
ALTER TABLE provider_state ADD CONSTRAINT provider_state_provider_check CHECK (provider='blizzard');
