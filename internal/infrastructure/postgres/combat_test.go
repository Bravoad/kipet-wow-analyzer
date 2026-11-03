package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"kipet-wow-analyzer/internal/application"
	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/combat"
	"kipet-wow-analyzer/migrations"
)

const combatCode = "DEMOCombat000001"

func observedReport() combat.Report {
	title := "Synthetic public report"
	return combat.Report{Code: combatCode, Title: &title, Visibility: "public", Fights: []combat.Fight{{ID: 1, StartMS: 0, EndMS: 10000}}, Actors: []combat.Actor{{ID: 2, Type: "player"}, {ID: 3, Type: "pet"}}}
}
func claimCombat(t *testing.T, ctx context.Context, s *Store) *application.CombatLease {
	t.Helper()
	l, err := s.ClaimCombat(ctx, "synthetic-combat-worker")
	if err != nil || l == nil {
		t.Fatalf("claim=%+v %v", l, err)
	}
	if err = s.StartCombatAttempt(ctx, *l); err != nil {
		t.Fatal(err)
	}
	return l
}
func publishCombat(t *testing.T, ctx context.Context, s *Store) *application.CombatResource {
	t.Helper()
	if _, err := s.AdmitCombat(ctx, combatCode, 0, 0); err != nil {
		t.Fatal(err)
	}
	l := claimCombat(t, ctx, s)
	r, err := s.SaveCombatReport(ctx, *l, observedReport(), true)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCombatDurableAdmissionAnalysisAndAccessFreshness(t *testing.T) {
	ctx, s, pool := testStore(t)
	s.options.RefreshCooldown = time.Nanosecond
	const concurrency = 20
	ids := make(chan string, concurrency)
	errs := make(chan error, concurrency)
	var group sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			j, err := s.AdmitCombat(ctx, combatCode, 0, 0)
			if err != nil {
				errs <- err
			} else {
				ids <- j.ID
			}
		}()
	}
	group.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	id := ""
	for got := range ids {
		if id != "" && got != id {
			t.Fatal("dedup admitted distinct report jobs")
		}
		id = got
	}
	l := claimCombat(t, ctx, s)
	if _, err := s.ClaimCombat(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	r, err := s.SaveCombatReport(ctx, *l, observedReport(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = uuid.Parse(r.ID); err != nil {
		t.Fatal(err)
	}
	job, err := s.GetCombatJob(ctx, id)
	if err != nil || job.Status != "succeeded" || job.Kind != "refresh_report" {
		t.Fatalf("%+v %v", job, err)
	}
	svc := application.CombatService{Repository: s, Mode: "fixture"}
	result, err := svc.ReadAnalysis(ctx, combatCode, 1, 2, "ru-RU")
	if err != nil || !result.Pending {
		t.Fatalf("%+v %v", result, err)
	}
	l = claimCombat(t, ctx, s)
	again, err := s.SaveCombatReport(ctx, *l, observedReport(), false)
	if err != nil || again.ID != r.ID {
		t.Fatalf("stable report changed version %+v %v", again, err)
	}
	analysis := combat.Analysis{ReportCode: combatCode, FightID: 1, ActorID: 2, Source: "fixture", EventsComplete: true}
	if err = s.SaveCombatAnalysis(ctx, *l, r.ID, analysis); err != nil {
		t.Fatal(err)
	}
	result, err = svc.ReadAnalysis(ctx, combatCode, 1, 2, "en-US")
	if err != nil || result.Data == nil || result.Pending || *result.Meta.SnapshotID != r.ID {
		t.Fatalf("%+v %v", result, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE combat_reports SET fetched_at=clock_timestamp()-interval '20 minutes',fresh_until=clock_timestamp()-interval '1 minute',valid_until=clock_timestamp()+interval '1 hour' WHERE code=$1`, combatCode); err != nil {
		t.Fatal(err)
	}
	if _, err = s.LoadCombatAnalysis(ctx, combatCode, r.ID, 1, 2); !errors.Is(err, character.ErrNotFound) {
		t.Fatal("expired report check did not close cached analysis", err)
	}
	result, err = svc.ReadAnalysis(ctx, combatCode, 1, 2, "ru-RU")
	if err != nil || !result.Pending || result.Data != nil {
		t.Fatalf("expired public proof leaked data %+v %v", result, err)
	}
}

func TestCombatQuarantinePurgeAndNegativeLookup(t *testing.T) {
	ctx, s, pool := testStore(t)
	s.options.RefreshCooldown = time.Nanosecond
	r := publishCombat(t, ctx, s)
	if _, err := s.AdmitCombat(ctx, combatCode, 1, 2); err != nil {
		t.Fatal(err)
	}
	l := claimCombat(t, ctx, s)
	if _, err := s.SaveCombatReport(ctx, *l, observedReport(), false); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCombatAnalysis(ctx, *l, r.ID, combat.Analysis{ReportCode: combatCode, FightID: 1, ActorID: 2, EventsComplete: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadCombatAnalysis(ctx, combatCode, r.ID, 1, 2); err != nil {
		t.Fatal("analysis was not present before quarantine", err)
	}
	if _, err := s.AdmitCombat(ctx, combatCode, 0, 0); err != nil {
		t.Fatal(err)
	}
	l = claimCombat(t, ctx, s)
	if err := s.QuarantineCombat(ctx, *l); err != nil {
		t.Fatal(err)
	}
	svc := application.CombatService{Repository: s, Mode: "fixture"}
	result, err := svc.ReadReport(ctx, combatCode, "ru-RU")
	if result.Data != nil || character.AsFault(err).Code != "PROVIDER_ACCESS_DENIED" {
		t.Fatalf("%+v %v", result, err)
	}
	if _, err = s.LoadCombatAnalysis(ctx, combatCode, r.ID, 1, 2); !errors.Is(err, character.ErrNotFound) {
		t.Fatal(err)
	}
	if err = s.PurgeCombat(ctx, *l); err != nil {
		t.Fatal(err)
	}
	if _, err = s.LookupCombat(ctx, combatCode); character.AsFault(err).Code != "REPORT_UNAVAILABLE" {
		t.Fatal(err)
	}
	if _, err = s.GetCombatJob(ctx, l.Job.ID); !errors.Is(err, character.ErrNotFound) {
		t.Fatal("purged job survived", err)
	}
	if _, err = s.SaveCombatReport(ctx, *l, observedReport(), true); !errors.Is(err, character.ErrLeaseLost) {
		t.Fatal("purged worker resurrected report", err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM combat_reports`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if _, err = s.AdmitCombat(ctx, combatCode, 0, 0); character.AsFault(err).Code != "REPORT_UNAVAILABLE" {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE combat_negative_lookups SET expires_at=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdmitCombat(ctx, combatCode, 0, 0); err != nil {
		t.Fatal(err)
	}
	raw, err := s.LookupCombat(ctx, combatCode)
	if err != nil || len(raw.Payload) != 0 || raw.ID != "" {
		t.Fatal("new placeholder inherited deleted payload", err)
	}
}

func TestCombatLeaseFencingAndProviderRetryFloor(t *testing.T) {
	ctx, s, pool := testStore(t)
	s.options.RefreshCooldown = time.Nanosecond
	if _, err := s.AdmitCombat(ctx, combatCode, 0, 0); err != nil {
		t.Fatal(err)
	}
	old := claimCombat(t, ctx, s)
	if _, err := pool.Exec(ctx, `UPDATE combat_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, old.Job.ID); err != nil {
		t.Fatal(err)
	}
	current := claimCombat(t, ctx, s)
	if current.Job.ID != old.Job.ID || current.Token == old.Token {
		t.Fatal("expired lease was not fenced")
	}
	if _, err := s.SaveCombatReport(ctx, *old, observedReport(), true); !errors.Is(err, character.ErrLeaseLost) {
		t.Fatal(err)
	}
	floor := time.Now().Add(time.Hour)
	fault := &character.Fault{Code: "PROVIDER_UNAVAILABLE", Retryable: true, RetryAt: floor}
	if err := s.FailCombat(ctx, *current, fault, time.Now()); err != nil {
		t.Fatal(err)
	}
	job, err := s.GetCombatJob(ctx, current.Job.ID)
	if err != nil || job.Status != "retry_wait" || job.RunAfter.Before(floor.Add(-time.Microsecond)) {
		t.Fatalf("%+v %v", job, err)
	}
	if next, err := s.ClaimCombat(ctx, "too-early"); err != nil || next != nil {
		t.Fatalf("retry before provider floor %+v %v", next, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE combat_jobs SET run_after=clock_timestamp()-interval '1 second',attempts=4 WHERE id=$1`, current.Job.ID); err != nil {
		t.Fatal(err)
	}
	current = claimCombat(t, ctx, s)
	if err = s.FailCombat(ctx, *current, fault, time.Now()); err != nil {
		t.Fatal(err)
	}
	job, err = s.GetCombatJob(ctx, current.Job.ID)
	if err != nil || job.Status != "failed" {
		t.Fatalf("%+v %v", job, err)
	}
	_, err = s.AdmitCombat(ctx, combatCode, 0, 0)
	got := character.AsFault(err)
	if got.RetryAt.Before(floor.Add(-time.Microsecond)) || got.JobID == nil {
		t.Fatal("terminal failure lost retry floor", err)
	}
}

func TestCombatReportRevisionFencesDerivedWork(t *testing.T) {
	ctx, s, _ := testStore(t)
	s.options.RefreshCooldown = time.Nanosecond
	version := publishCombat(t, ctx, s)
	if _, err := s.AdmitCombat(ctx, combatCode, 1, 2); err != nil {
		t.Fatal(err)
	}
	old := claimCombat(t, ctx, s)
	if _, err := s.AdmitCombat(ctx, combatCode, 0, 0); err != nil {
		t.Fatal(err)
	}
	refresh := claimCombat(t, ctx, s)
	report := observedReport()
	title := "Changed public report"
	report.Title = &title
	next, err := s.SaveCombatReport(ctx, *refresh, report, true)
	if err != nil || next.ID == version.ID {
		t.Fatalf("revision missing %+v %v", next, err)
	}
	if err = s.SaveCombatAnalysis(ctx, *old, version.ID, combat.Analysis{ReportCode: combatCode, FightID: 1, ActorID: 2}); !errors.Is(err, character.ErrLeaseLost) {
		t.Fatal("old derived worker published", err)
	}
	job, err := s.GetCombatJob(ctx, old.Job.ID)
	if err != nil || job.Status != "cancelled" {
		t.Fatalf("%+v %v", job, err)
	}
}

func TestCombatRetentionModeAndMigrationRollback(t *testing.T) {
	ctx, s, pool := testStore(t)
	if err := s.CheckCombatMode(ctx, "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckCombatMode(ctx, "warcraftlogs"); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckCombatMode(ctx, "fixture"); !errors.Is(err, ErrProviderModeMismatch) {
		t.Fatal("mixed provenance accepted", err)
	}
	publishCombat(t, ctx, s)
	if _, err := pool.Exec(ctx, `UPDATE combat_reports SET fetched_at=statement_timestamp()-interval '25 hours',fresh_until=statement_timestamp()-interval '24 hours',valid_until=statement_timestamp()-interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	if err := s.MaintainCombat(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LookupCombat(ctx, combatCode); !errors.Is(err, character.ErrNotFound) {
		t.Fatal("retained expired report", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO provider_state(provider,scope) VALUES('warcraftlogs','global')`); err != nil {
		t.Fatal(err)
	}
	rollbackThrough(t, ctx, pool, "0007_combat_reports.up.sql")
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('combat_reports') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatal("combat rollback failed", err)
	}
	if err := migrations.Up(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckCombatMode(ctx, "fixture"); err != nil {
		t.Fatal("rolled back marker survived", err)
	}
}

func TestCombatCapacityAndLostLeaseNeverPublish(t *testing.T) {
	ctx, s, pool := testStore(t)
	s.options.QueueCapacity = 1
	if _, err := s.AdmitCombat(ctx, combatCode, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdmitCombat(ctx, "DEMOCombat000002", 0, 0); character.AsFault(err).Code != "QUEUE_FULL" {
		t.Fatal("capacity bypassed", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM combat_reports`).Scan(&count); err != nil || count != 1 {
		t.Fatal("rejected work left an identifying placeholder", count, err)
	}
	l := claimCombat(t, ctx, s)
	// A database delay after the initial fence must not allow a transaction to
	// publish once its lease expires before the final state transition.
	if _, err := pool.Exec(ctx, `CREATE FUNCTION slow_combat_report_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(0.08); RETURN NEW; END $$;
	 CREATE TRIGGER combat_report_delay BEFORE UPDATE ON combat_reports FOR EACH ROW EXECUTE FUNCTION slow_combat_report_update()`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE combat_jobs SET lease_until=clock_timestamp()+interval '40 milliseconds' WHERE id=$1`, l.Job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveCombatReport(ctx, *l, observedReport(), false); !errors.Is(err, character.ErrLeaseLost) {
		t.Fatal("expired publication transaction committed", err)
	}
	report, err := s.LookupCombat(ctx, combatCode)
	if err != nil || report.Payload != nil {
		t.Fatal("lost lease left visible metadata", err)
	}
}
