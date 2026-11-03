package application

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"kipet-wow-analyzer/internal/domain/analysis"
	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/guild"
)

type GuildProvider interface {
	FetchGuild(context.Context, guild.Key, character.Realm) (guild.Bundle, error)
}
type GuildRecord struct {
	ID, GuildID                       string
	Key                               guild.Key
	Realm                             character.Realm
	Payload                           json.RawMessage
	FetchedAt, FreshUntil, ValidUntil time.Time
	Blocked                           bool
	LastJob                           *character.Job
}
type GuildLease struct {
	Job            character.Job
	Key            guild.Key
	GuildID, Token string
	SnapshotID     *string
	Cursor         int
	ExpiresAt      time.Time
}
type GuildRepository interface {
	LookupGuild(context.Context, guild.Key) (*GuildRecord, error)
	AdmitGuild(context.Context, guild.Key, bool) (character.Job, error)
	GetGuildJob(context.Context, string) (character.Job, error)
	ClaimGuild(context.Context, string) (*GuildLease, error)
	RenewGuild(context.Context, GuildLease) error
	StartGuildAttempt(context.Context, GuildLease) error
	SaveGuild(context.Context, GuildLease, guild.Bundle) (*GuildRecord, error)
	ScheduleGuildChunk(context.Context, GuildLease) (int, bool, error)
	FailGuild(context.Context, GuildLease, *character.Fault, time.Time) error
	QuarantineGuild(context.Context, GuildLease) error
	PurgeGuild(context.Context, GuildLease) error
	MaintainGuild(context.Context) error
}
type GuildCharacters interface {
	Store
	CharacterBatchStore
}
type GuildService struct {
	Repository GuildRepository
	Characters GuildCharacters
	Mode       string
	Engine     *analysis.Engine
	Now        func() time.Time
}

func (s *GuildService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s *GuildService) validate(key guild.Key, locale string) error {
	normalized, err := guild.NormalizeKey(key.Region, key.Realm, key.Slug)
	if err != nil || normalized != key || locale != "ru-RU" && locale != "en-US" {
		return character.NewFault("INVALID_ARGUMENT", false)
	}
	if s.Repository == nil || s.Characters == nil || (s.Mode != "fixture" && s.Mode != "blizzard") {
		return character.NewFault("INFRASTRUCTURE_UNAVAILABLE", true)
	}
	return nil
}
func (s *GuildService) enqueue(ctx context.Context, key guild.Key, locale string, manual bool) (Result, error) {
	job, err := s.Repository.AdmitGuild(ctx, key, manual)
	if err != nil {
		return Result{}, err
	}
	m := NewMeta(s.Mode, locale, "pending")
	m.JobID = &job.ID
	return Result{Meta: m, Pending: true}, nil
}
func (s *GuildService) read(ctx context.Context, key guild.Key, locale string) (*GuildRecord, *Result, error) {
	if err := s.validate(key, locale); err != nil {
		return nil, nil, err
	}
	if _, err := s.Characters.ResolveRealm(ctx, key.Region, key.Realm); err != nil {
		return nil, nil, mapRealmError(err)
	}
	r, err := s.Repository.LookupGuild(ctx, key)
	if err != nil && !errors.Is(err, character.ErrNotFound) {
		return nil, nil, err
	}
	if r != nil && r.Blocked {
		fault := character.NewFault("PROVIDER_ACCESS_DENIED", true)
		if job, e := s.Repository.AdmitGuild(ctx, key, false); e == nil {
			fault.JobID = &job.ID
		} else {
			prior := character.AsFault(e)
			fault.JobID = prior.JobID
			fault.RetryAt = prior.RetryAt
		}
		return nil, nil, fault
	}
	if r == nil || len(r.Payload) == 0 || !s.now().Before(r.ValidUntil) {
		p, e := s.enqueue(ctx, key, locale, false)
		return nil, &p, e
	}
	if !s.now().Before(r.FreshUntil) {
		if job, e := s.Repository.AdmitGuild(ctx, key, false); e == nil {
			r.LastJob = &job
		}
	}
	return r, nil, nil
}
func (s *GuildService) meta(r *GuildRecord, locale string, bundle guild.Bundle) Meta {
	m := NewMeta(s.Mode, locale, "fresh")
	m.SnapshotID = &r.ID
	m.FetchedAt = &r.FetchedAt
	m.ValidUntil = &r.ValidUntil
	if !s.now().Before(r.FreshUntil) {
		m.Status = "stale"
	}
	if r.LastJob != nil && r.LastJob.Active() {
		m.JobID = &r.LastJob.ID
	}
	for _, section := range []string{"guild", "roster"} {
		state := "complete"
		var reason any
		if section == "roster" && (bundle.Roster.Members == nil || bundle.Roster.ReportedCount != nil && *bundle.Roster.ReportedCount != len(bundle.Roster.Members)) {
			state = "partial"
			reason = "not_provided"
		}
		m.Coverage[section] = map[string]any{"state": state, "fetched_at": r.FetchedAt, "fresh_until": r.FreshUntil, "valid_until": r.ValidUntil, "reason": reason}
	}
	return m
}
func decodeGuild(r *GuildRecord, locale string) (guild.Bundle, error) {
	var b guild.Bundle
	if json.Unmarshal(r.Payload, &b) != nil || b.Profile.ID != r.GuildID || b.Roster.GuildID != r.GuildID || b.Profile.Region != r.Key.Region || b.Profile.Slug != r.Key.Slug {
		return b, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	b.Profile.Realm = r.Realm.DTO(locale)
	return b, nil
}
func (s *GuildService) ReadProfile(ctx context.Context, key guild.Key, locale string) (Result, error) {
	r, p, err := s.read(ctx, key, locale)
	if err != nil {
		return Result{}, err
	}
	if p != nil {
		return *p, nil
	}
	b, err := decodeGuild(r, locale)
	if err != nil {
		return Result{}, err
	}
	return Result{Data: b.Profile, Meta: s.meta(r, locale, b)}, nil
}
func (s *GuildService) ReadRoster(ctx context.Context, key guild.Key, locale string) (Result, error) {
	r, p, err := s.read(ctx, key, locale)
	if err != nil {
		return Result{}, err
	}
	if p != nil {
		return *p, nil
	}
	b, err := decodeGuild(r, locale)
	if err != nil {
		return Result{}, err
	}
	return Result{Data: b.Roster, Meta: s.meta(r, locale, b)}, nil
}
func (s *GuildService) ReadAnalysis(ctx context.Context, key guild.Key, locale string) (Result, error) {
	r, p, err := s.read(ctx, key, locale)
	if err != nil {
		return Result{}, err
	}
	if p != nil {
		return *p, nil
	}
	b, err := decodeGuild(r, locale)
	if err != nil {
		return Result{}, err
	}
	keys := make([]character.Key, 0, len(b.Roster.Members))
	for _, member := range b.Roster.Members {
		k, e := character.NormalizeKey(member.Key.Region, member.Key.Realm, member.Key.Name)
		if e != nil {
			return Result{}, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
		}
		keys = append(keys, k)
	}
	records, err := s.Characters.FindMany(ctx, keys)
	if err != nil {
		return Result{}, err
	}
	if len(records) != len(keys) {
		return Result{}, character.NewFault("INFRASTRUCTURE_UNAVAILABLE", true)
	}
	observations := make([]guild.Observation, 0, len(keys))
	now := s.now()
	var memberDeadline *time.Time
	needsMemberRefresh := false
	for i, record := range records {
		projection := ProjectCharacter(record, keys[i], locale, s.Mode, now)
		// A guild snapshot can outlive a member's negative-cache entry or
		// profile freshness. Re-admit one bounded roster-scheduling job; the
		// worker still owns per-member cooldowns, capacity and deduplication.
		if (projection.Status != "fresh" || record != nil && record.ProviderID != nil && *record.ProviderID != b.Roster.Members[i].ProviderID) &&
			(record == nil || !record.Missing && (record.LastJob == nil || !record.LastJob.Active())) {
			needsMemberRefresh = true
		}
		observation := guild.Observation{Member: b.Roster.Members[i], Status: projection.Status, SnapshotID: projection.Meta.SnapshotID, Sections: map[string]json.RawMessage{}}
		if projection.Data != nil {
			if projection.Meta.ValidUntil != nil && (memberDeadline == nil || projection.Meta.ValidUntil.Before(*memberDeadline)) {
				at := *projection.Meta.ValidUntil
				memberDeadline = &at
			}
			d := projection.Data
			observation.Profile = d.Profile
			observation.Sections = map[string]json.RawMessage{"equipment": d.Equipment, "statistics": d.Statistics, "talents": d.Talents, "mythic_plus": d.MythicPlus, "raids": d.Raids}
			if s.Engine != nil && record != nil && record.Snapshot != nil && record.ProviderID != nil && *record.ProviderID == b.Roster.Members[i].ProviderID {
				report := s.Engine.Analyze(analysis.Input{Snapshot: *record.Snapshot, Source: s.Mode, Content: "general_pve", Now: now})
				if report.Ruleset != nil {
					count := 0
					for _, finding := range report.Results {
						if finding.Outcome != nil && (*finding.Outcome == "Warning" || *finding.Outcome == "Critical") {
							count++
						}
					}
					observation.Notices = &count
				}
			}
		}
		observations = append(observations, observation)
	}
	analysis := guild.Analyze(b, observations)
	// A roster replacement or quarantine cannot lend authority to this batch.
	current, err := s.Repository.LookupGuild(ctx, key)
	if err != nil {
		return Result{}, err
	}
	if current.Blocked {
		return Result{}, character.NewFault("PROVIDER_ACCESS_DENIED", true)
	}
	if current.ID != r.ID || !s.now().Before(current.ValidUntil) {
		return Result{}, character.NewFault("PROVIDER_UNAVAILABLE", true)
	}
	if needsMemberRefresh {
		if job, e := s.Repository.AdmitGuild(ctx, key, false); e == nil {
			r.LastJob = &job
		}
	}
	m := s.meta(r, locale, b)
	if analysis.Coverage.Stale > 0 {
		m.Status = "stale"
	}
	if memberDeadline != nil && memberDeadline.Before(*m.ValidUntil) {
		m.ValidUntil = memberDeadline
	}
	if analysis.Coverage.Profiles < analysis.Coverage.Roster {
		v := m.Coverage["roster"].(map[string]any)
		v["state"] = "partial"
		v["reason"] = "not_provided"
	}
	return Result{Data: analysis, Meta: m}, nil
}
func (s *GuildService) Refresh(ctx context.Context, key guild.Key, locale string) (Result, error) {
	if err := s.validate(key, locale); err != nil {
		return Result{}, err
	}
	if _, err := s.Characters.ResolveRealm(ctx, key.Region, key.Realm); err != nil {
		return Result{}, mapRealmError(err)
	}
	return s.enqueue(ctx, key, locale, true)
}
func (s *GuildService) GetJob(ctx context.Context, id string) (character.Job, error) {
	if s.Repository == nil {
		return character.Job{}, character.ErrNotFound
	}
	return s.Repository.GetGuildJob(ctx, id)
}
