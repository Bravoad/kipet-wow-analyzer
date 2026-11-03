package application

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/combat"
)

var reportCodePattern = regexp.MustCompile(`^[A-Za-z0-9]{16}$`)

func ValidReportCode(code string) bool { return reportCodePattern.MatchString(code) }

type CombatProvider interface {
	Report(context.Context, string) (combat.Report, error)
	Collect(context.Context, combat.Report, int, int) (combat.Collection, error)
}

type CombatResource struct {
	ID                                string
	Payload                           json.RawMessage
	FetchedAt, FreshUntil, ValidUntil time.Time
}
type CombatReport struct {
	CombatResource
	Blocked bool
}
type CombatLease struct {
	Job              character.Job
	Code             string
	FightID, ActorID int
	ReportVersion    *string
	Token            string
	ExpiresAt        time.Time
}
type CombatRepository interface {
	LookupCombat(context.Context, string) (*CombatReport, error)
	LoadCombatAnalysis(context.Context, string, string, int, int) (*CombatResource, error)
	AdmitCombat(context.Context, string, int, int) (character.Job, error)
	GetCombatJob(context.Context, string) (character.Job, error)
	ClaimCombat(context.Context, string) (*CombatLease, error)
	RenewCombat(context.Context, CombatLease) error
	StartCombatAttempt(context.Context, CombatLease) error
	SaveCombatReport(context.Context, CombatLease, combat.Report, bool) (*CombatResource, error)
	SaveCombatAnalysis(context.Context, CombatLease, string, combat.Analysis) error
	FailCombat(context.Context, CombatLease, *character.Fault, time.Time) error
	QuarantineCombat(context.Context, CombatLease) error
	PurgeCombat(context.Context, CombatLease) error
	MaintainCombat(context.Context) error
}

type CombatService struct {
	Repository CombatRepository
	Mode       string
	Now        func() time.Time
}

func (s *CombatService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s *CombatService) enabled(code, locale string) error {
	if !ValidReportCode(code) || locale != "ru-RU" && locale != "en-US" {
		return character.NewFault("INVALID_ARGUMENT", false)
	}
	if s.Repository == nil || s.Mode != "fixture" && s.Mode != "warcraftlogs" {
		return character.NewFault("COMBAT_NOT_CONFIGURED", false)
	}
	return nil
}
func (s *CombatService) pending(ctx context.Context, code string, fightID, actorID int, locale string) (Result, error) {
	job, err := s.Repository.AdmitCombat(ctx, code, fightID, actorID)
	if err != nil {
		return Result{}, err
	}
	meta := NewMeta(s.Mode, locale, "pending")
	meta.JobID = &job.ID
	return Result{Meta: meta, Pending: true}, nil
}
func (s *CombatService) report(ctx context.Context, code, locale string) (*CombatReport, *Result, error) {
	if err := s.enabled(code, locale); err != nil {
		return nil, nil, err
	}
	r, err := s.Repository.LookupCombat(ctx, code)
	if err != nil && !errors.Is(err, character.ErrNotFound) {
		return nil, nil, err
	}
	if r != nil && r.Blocked {
		fault := character.NewFault("PROVIDER_ACCESS_DENIED", true)
		if job, e := s.Repository.AdmitCombat(ctx, code, 0, 0); e == nil {
			fault.JobID = &job.ID
		} else {
			prior := character.AsFault(e)
			fault.JobID = prior.JobID
			fault.RetryAt = prior.RetryAt
		}
		return nil, nil, fault
	}
	// Access verification has a short freshness deadline: cached analyses never
	// outlive a public-report check, even if their retention window is longer.
	if r == nil || len(r.Payload) == 0 || !s.now().Before(r.FreshUntil) || !s.now().Before(r.ValidUntil) {
		pending, err := s.pending(ctx, code, 0, 0, locale)
		return nil, &pending, err
	}
	var observed combat.Report
	if json.Unmarshal(r.Payload, &observed) != nil || observed.Code != code || observed.Visibility != "public" {
		return nil, nil, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	return r, nil, nil
}
func (s *CombatService) meta(r *CombatResource, locale, section string) Meta {
	m := NewMeta(s.Mode, locale, "fresh")
	m.SnapshotID = &r.ID
	m.FetchedAt = &r.FetchedAt
	m.ValidUntil = &r.ValidUntil
	m.Coverage[section] = map[string]any{"state": "complete", "fetched_at": r.FetchedAt, "fresh_until": r.FreshUntil, "valid_until": r.ValidUntil, "reason": nil}
	return m
}
func (s *CombatService) reportMeta(r *CombatReport, locale string) Meta {
	meta := s.meta(&r.CombatResource, locale, "report")
	var observed combat.Report
	if json.Unmarshal(r.Payload, &observed) == nil && (observed.Fights == nil || observed.Actors == nil) {
		coverage := meta.Coverage["report"].(map[string]any)
		coverage["state"] = "partial"
		coverage["reason"] = "not_provided"
	}
	return meta
}
func (s *CombatService) ReadReport(ctx context.Context, code, locale string) (Result, error) {
	r, pending, err := s.report(ctx, code, locale)
	if err != nil {
		return Result{}, err
	}
	if pending != nil {
		return *pending, nil
	}
	return Result{Data: r.Payload, Meta: s.reportMeta(r, locale)}, nil
}
func (s *CombatService) ReadFights(ctx context.Context, code, locale string) (Result, error) {
	r, pending, err := s.report(ctx, code, locale)
	if err != nil {
		return Result{}, err
	}
	if pending != nil {
		return *pending, nil
	}
	var report combat.Report
	if json.Unmarshal(r.Payload, &report) != nil {
		return Result{}, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	return Result{Data: map[string]any{"report_code": code, "fights": report.Fights, "actors": report.Actors}, Meta: s.reportMeta(r, locale)}, nil
}
func (s *CombatService) ReadAnalysis(ctx context.Context, code string, fightID, actorID int, locale string) (Result, error) {
	if fightID < 1 || fightID > 2147483647 || actorID < 1 || actorID > 2147483647 {
		return Result{}, character.NewFault("INVALID_ARGUMENT", false)
	}
	r, pending, err := s.report(ctx, code, locale)
	if err != nil {
		return Result{}, err
	}
	if pending != nil {
		return *pending, nil
	}
	var report combat.Report
	if json.Unmarshal(r.Payload, &report) != nil {
		return Result{}, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	if !CombatParticipant(report, fightID, actorID) {
		return Result{}, character.NewFault("INVALID_ARGUMENT", false)
	}
	resource, err := s.Repository.LoadCombatAnalysis(ctx, code, r.ID, fightID, actorID)
	if err != nil && !errors.Is(err, character.ErrNotFound) {
		return Result{}, err
	}
	if resource == nil || !s.now().Before(resource.FreshUntil) || !s.now().Before(resource.ValidUntil) {
		return s.pending(ctx, code, fightID, actorID, locale)
	}
	meta := s.meta(resource, locale, "combat")
	meta.SnapshotID = &r.ID
	var analysis combat.Analysis
	if json.Unmarshal(resource.Payload, &analysis) != nil || analysis.ReportCode != code || analysis.FightID != fightID || analysis.ActorID != actorID {
		return Result{}, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	if !analysis.EventsComplete || analysis.TimelineTruncated {
		reason := "not_provided"
		meta.Coverage["combat"] = map[string]any{"state": "partial", "fetched_at": resource.FetchedAt, "fresh_until": resource.FreshUntil, "valid_until": resource.ValidUntil, "reason": reason}
	}
	return Result{Data: resource.Payload, Meta: meta}, nil
}
func CombatParticipant(report combat.Report, fightID, actorID int) bool {
	found := false
	for _, fight := range report.Fights {
		if fight.ID == fightID {
			if fight.ParticipantIDs != nil {
				member := false
				for _, id := range fight.ParticipantIDs {
					if id == actorID {
						member = true
						break
					}
				}
				if !member {
					return false
				}
			}
			found = true
			break
		}
	}
	if !found {
		return false
	}
	for _, actor := range report.Actors {
		if actor.ID == actorID && actor.Type == "player" {
			return true
		}
	}
	return false
}
func (s *CombatService) GetJob(ctx context.Context, id string) (character.Job, error) {
	if s.Repository == nil {
		return character.Job{}, character.ErrNotFound
	}
	return s.Repository.GetCombatJob(ctx, id)
}
