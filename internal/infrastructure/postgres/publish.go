package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"kipet-wow-analyzer/internal/domain/character"
)

func (s *Store) Publish(ctx context.Context, lease character.Lease, snapshot character.Snapshot) error {
	if err := character.ValidateSnapshot(snapshot); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	job, err := lockLease(ctx, tx, lease)
	if err != nil {
		return err
	}
	var providerID *string
	var realmID string
	if err = tx.QueryRow(ctx, `SELECT provider_character_id::text,realm_id::text FROM characters WHERE id=$1 FOR UPDATE`, job.CharacterID).Scan(&providerID, &realmID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return character.ErrLeaseLost
		}
		return err
	}
	if fold(snapshot.DisplayName) != job.Key.Name {
		return fmt.Errorf("snapshot name does not match queued identity")
	}
	if providerID != nil && *providerID != snapshot.ProviderID {
		// A reused name starts a new identity; the already fetched payload is
		// deliberately discarded. Old job polling recovers with one profile GET.
		tag, e := tx.Exec(ctx, `DELETE FROM characters c WHERE c.id=$1 AND EXISTS(SELECT 1 FROM refresh_jobs j WHERE j.id=$2 AND j.lease_token=$3 AND j.status='running' AND j.lease_until>clock_timestamp())`, job.CharacterID, job.ID, lease.Token)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return character.ErrLeaseLost
		}
		newID := uuid.NewString()
		if _, err = tx.Exec(ctx, `INSERT INTO characters(id,region,realm_id,name,name_normalized,provider_character_id) VALUES($1,$2,$3,$4,$5,$6::bigint)`, newID, job.Key.Region, realmID, snapshot.DisplayName, job.Key.Name, snapshot.ProviderID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO refresh_jobs(id,character_id,reason) VALUES($1,$2,'revalidation')`, uuid.NewString(), newID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	realm, err := scanRealm(tx.QueryRow(ctx, `SELECT `+realmColumns+` FROM realms WHERE id=$1`, realmID))
	if err != nil {
		return err
	}
	if snapshot.ID == "" {
		snapshot.ID = uuid.NewString()
	}
	snapshot.CharacterID = job.CharacterID
	snapshot.JobID = job.ID
	_, err = tx.Exec(ctx, `INSERT INTO character_snapshots(id,character_id,refresh_job_id,fetched_at,profile_verified_at,valid_until,patch,season_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8::bigint)`, snapshot.ID, job.CharacterID, job.ID, snapshot.FetchedAt, snapshot.ProfileVerifiedAt, snapshot.ValidUntil, snapshot.Patch, snapshot.Season)
	if err != nil {
		return err
	}
	for _, name := range sortedSections() {
		section, exists := snapshot.Sections[name]
		if !exists {
			// Optional modules may not yet have fetched a section. Missing data
			// must never be persisted as an observed empty list.
			section = character.Unavailable("never_fetched", snapshot.FetchedAt)
		}
		if section.State != "unavailable" {
			var payload map[string]any
			if err = json.Unmarshal(section.Payload, &payload); err != nil {
				return err
			}
			if name == "profile" {
				payload["id"] = job.CharacterID
				payload["provider_id"] = snapshot.ProviderID
				payload["region"] = job.Key.Region
				payload["name"] = snapshot.DisplayName
				payload["name_normalized"] = job.Key.Name
				payload["realm"] = realm.DTO("en-US")
			} else {
				payload["character_id"] = job.CharacterID
			}
			section.Payload, err = json.Marshal(payload)
			if err != nil {
				return err
			}
		}
		var payload any
		if len(section.Payload) > 0 {
			payload = []byte(section.Payload)
		}
		_, err = tx.Exec(ctx, `INSERT INTO snapshot_sections(snapshot_id,section,state,error_code,fetched_at,source_updated_at,checked_at,fresh_until,valid_until,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb)`, snapshot.ID, name, section.State, section.Reason, section.FetchedAt, section.SourceUpdatedAt, section.CheckedAt, section.FreshUntil, section.ValidUntil, payload)
		if err != nil {
			return fmt.Errorf("persist %s section: %w", name, err)
		}
		if section.State != "unavailable" && name == "equipment" {
			if err = saveEquipment(ctx, tx, snapshot.ID, section.Payload); err != nil {
				return err
			}
		}
		if section.State != "unavailable" && name == "talents" {
			if err = saveTalents(ctx, tx, snapshot.ID, section.Payload); err != nil {
				return err
			}
		}
	}
	identityDeadline := snapshot.ValidUntil
	if profileDeadline := snapshot.Sections["profile"].ValidUntil; profileDeadline != nil && profileDeadline.Before(identityDeadline) {
		identityDeadline = *profileDeadline
	}
	if _, err = tx.Exec(ctx, `UPDATE characters SET provider_character_id=$2::bigint,name=$3,current_snapshot_id=$4,data_valid_until=$5,publication_blocked_at=NULL,publication_block_reason=NULL,updated_at=clock_timestamp() WHERE id=$1`, job.CharacterID, snapshot.ProviderID, snapshot.DisplayName, snapshot.ID, identityDeadline); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE refresh_jobs SET status='succeeded',finished_at=clock_timestamp(),updated_at=clock_timestamp(),last_error_code=NULL,lease_token=NULL,lease_owner=NULL,leased_at=NULL,lease_until=NULL WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_until>clock_timestamp()`, job.ID, lease.Token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return character.ErrLeaseLost
	}
	return tx.Commit(ctx)
}

type equipmentPayload struct {
	Items []struct {
		Slot          string          `json:"slot"`
		ItemID        string          `json:"item_id"`
		Name          *string         `json:"name"`
		ItemLevel     *int            `json:"item_level"`
		Quality       *string         `json:"quality"`
		InventoryType *string         `json:"inventory_type"`
		Enchantments  json.RawMessage `json:"enchantments"`
		Sockets       json.RawMessage `json:"sockets"`
		Stats         json.RawMessage `json:"stats"`
		SetID         *string         `json:"set_id"`
	} `json:"items"`
}

func jsonArrayState(raw json.RawMessage) (string, any) {
	if len(raw) == 0 || string(raw) == "null" {
		return "unknown", nil
	}
	return "known", []byte(raw)
}
func saveEquipment(ctx context.Context, tx pgx.Tx, snapshotID string, raw json.RawMessage) error {
	var payload equipmentPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	for _, item := range payload.Items {
		names := map[string]string{}
		if item.Name != nil {
			names["en-US"] = *item.Name
		}
		nameJSON, _ := json.Marshal(names)
		enchantState, enchants := jsonArrayState(item.Enchantments)
		socketState, sockets := jsonArrayState(item.Sockets)
		var stats, sets any
		if len(item.Stats) > 0 && string(item.Stats) != "null" {
			stats = []byte(item.Stats)
		}
		if item.SetID != nil {
			sets, _ = json.Marshal(map[string]string{"id": *item.SetID})
		}
		_, err := tx.Exec(ctx, `INSERT INTO equipment_items(snapshot_id,slot,provider_item_id,item_level,quality,names,inventory_type,enchantments_state,enchantments,sockets_state,sockets,stats,set_info) VALUES($1,$2,$3::bigint,$4,$5,$6::jsonb,$7,$8,$9::jsonb,$10,$11::jsonb,$12::jsonb,$13::jsonb)`, snapshotID, item.Slot, item.ItemID, item.ItemLevel, item.Quality, nameJSON, item.InventoryType, enchantState, enchants, socketState, sockets, stats, sets)
		if err != nil {
			return fmt.Errorf("persist equipment: %w", err)
		}
	}
	return nil
}

type talentPayload struct {
	Loadouts []struct {
		ProviderID     *string `json:"provider_id"`
		Specialization struct {
			ID string `json:"id"`
		} `json:"specialization"`
		Name       *string         `json:"name"`
		IsActive   *bool           `json:"is_active"`
		ExportCode *string         `json:"export_code"`
		Nodes      json.RawMessage `json:"nodes"`
	} `json:"loadouts"`
}

func saveTalents(ctx context.Context, tx pgx.Tx, snapshotID string, raw json.RawMessage) error {
	var payload talentPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	for _, loadout := range payload.Loadouts {
		state, nodes := jsonArrayState(loadout.Nodes)
		_, err := tx.Exec(ctx, `INSERT INTO talent_loadouts(id,snapshot_id,provider_loadout_id,specialization_id,name,is_active,export_code,nodes_state,selected_nodes) VALUES($1,$2,$3::bigint,$4::bigint,$5,$6,$7,$8,$9::jsonb)`, uuid.NewString(), snapshotID, loadout.ProviderID, loadout.Specialization.ID, loadout.Name, loadout.IsActive, loadout.ExportCode, state, nodes)
		if err != nil {
			return fmt.Errorf("persist talent loadout: %w", err)
		}
	}
	return nil
}

// Maintain never extends source timestamps. Expired data is hidden on the read
// path immediately; this bounded cleanup subsequently removes all stored copies.
func (s *Store) Maintain(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(748215930003)`); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM negative_lookups WHERE expires_at<=clock_timestamp()`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM analysis_results a WHERE a.valid_until<=clock_timestamp()
	 OR EXISTS(SELECT 1 FROM snapshot_sections v WHERE v.snapshot_id=a.snapshot_id
	 AND v.section IN ('profile','equipment','statistics','talents') AND v.valid_until<=clock_timestamp())`)
	if err != nil {
		return err
	}
	// Preserve the section shape while removing all expired source data.
	_, err = tx.Exec(ctx, `DELETE FROM equipment_items i USING snapshot_sections x WHERE i.snapshot_id=x.snapshot_id AND x.section='equipment' AND x.valid_until<=clock_timestamp()`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM talent_loadouts i USING snapshot_sections x WHERE i.snapshot_id=x.snapshot_id AND x.section='talents' AND x.valid_until<=clock_timestamp()`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE snapshot_sections SET state='unavailable',error_code='expired',fetched_at=NULL,source_updated_at=NULL,fresh_until=NULL,valid_until=NULL,payload=NULL WHERE valid_until<=clock_timestamp()`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE characters c SET current_snapshot_id=NULL FROM character_snapshots s WHERE c.current_snapshot_id=s.id AND s.valid_until<=clock_timestamp()`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM character_snapshots WHERE valid_until<=clock_timestamp()`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM refresh_jobs WHERE status IN ('succeeded','failed','cancelled') AND finished_at<clock_timestamp()-interval '24 hours'`)
	if err != nil {
		return err
	}
	// Hard-deadline cleanup also removes stale identity metadata and old jobs.
	_, err = tx.Exec(ctx, `DELETE FROM characters WHERE COALESCE(data_valid_until,created_at+interval '720 hours')<=clock_timestamp()`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM characters WHERE realm_id IN (SELECT id FROM realms WHERE fetched_at<=clock_timestamp()-interval '720 hours')`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM realms WHERE fetched_at<=clock_timestamp()-interval '720 hours'`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM item_catalog WHERE fetched_at<=clock_timestamp()-interval '720 hours'`)
	if err != nil {
		return err
	}
	// Queue revalidation before the hard deadline even for unvisited profiles.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(748215930002)`); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT c.id::text FROM characters c JOIN character_snapshots s ON s.id=c.current_snapshot_id WHERE s.profile_verified_at<clock_timestamp()-interval '648 hours' AND NOT EXISTS(SELECT 1 FROM refresh_jobs j WHERE j.character_id=c.id AND (j.status IN ('queued','running','retry_wait') OR j.finished_at>clock_timestamp()-interval '15 minutes')) ORDER BY c.id LIMIT 100 FOR UPDATE OF c SKIP LOCKED`)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = tx.Exec(ctx, `INSERT INTO refresh_jobs(id,character_id,reason) SELECT $1,$2,'revalidation' WHERE (SELECT count(*) FROM refresh_jobs WHERE status IN ('queued','running','retry_wait'))<$3 ON CONFLICT(character_id) WHERE status IN ('queued','running','retry_wait') DO NOTHING`, uuid.NewString(), id, s.options.QueueCapacity); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
