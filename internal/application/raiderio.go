package application

import (
	"context"
	"errors"
	"net/url"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/raiderio"
)

const RaiderJobKind = "refresh_raiderio"

type RaiderRecord struct {
	Version  string
	Snapshot raiderio.Snapshot
	Blocked  bool
	Job      *character.Job
}

type RaiderLease struct {
	Job              character.Job
	Key              raiderio.Key
	ProfileID, Token string
}

type RaiderRepository interface {
	LookupRaider(context.Context, raiderio.Key) (*RaiderRecord, error)
	AdmitRaider(context.Context, raiderio.Key) (character.Job, error)
	GetRaiderJob(context.Context, string) (character.Job, error)
	ClaimRaider(context.Context) (*RaiderLease, error)
	StartRaiderAttempt(context.Context, RaiderLease) error
	SaveRaider(context.Context, RaiderLease, raiderio.Snapshot) error
	FailRaider(context.Context, RaiderLease, *character.Fault, time.Time) error
	PurgeRaider(context.Context, RaiderLease) error
	MaintainRaider(context.Context) error
}

type RaiderService struct {
	Repository RaiderRepository
}

func (s *RaiderService) Read(ctx context.Context, key raiderio.Key, locale string) (Result, error) {
	record, err := s.Repository.LookupRaider(ctx, key)
	if err != nil && !errors.Is(err, character.ErrNotFound) {
		return Result{}, err
	}
	if record != nil && record.Blocked {
		fault := character.NewFault("PROVIDER_ACCESS_DENIED", true)
		if job, err := s.Repository.AdmitRaider(ctx, key); err == nil {
			fault.JobID = &job.ID
		} else {
			admission := character.AsFault(err)
			fault.JobID, fault.RetryAt = admission.JobID, admission.RetryAt
		}
		return Result{}, fault
	}
	now := time.Now().UTC()
	if record == nil || record.Version == "" || !now.Before(record.Snapshot.ValidUntil) {
		return s.Refresh(ctx, key, locale)
	}
	snapshot := record.Snapshot
	profileURL := &snapshot.Profile.ProfileURL
	if key.Kind == "guild" {
		if snapshot.Guild == nil {
			return Result{}, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
		}
		guild := *snapshot.Guild
		snapshot.Guild = &guild
		profileURL = &guild.ProfileURL
	}
	link, err := url.Parse(*profileURL)
	if err != nil {
		return Result{}, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	*profileURL = link.String()
	meta := NewMeta("raiderio", locale, "fresh")
	meta.SnapshotID = &record.Version
	meta.FetchedAt = &snapshot.FetchedAt
	meta.ValidUntil = &snapshot.ValidUntil
	if !now.Before(snapshot.FreshUntil) {
		meta.Status = "stale"
		if job, err := s.Repository.AdmitRaider(ctx, key); err == nil {
			record.Job = &job
		}
	}
	if record.Job != nil && record.Job.Active() {
		meta.JobID = &record.Job.ID
	}
	if key.Kind == "guild" {
		return Result{Meta: meta, Data: *snapshot.Guild}, nil
	}
	return Result{Meta: meta, Data: snapshot.Profile}, nil
}

func (s *RaiderService) Refresh(ctx context.Context, key raiderio.Key, locale string) (Result, error) {
	job, err := s.Repository.AdmitRaider(ctx, key)
	if err != nil {
		return Result{}, err
	}
	meta := NewMeta("raiderio", locale, "pending")
	meta.JobID = &job.ID
	return Result{Pending: true, Meta: meta}, nil
}

func (s *RaiderService) GetJob(ctx context.Context, id string) (character.Job, error) {
	return s.Repository.GetRaiderJob(ctx, id)
}
