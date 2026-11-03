ALTER TABLE raiderio_profiles
    ADD COLUMN kind text NOT NULL DEFAULT 'character' CHECK(kind IN ('character','guild'));
ALTER TABLE raiderio_profiles DROP CONSTRAINT raiderio_profiles_region_realm_name_key;
ALTER TABLE raiderio_profiles ADD UNIQUE(kind,region,realm,name);

ALTER TABLE raiderio_negative_lookups
    ADD COLUMN kind text NOT NULL DEFAULT 'character' CHECK(kind IN ('character','guild'));
