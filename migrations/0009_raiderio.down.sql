DROP TABLE raiderio_negative_lookups;
DROP TABLE raiderio_jobs;
DROP TABLE raiderio_profiles;
DELETE FROM provider_state WHERE provider='raiderio';
ALTER TABLE provider_state DROP CONSTRAINT provider_state_provider_check;
ALTER TABLE provider_state ADD CONSTRAINT provider_state_provider_check CHECK(provider IN ('blizzard','warcraftlogs'));
