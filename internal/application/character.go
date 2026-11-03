package application

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
)

type Meta struct {
	Source     string         `json:"source"`
	FetchedAt  *time.Time     `json:"fetched_at"`
	Status     string         `json:"status"`
	SnapshotID *string        `json:"snapshot_id"`
	ValidUntil *time.Time     `json:"valid_until"`
	Patch      *string        `json:"patch"`
	Season     *string        `json:"season"`
	Locale     string         `json:"locale"`
	Coverage   map[string]any `json:"coverage"`
	JobID      *string        `json:"job_id"`
}

type Result struct {
	Data    any
	Meta    Meta
	Pending bool
}
type CharacterService struct {
	Store Store
	Cache SnapshotCache
	Now   func() time.Time
}

func (s *CharacterService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func NewMeta(source, locale, status string) Meta {
	return Meta{Source: source, Locale: locale, Status: status, Coverage: map[string]any{}}
}

func (s *CharacterService) Read(ctx context.Context, key character.Key, locale, section string) (Result, error) {
	validSection := false
	for _, name := range character.AllSections {
		if name == section {
			validSection = true
			break
		}
	}
	if !validSection {
		return Result{}, character.NewFault("INVALID_ARGUMENT", false)
	}
	if _, err := s.Store.ResolveRealm(ctx, key.Region, key.Realm); err != nil {
		return Result{}, mapRealmError(err)
	}
	missing, err := s.Store.IsMissing(ctx, key)
	if err != nil {
		return Result{}, err
	}
	if missing {
		return Result{}, character.NewFault("CHARACTER_UNAVAILABLE", false)
	}
	record, err := s.Store.Find(ctx, key)
	if err != nil && !errors.Is(err, character.ErrNotFound) {
		return Result{}, err
	}
	if record != nil && record.Blocked {
		fault := character.NewFault("PROVIDER_ACCESS_DENIED", true)
		if queued, e := s.Store.Enqueue(ctx, key, "stale_read", false); e == nil {
			fault.JobID = &queued.Job.ID
		} else {
			prior := character.AsFault(e)
			fault.RetryAt = prior.RetryAt
			fault.JobID = prior.JobID
		}
		return Result{}, fault
	}
	now := s.now()
	if record == nil || record.Snapshot == nil || !now.Before(record.Snapshot.ValidUntil) || !record.Snapshot.Sections["profile"].Publishable(now) {
		job, err := s.Store.Enqueue(ctx, key, "cache_miss", false)
		if err != nil {
			return Result{}, err
		}
		meta := NewMeta("blizzard", locale, "pending")
		meta.JobID = &job.Job.ID
		return Result{Data: nil, Meta: meta, Pending: true}, nil
	}
	snapshot := record.Snapshot
	wanted, exists := snapshot.Sections[section]
	if !exists {
		wanted = character.Unavailable("never_fetched", now)
	}
	needsRefresh := !wanted.Fresh(now)
	if wanted.State == "unavailable" && (wanted.Reason == nil || *wanted.Reason != "never_fetched") && now.Before(wanted.CheckedAt.Add(sectionTTL(section))) {
		needsRefresh = false
	}
	var jobID *string
	if record.LastJob != nil && record.LastJob.Active() {
		jobID = &record.LastJob.ID
	}
	if needsRefresh && jobID == nil {
		if queued, e := s.Store.Enqueue(ctx, key, "stale_read", false); e == nil {
			jobID = &queued.Job.ID
		}
	}
	meta := NewMeta("blizzard", locale, "fresh")
	meta.JobID = jobID
	meta.SnapshotID = &snapshot.ID
	meta.Patch = snapshot.Patch
	meta.Season = snapshot.Season
	// Progress carries its own observed season, never the rule-engine context.
	if section == "mythic_plus" || section == "raids" {
		meta.Season = nil
	}
	for _, name := range character.AllSections {
		v, exists := snapshot.Sections[name]
		if !exists {
			v = character.Unavailable("never_fetched", now)
		}
		if v.State != "unavailable" && !v.Publishable(now) {
			v = character.Unavailable("expired", now)
		}
		meta.Coverage[name] = v.Coverage()
	}
	if !wanted.Publishable(now) {
		meta.Status = "unavailable"
		return Result{Data: nil, Meta: meta}, nil
	}
	meta.FetchedAt = wanted.FetchedAt
	until := *wanted.ValidUntil
	if snapshot.ValidUntil.Before(until) {
		until = snapshot.ValidUntil
	}
	meta.ValidUntil = &until
	if !wanted.Fresh(now) {
		meta.Status = "stale"
	}
	payload := wanted.Payload
	if s.Cache != nil {
		if cached, e := s.Cache.Get(ctx, snapshot.ID, section, locale); e == nil && json.Valid(cached) {
			payload = cached
		}
	}
	// Only the current PostgreSQL-authorized snapshot can reach this point.
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		return Result{}, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	if section == "profile" {
		data["realm"] = record.Realm.DTO(locale)
		data["id"] = record.ID
		data["name_normalized"] = key.Name
	}
	if section == "mythic_plus" {
		var observed struct {
			Season *struct {
				ID string `json:"id"`
			} `json:"season"`
		}
		if err := json.Unmarshal(payload, &observed); err != nil {
			return Result{}, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
		}
		if observed.Season != nil {
			meta.Season = &observed.Season.ID
		}
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return Result{}, err
	}
	if s.Cache != nil {
		_ = s.Cache.Put(ctx, snapshot.ID, section, locale, encoded, until)
	}
	return Result{Data: json.RawMessage(encoded), Meta: meta}, nil
}

func (s *CharacterService) Refresh(ctx context.Context, key character.Key, locale string) (Result, error) {
	if _, err := s.Store.ResolveRealm(ctx, key.Region, key.Realm); err != nil {
		return Result{}, mapRealmError(err)
	}
	missing, err := s.Store.IsMissing(ctx, key)
	if err != nil {
		return Result{}, err
	}
	if missing {
		return Result{}, character.NewFault("CHARACTER_UNAVAILABLE", false)
	}
	r, err := s.Store.Find(ctx, key)
	if err != nil && !errors.Is(err, character.ErrNotFound) {
		return Result{}, err
	}
	// Quarantine hides the payload while an allowed refresh may revalidate it.
	fresh := r != nil && !r.Blocked && r.Snapshot != nil && s.now().Before(r.Snapshot.ValidUntil)
	if fresh {
		for _, name := range character.AllSections {
			if !r.Snapshot.Sections[name].Fresh(s.now()) {
				fresh = false
				break
			}
		}
	}
	if fresh && (r.LastJob == nil || !r.LastJob.Active()) {
		return Result{Data: map[string]any{"disposition": "already_fresh", "job_id": nil}, Meta: NewMeta("system", locale, "fresh")}, nil
	}
	job, err := s.Store.Enqueue(ctx, key, "user_refresh", true)
	if err != nil {
		return Result{}, err
	}
	meta := NewMeta("blizzard", locale, "pending")
	meta.JobID = &job.Job.ID
	return Result{Data: nil, Meta: meta, Pending: true}, nil
}

func sectionTTL(section string) time.Duration {
	if section == "talents" {
		return 30 * time.Minute
	}
	return 15 * time.Minute
}
func mapRealmError(err error) error {
	if errors.Is(err, character.ErrNotFound) {
		return character.NewFault("REALM_NOT_FOUND", false)
	}
	return err
}
