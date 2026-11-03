package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
)

// FindMany obtains identities, latest snapshots, sections and negative-cache
// state in one SQL statement, so guild size does not create N profile reads.
// Results align with caller order and still need application publication guards.
func (s *Store) FindMany(ctx context.Context, keys []character.Key) ([]*character.Record, error) {
	if len(keys) > 1000 {
		return nil, character.NewFault("INVALID_ARGUMENT", false)
	}
	result := make([]*character.Record, len(keys))
	if len(keys) == 0 {
		return result, nil
	}
	type input struct {
		Region string `json:"region"`
		Realm  string `json:"realm"`
		Name   string `json:"name"`
		Hash   string `json:"hash"`
	}
	inputs := make([]input, 0, len(keys))
	for _, key := range keys {
		inputs = append(inputs, input{key.Region, key.Realm, key.Name, hex.EncodeToString(s.missingHash(key))})
	}
	payload, err := json.Marshal(inputs)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `WITH requested AS (
	 SELECT value,ordinality FROM jsonb_array_elements($1::jsonb) WITH ORDINALITY)
	 SELECT i.ordinality::int,
	 EXISTS(SELECT 1 FROM negative_lookups n WHERE n.key_hash=decode(i.value->>'hash','hex') AND n.expires_at>clock_timestamp()),
	 c.id::text,c.name,c.provider_character_id::text,c.publication_blocked_at IS NOT NULL,c.data_valid_until,
	 r.id::text,r.provider_realm_id::text,r.region,r.slug,r.names,r.connected_realm_id::text,r.fetched_at,
	 s.id::text,COALESCE(s.refresh_job_id::text,''),s.fetched_at,s.profile_verified_at,s.valid_until,s.patch,s.season_id::text,
	 COALESCE((SELECT jsonb_agg(jsonb_build_object('name',ss.section,'state',ss.state,'reason',ss.error_code,
	 'fetched_at',ss.fetched_at,'source_updated_at',ss.source_updated_at,'checked_at',ss.checked_at,
	 'fresh_until',ss.fresh_until,'valid_until',ss.valid_until,'payload',ss.payload)) FROM snapshot_sections ss WHERE ss.snapshot_id=s.id),'[]'::jsonb),
	 (SELECT jsonb_build_object('id',j.id,'status',j.status,'error_code',j.last_error_code) FROM refresh_jobs j WHERE j.character_id=c.id ORDER BY j.created_at DESC,j.id DESC LIMIT 1)
	 FROM requested i LEFT JOIN realms r ON r.region=i.value->>'region' AND r.slug=i.value->>'realm'
	 LEFT JOIN characters c ON c.region=r.region AND c.realm_id=r.id AND c.name_normalized=i.value->>'name'
	 LEFT JOIN character_snapshots s ON s.id=c.current_snapshot_id AND s.character_id=c.id
	 ORDER BY i.ordinality`, payload)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ordinal int
		var missing, blocked bool
		var id, name, provider, realmID, realmProvider, region, slug, connected, snapshotID, jobID, patch, season *string
		var identityUntil, realmFetched, fetched, verified, valid *time.Time
		var names, sections, job []byte
		if err = rows.Scan(&ordinal, &missing, &id, &name, &provider, &blocked, &identityUntil, &realmID, &realmProvider, &region, &slug, &names, &connected, &realmFetched, &snapshotID, &jobID, &fetched, &verified, &valid, &patch, &season, &sections, &job); err != nil {
			return nil, err
		}
		if ordinal < 1 || ordinal > len(keys) {
			return nil, character.NewFault("INFRASTRUCTURE_UNAVAILABLE", true)
		}
		key := keys[ordinal-1]
		if missing {
			result[ordinal-1] = &character.Record{Key: key, Missing: true}
			continue
		}
		if id == nil {
			continue
		}
		record := &character.Record{ID: *id, Key: key, ProviderID: provider, Blocked: blocked, DataValidUntil: identityUntil}
		if name != nil {
			record.Name = *name
			record.Key.LookupName = *name
		}
		if realmID != nil {
			record.Realm.ID = *realmID
		}
		if realmProvider != nil {
			record.Realm.ProviderID = *realmProvider
		}
		if region != nil {
			record.Realm.Region = *region
		}
		if slug != nil {
			record.Realm.Slug = *slug
		}
		record.Realm.ConnectedID = connected
		if realmFetched != nil {
			record.Realm.FetchedAt = *realmFetched
		}
		if len(names) > 0 && json.Unmarshal(names, &record.Realm.Names) != nil {
			return nil, character.NewFault("INFRASTRUCTURE_UNAVAILABLE", true)
		}
		if snapshotID != nil && fetched != nil && verified != nil && valid != nil {
			snapshot := &character.Snapshot{ID: *snapshotID, CharacterID: record.ID, DisplayName: record.Name, FetchedAt: *fetched, ProfileVerifiedAt: *verified, ValidUntil: *valid, Patch: patch, Season: season, Sections: map[string]character.Section{}}
			if provider != nil {
				snapshot.ProviderID = *provider
			}
			if jobID != nil {
				snapshot.JobID = *jobID
			}
			var raw []struct {
				Name            string          `json:"name"`
				State           string          `json:"state"`
				Reason          *string         `json:"reason"`
				FetchedAt       *time.Time      `json:"fetched_at"`
				SourceUpdatedAt *time.Time      `json:"source_updated_at"`
				CheckedAt       time.Time       `json:"checked_at"`
				FreshUntil      *time.Time      `json:"fresh_until"`
				ValidUntil      *time.Time      `json:"valid_until"`
				Payload         json.RawMessage `json:"payload"`
			}
			if json.Unmarshal(sections, &raw) != nil {
				return nil, character.NewFault("INFRASTRUCTURE_UNAVAILABLE", true)
			}
			for _, v := range raw {
				data := v.Payload
				if string(data) == "null" {
					data = nil
				}
				snapshot.Sections[v.Name] = character.Section{State: v.State, Reason: v.Reason, FetchedAt: v.FetchedAt, SourceUpdatedAt: v.SourceUpdatedAt, CheckedAt: v.CheckedAt, FreshUntil: v.FreshUntil, ValidUntil: v.ValidUntil, Payload: data}
			}
			record.Snapshot = snapshot
		}
		if len(job) > 0 {
			var latest struct {
				ID        string  `json:"id"`
				Status    string  `json:"status"`
				ErrorCode *string `json:"error_code"`
			}
			if json.Unmarshal(job, &latest) != nil {
				return nil, character.NewFault("INFRASTRUCTURE_UNAVAILABLE", true)
			}
			record.LastJob = &character.Job{ID: latest.ID, Status: latest.Status, ErrorCode: latest.ErrorCode}
		}
		result[ordinal-1] = record
	}
	return result, rows.Err()
}
