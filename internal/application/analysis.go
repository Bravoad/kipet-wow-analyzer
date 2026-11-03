package application

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"kipet-wow-analyzer/internal/domain/analysis"
	"kipet-wow-analyzer/internal/domain/character"
)

type AnalysisService struct {
	Characters *CharacterService
	Store      Store
	Repository AnalysisRepository
	Engine     *analysis.Engine
	Mode       string
	Now        func() time.Time
}

func (s *AnalysisService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Read evaluates a current immutable snapshot. It never fetches a provider or
// uses an analysis cache as independent permission to publish character data.
func (s *AnalysisService) Read(ctx context.Context, key character.Key, locale, content string) (Result, error) {
	if !analysis.ValidContent(content) {
		return Result{}, character.NewFault("INVALID_ARGUMENT", false)
	}
	base, err := s.Characters.Read(ctx, key, locale, "profile")
	if err != nil || base.Pending {
		return base, err
	}
	if s.Engine == nil {
		return Result{}, character.NewFault("INFRASTRUCTURE_UNAVAILABLE", true)
	}
	record, err := s.Store.Find(ctx, key)
	if err != nil {
		if errors.Is(err, character.ErrNotFound) {
			return Result{}, character.NewFault("CHARACTER_UNAVAILABLE", false)
		}
		return Result{}, err
	}
	if record == nil || record.Blocked {
		return Result{}, character.NewFault("PROVIDER_ACCESS_DENIED", true)
	}
	now := s.now()
	if record.Snapshot == nil || !now.Before(record.Snapshot.ValidUntil) || !record.Snapshot.Sections["profile"].Publishable(now) {
		return Result{}, character.NewFault("PROVIDER_UNAVAILABLE", true)
	}
	snapshot := *record.Snapshot
	snapshot.Sections = make(map[string]character.Section, len(character.Sections))
	meta := NewMeta("blizzard", locale, "fresh")
	if s.Mode == "fixture" {
		meta.Source = "fixture"
	}
	meta.SnapshotID = &snapshot.ID
	meta.Patch = snapshot.Patch
	meta.Season = snapshot.Season
	validUntil := snapshot.ValidUntil
	cacheUntil := snapshot.ValidUntil
	needsRefresh := false
	for _, name := range character.Sections {
		section, ok := record.Snapshot.Sections[name]
		if !ok {
			section = character.Unavailable("never_fetched", now)
		}
		if !section.Fresh(now) && !(section.State == "unavailable" && now.Before(section.CheckedAt.Add(sectionTTL(name)))) {
			needsRefresh = true
		}
		if section.State != "unavailable" && !section.Publishable(now) {
			section = character.Unavailable("expired", now)
		}
		snapshot.Sections[name] = section
		meta.Coverage[name] = section.Coverage()
		if section.Publishable(now) {
			if meta.FetchedAt == nil || section.FetchedAt.Before(*meta.FetchedAt) {
				at := *section.FetchedAt
				meta.FetchedAt = &at
			}
			if section.ValidUntil.Before(validUntil) {
				validUntil = *section.ValidUntil
			}
			if section.ValidUntil.Before(cacheUntil) {
				cacheUntil = *section.ValidUntil
			}
			if section.FreshUntil != nil && section.FreshUntil.After(now) && section.FreshUntil.Before(cacheUntil) {
				cacheUntil = *section.FreshUntil
			}
			if !section.Fresh(now) {
				meta.Status = "stale"
			}
		}
	}
	meta.ValidUntil = &validUntil
	if record.LastJob != nil && record.LastJob.Active() {
		meta.JobID = &record.LastJob.ID
	}
	if needsRefresh && meta.JobID == nil {
		if queued, e := s.Store.Enqueue(ctx, key, "stale_read", false); e == nil {
			meta.JobID = &queued.Job.ID
		}
	}
	if s.Repository != nil {
		if cached, e := s.Repository.LoadAnalysis(ctx, snapshot.ID, s.Engine.CatalogDigest(), content); e == nil {
			var report analysis.Report
			if json.Unmarshal(cached, &report) == nil && report.SnapshotID == snapshot.ID && report.Source == meta.Source && report.Context.Content == content && report.Results != nil && report.Diagnostics != nil {
				return Result{Data: report, Meta: meta}, nil
			}
		}
	}
	report := s.Engine.Analyze(analysis.Input{Snapshot: snapshot, Source: meta.Source, Content: content, Now: now})
	if s.Repository != nil && cacheUntil.After(now) {
		if payload, e := json.Marshal(report); e == nil {
			_ = s.Repository.SaveAnalysis(ctx, snapshot.ID, s.Engine.CatalogDigest(), content, payload, cacheUntil)
		}
	}
	return Result{Data: report, Meta: meta}, nil
}
