-- Progress uses independent normalized sections and source timestamps. Existing
-- snapshots acquire explicit unknown sections rather than empty progress lists.
ALTER TABLE snapshot_sections DROP CONSTRAINT snapshot_sections_section_check;
ALTER TABLE snapshot_sections ADD CONSTRAINT snapshot_sections_section_check
    CHECK (section IN ('profile','equipment','statistics','talents','mythic_plus','raids'));

INSERT INTO snapshot_sections(snapshot_id,section,state,error_code,checked_at)
SELECT x.id,p.section,'unavailable','never_fetched',clock_timestamp()
FROM character_snapshots x
CROSS JOIN (VALUES ('mythic_plus'),('raids')) AS p(section)
ON CONFLICT(snapshot_id,section) DO NOTHING;
