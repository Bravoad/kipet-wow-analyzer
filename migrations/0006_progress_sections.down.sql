-- Controlled rollback discards only the two optional progress sections.
DELETE FROM snapshot_sections WHERE section IN ('mythic_plus','raids');
ALTER TABLE snapshot_sections DROP CONSTRAINT snapshot_sections_section_check;
ALTER TABLE snapshot_sections ADD CONSTRAINT snapshot_sections_section_check
    CHECK (section IN ('profile','equipment','statistics','talents'));
