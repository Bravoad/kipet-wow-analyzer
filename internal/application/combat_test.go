package application

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/combat"
)

type combatReadRepo struct {
	CombatRepository
	report            *CombatReport
	analysis          *CombatResource
	lookupError       error
	admissions, loads int
	fight, actor      int
}

func (r *combatReadRepo) LookupCombat(context.Context, string) (*CombatReport, error) {
	if r.lookupError != nil {
		return nil, r.lookupError
	}
	if r.report == nil {
		return nil, character.ErrNotFound
	}
	return r.report, nil
}
func (r *combatReadRepo) AdmitCombat(_ context.Context, _ string, fight, actor int) (character.Job, error) {
	r.admissions++
	r.fight = fight
	r.actor = actor
	return character.Job{ID: "job"}, nil
}
func (r *combatReadRepo) LoadCombatAnalysis(context.Context, string, string, int, int) (*CombatResource, error) {
	r.loads++
	if r.analysis == nil {
		return nil, character.ErrNotFound
	}
	return r.analysis, nil
}
func TestCombatReadPublicationBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	const code = "DEMOCombat000001"
	report := combat.Report{Code: code, Visibility: "public", Fights: []combat.Fight{{ID: 1, ParticipantIDs: []int{2}}}, Actors: []combat.Actor{{ID: 2, Type: "player"}, {ID: 3, Type: "pet"}, {ID: 4, Type: "player"}}}
	payload, _ := json.Marshal(report)
	newRepo := func() *combatReadRepo {
		return &combatReadRepo{report: &CombatReport{CombatResource: CombatResource{ID: "version", Payload: payload, FetchedAt: now, FreshUntil: now.Add(time.Minute), ValidUntil: now.Add(time.Hour)}}}
	}
	t.Run("validation precedes queue", func(t *testing.T) {
		r := newRepo()
		s := CombatService{Repository: r, Mode: "fixture"}
		for _, bad := range []string{"bad", "https://evil.example/reports/DEMOCombat000001", "DEMOCombat00000/"} {
			_, err := s.ReadReport(ctx, bad, "ru-RU")
			if character.AsFault(err).Code != "INVALID_ARGUMENT" {
				t.Fatal(err)
			}
		}
		if r.admissions != 0 {
			t.Fatal("invalid code queued")
		}
	})
	t.Run("disabled never queues", func(t *testing.T) {
		r := newRepo()
		s := CombatService{Repository: r, Mode: "disabled"}
		_, err := s.ReadReport(ctx, code, "ru-RU")
		if character.AsFault(err).Code != "COMBAT_NOT_CONFIGURED" || r.admissions != 0 {
			t.Fatal(err)
		}
	})
	t.Run("missing report accepted durably", func(t *testing.T) {
		r := &combatReadRepo{}
		s := CombatService{Repository: r, Mode: "fixture", Now: func() time.Time { return now }}
		result, err := s.ReadReport(ctx, code, "ru-RU")
		if err != nil || !result.Pending || result.Data != nil || result.Meta.JobID == nil || r.admissions != 1 {
			t.Fatalf("%+v %v", result, err)
		}
	})
	t.Run("quarantine hides and schedules verification", func(t *testing.T) {
		r := newRepo()
		r.report.Blocked = true
		s := CombatService{Repository: r, Mode: "fixture", Now: func() time.Time { return now }}
		result, err := s.ReadReport(ctx, code, "ru-RU")
		if result.Data != nil || character.AsFault(err).Code != "PROVIDER_ACCESS_DENIED" || character.AsFault(err).JobID == nil || r.admissions != 1 {
			t.Fatalf("%+v %v", result, err)
		}
	})
	t.Run("expired access check defeats fresh analysis", func(t *testing.T) {
		r := newRepo()
		r.report.FreshUntil = now.Add(-time.Second)
		r.analysis = &CombatResource{Payload: json.RawMessage(`{"secret":"must not leak"}`), FreshUntil: now.Add(time.Hour), ValidUntil: now.Add(time.Hour)}
		s := CombatService{Repository: r, Mode: "fixture", Now: func() time.Time { return now }}
		result, err := s.ReadAnalysis(ctx, code, 1, 2, "ru-RU")
		if err != nil || !result.Pending || result.Data != nil || r.loads != 0 || r.fight != 0 {
			t.Fatalf("%+v %v", result, err)
		}
	})
	t.Run("public explicit proof required", func(t *testing.T) {
		r := newRepo()
		r.report.Payload = json.RawMessage(`{"code":"DEMOCombat000001","visibility":"unlisted"}`)
		s := CombatService{Repository: r, Mode: "fixture", Now: func() time.Time { return now }}
		result, err := s.ReadReport(ctx, code, "ru-RU")
		if result.Data != nil || character.AsFault(err).Code != "PROVIDER_INVALID_RESPONSE" {
			t.Fatal(err)
		}
	})
	t.Run("missing report collections remain partial", func(t *testing.T) {
		r := newRepo()
		r.report.Payload = json.RawMessage(`{"code":"DEMOCombat000001","visibility":"public","fights":null,"actors":[]}`)
		s := CombatService{Repository: r, Mode: "fixture", Now: func() time.Time { return now }}
		result, err := s.ReadReport(ctx, code, "ru-RU")
		if err != nil || result.Meta.Coverage["report"].(map[string]any)["state"] != "partial" {
			t.Fatalf("%+v %v", result, err)
		}
	})
	t.Run("fight and player must be listed", func(t *testing.T) {
		for _, pair := range [][2]int{{0, 2}, {99, 2}, {1, 3}, {1, 4}, {1, 99}} {
			r := newRepo()
			s := CombatService{Repository: r, Mode: "fixture", Now: func() time.Time { return now }}
			_, err := s.ReadAnalysis(ctx, code, pair[0], pair[1], "ru-RU")
			if character.AsFault(err).Code != "INVALID_ARGUMENT" || r.admissions != 0 || r.loads != 0 {
				t.Fatal(pair, err)
			}
		}
	})
	t.Run("analysis result binds complete identity", func(t *testing.T) {
		r := newRepo()
		a := combat.Analysis{ReportCode: code, FightID: 1, ActorID: 2, EventsComplete: false}
		p, _ := json.Marshal(a)
		r.analysis = &CombatResource{ID: "result", Payload: p, FetchedAt: now, FreshUntil: now.Add(time.Minute), ValidUntil: now.Add(time.Hour)}
		s := CombatService{Repository: r, Mode: "fixture", Now: func() time.Time { return now }}
		result, err := s.ReadAnalysis(ctx, code, 1, 2, "en-US")
		if err != nil || result.Data == nil || *result.Meta.SnapshotID != "version" || result.Meta.Source != "fixture" || result.Meta.Coverage["combat"].(map[string]any)["state"] != "partial" {
			t.Fatalf("%+v %v", result, err)
		}
		a.ActorID = 88
		r.analysis.Payload, _ = json.Marshal(a)
		_, err = s.ReadAnalysis(ctx, code, 1, 2, "ru-RU")
		if character.AsFault(err).Code != "PROVIDER_INVALID_RESPONSE" {
			t.Fatal(err)
		}
	})
}
