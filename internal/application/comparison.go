package application

import (
	"context"
	"encoding/json"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/guild"
)

type CharacterBatchStore interface {
	FindMany(context.Context, []character.Key) ([]*character.Record, error)
}
type CharacterData struct {
	Profile    json.RawMessage `json:"profile"`
	Equipment  json.RawMessage `json:"equipment"`
	Statistics json.RawMessage `json:"statistics"`
	Talents    json.RawMessage `json:"talents"`
	MythicPlus json.RawMessage `json:"mythic_plus"`
	Raids      json.RawMessage `json:"raids"`
}
type CharacterProjection struct {
	Key    guild.CharacterKey `json:"key"`
	Status string             `json:"status"`
	Reason *string            `json:"reason"`
	Meta   Meta               `json:"meta"`
	Data   *CharacterData     `json:"data"`
}

// ProjectCharacter is shared by comparisons and guild analysis. Batch data is
// never a publication grant: profile, identity and each section keep deadlines.
func ProjectCharacter(record *character.Record, key character.Key, locale, mode string, now time.Time) CharacterProjection {
	name := key.LookupName
	if name == "" {
		name = key.Name
	}
	p := CharacterProjection{Key: guild.CharacterKey{Region: key.Region, Realm: key.Realm, Name: name}, Status: "pending", Meta: NewMeta(mode, locale, "pending")}
	setReason := func(status, reason string) { p.Status = status; p.Meta.Status = status; p.Reason = &reason }
	if record == nil {
		setReason("pending", "not_loaded")
		return p
	}
	if record.Missing {
		setReason("unavailable", "CHARACTER_UNAVAILABLE")
		return p
	}
	if record.Blocked {
		setReason("unavailable", "PROVIDER_ACCESS_DENIED")
		return p
	}
	if record.DataValidUntil != nil && !now.Before(*record.DataValidUntil) || record.Realm.FetchedAt.IsZero() || !now.Before(record.Realm.FetchedAt.Add(720*time.Hour)) {
		setReason("unavailable", "expired")
		return p
	}
	if record.LastJob != nil && record.LastJob.Active() {
		p.Meta.JobID = &record.LastJob.ID
	}
	snap := record.Snapshot
	if snap == nil {
		if record.LastJob != nil && !record.LastJob.Active() && record.LastJob.ErrorCode != nil {
			setReason("unavailable", *record.LastJob.ErrorCode)
		} else {
			setReason("pending", "not_loaded")
		}
		return p
	}
	profile := snap.Sections["profile"]
	if !now.Before(snap.ValidUntil) || profile.State != "complete" || !profile.Publishable(now) {
		setReason("unavailable", "expired")
		return p
	}
	var data map[string]any
	if json.Unmarshal(profile.Payload, &data) != nil || data == nil {
		setReason("unavailable", "invalid_response")
		return p
	}
	data["id"], data["realm"], data["name_normalized"] = record.ID, record.Realm.DTO(locale), key.Name
	profileJSON, err := json.Marshal(data)
	if err != nil {
		setReason("unavailable", "invalid_response")
		return p
	}
	p.Data = &CharacterData{Profile: profileJSON}
	p.Status = "fresh"
	p.Meta.Status = "fresh"
	p.Meta.SnapshotID = &snap.ID
	p.Meta.Patch = snap.Patch
	p.Meta.Season = snap.Season
	p.Meta.FetchedAt = profile.FetchedAt
	until := snap.ValidUntil
	if profile.ValidUntil.Before(until) {
		until = *profile.ValidUntil
	}
	targets := map[string]*json.RawMessage{"equipment": &p.Data.Equipment, "statistics": &p.Data.Statistics, "talents": &p.Data.Talents, "mythic_plus": &p.Data.MythicPlus, "raids": &p.Data.Raids}
	for _, section := range character.AllSections {
		v, exists := snap.Sections[section]
		if !exists {
			v = character.Unavailable("never_fetched", now)
		}
		if !v.Publishable(now) {
			if v.State != "unavailable" {
				v = character.Unavailable("expired", now)
			}
		} else {
			if !v.Fresh(now) {
				p.Status = "stale"
				p.Meta.Status = "stale"
			}
			if v.ValidUntil.Before(until) {
				until = *v.ValidUntil
			}
			if target, ok := targets[section]; ok {
				*target = append(json.RawMessage(nil), v.Payload...)
			}
		}
		p.Meta.Coverage[section] = v.Coverage()
	}
	p.Meta.ValidUntil = &until
	return p
}

type Compatibility struct {
	SamePatch            *bool    `json:"same_patch"`
	SameSpecialization   *bool    `json:"same_specialization"`
	MythicPlusSameSeason *bool    `json:"mythic_plus_same_season"`
	CombatComparable     bool     `json:"combat_comparable"`
	Reasons              []string `json:"reasons"`
}
type Comparison struct {
	GeneratedAt   time.Time             `json:"generated_at"`
	Entries       []CharacterProjection `json:"entries"`
	Compatibility Compatibility         `json:"compatibility"`
}
type ComparisonStore interface {
	Store
	CharacterBatchStore
}
type ComparisonService struct {
	Store ComparisonStore
	Mode  string
	Now   func() time.Time
}

func (s *ComparisonService) Read(ctx context.Context, input []guild.CharacterKey, locale string) (Result, error) {
	if len(input) < 1 || len(input) > 5 || locale != "ru-RU" && locale != "en-US" {
		return Result{}, character.NewFault("INVALID_ARGUMENT", false)
	}
	keys := make([]character.Key, 0, len(input))
	seen := map[string]bool{}
	for _, v := range input {
		k, e := character.NormalizeKey(v.Region, v.Realm, v.Name)
		if e != nil {
			return Result{}, e
		}
		id := k.Region + "\x00" + k.Realm + "\x00" + k.Name
		if seen[id] {
			return Result{}, character.NewFault("INVALID_ARGUMENT", false)
		}
		seen[id] = true
		keys = append(keys, k)
	}
	if s.Store == nil || s.Mode != "fixture" && s.Mode != "blizzard" {
		return Result{}, character.NewFault("INFRASTRUCTURE_UNAVAILABLE", true)
	}
	records, err := s.Store.FindMany(ctx, keys)
	if err != nil {
		return Result{}, err
	}
	if len(records) != len(keys) {
		return Result{}, character.NewFault("INFRASTRUCTURE_UNAVAILABLE", true)
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	result := Comparison{GeneratedAt: now, Entries: make([]CharacterProjection, 0, len(keys))}
	meta := NewMeta("system", locale, "fresh")
	meta.FetchedAt = &now
	available := 0
	for i, key := range keys {
		p := ProjectCharacter(records[i], key, locale, s.Mode, now)
		reason := "cache_miss"
		if p.Data != nil {
			reason = "stale_read"
		}
		if p.Status != "fresh" && (records[i] == nil || !records[i].Missing) && p.Meta.JobID == nil {
			if _, e := s.Store.ResolveRealm(ctx, key.Region, key.Realm); e != nil {
				fault := character.AsFault(mapRealmError(e))
				p.Status = "unavailable"
				p.Meta.Status = "unavailable"
				p.Reason = &fault.Code
			} else if queued, e := s.Store.Enqueue(ctx, key, reason, false); e == nil {
				p.Meta.JobID = &queued.Job.ID
				if p.Data == nil {
					p.Status = "pending"
					p.Meta.Status = "pending"
				}
			} else if p.Data == nil {
				f := character.AsFault(e)
				p.Status = "unavailable"
				p.Meta.Status = "unavailable"
				p.Reason = &f.Code
				p.Meta.JobID = f.JobID
			}
		}
		if p.Status != "fresh" {
			meta.Status = "stale"
		}
		if p.Data != nil {
			available++
			if p.Meta.ValidUntil != nil && (meta.ValidUntil == nil || p.Meta.ValidUntil.Before(*meta.ValidUntil)) {
				at := *p.Meta.ValidUntil
				meta.ValidUntil = &at
			}
		}
		result.Entries = append(result.Entries, p)
	}
	if available == 0 {
		meta.Status = "unavailable"
	}
	result.Compatibility = CompareContext(result.Entries)
	return Result{Data: result, Meta: meta}, nil
}

func CompareContext(entries []CharacterProjection) Compatibility {
	c := Compatibility{Reasons: []string{"COMBAT_CONTEXT_UNAVAILABLE"}}
	patches, specs, seasons := []string{}, []string{}, []string{}
	for _, entry := range entries {
		patch, spec, season := "", "", ""
		if entry.Data != nil {
			if entry.Meta.Patch != nil {
				patch = *entry.Meta.Patch
			}
			var p struct {
				Class struct {
					ID string `json:"id"`
				} `json:"character_class"`
				Spec *struct {
					ID string `json:"id"`
				} `json:"active_specialization"`
			}
			if json.Unmarshal(entry.Data.Profile, &p) == nil && p.Spec != nil && p.Class.ID != "" && p.Spec.ID != "" {
				spec = p.Class.ID + "/" + p.Spec.ID
			}
			var m struct {
				Season *struct {
					ID string `json:"id"`
				} `json:"season"`
				Rating *float64 `json:"rating"`
			}
			if json.Unmarshal(entry.Data.MythicPlus, &m) == nil && m.Season != nil && m.Season.ID != "" {
				season = m.Season.ID
			}
		}
		patches = append(patches, patch)
		specs = append(specs, spec)
		seasons = append(seasons, season)
	}
	compare := func(values []string, unknown, mismatch string) *bool {
		if len(values) == 0 {
			return nil
		}
		for _, v := range values {
			if v == "" {
				c.Reasons = append(c.Reasons, unknown)
				return nil
			}
		}
		same := true
		for _, v := range values {
			if v != values[0] {
				same = false
			}
		}
		if !same {
			c.Reasons = append(c.Reasons, mismatch)
		}
		return &same
	}
	c.SamePatch = compare(patches, "PATCH_UNKNOWN", "PATCH_MISMATCH")
	c.SameSpecialization = compare(specs, "SPECIALIZATION_UNKNOWN", "SPECIALIZATION_MISMATCH")
	c.MythicPlusSameSeason = compare(seasons, "MYTHIC_SEASON_UNKNOWN", "MYTHIC_SEASON_MISMATCH")
	return c
}
