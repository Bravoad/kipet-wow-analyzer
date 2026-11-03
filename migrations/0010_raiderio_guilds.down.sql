DELETE FROM raiderio_profiles WHERE kind='guild';
DELETE FROM raiderio_negative_lookups WHERE kind='guild';

ALTER TABLE raiderio_profiles DROP COLUMN kind;
ALTER TABLE raiderio_profiles ADD UNIQUE(region,realm,name);
ALTER TABLE raiderio_negative_lookups DROP COLUMN kind;
