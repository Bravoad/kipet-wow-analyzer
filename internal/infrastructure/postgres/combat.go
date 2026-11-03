package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"kipet-wow-analyzer/internal/application"
	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/combat"
)

func (s *Store) CheckCombatMode(ctx context.Context, mode string) error {
	if mode == "disabled" {
		return nil
	}
	if mode != "fixture" && mode != "warcraftlogs" {
		return fmt.Errorf("invalid combat provider mode")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(748215930008)`); err != nil {
		return err
	}
	var existing string
	err = tx.QueryRow(ctx, `SELECT value FROM app_settings WHERE key='combat_provider_mode' FOR UPDATE`).Scan(&existing)
	if err == nil {
		if existing != mode {
			return ErrProviderModeMismatch
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var populated bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM combat_reports)`).Scan(&populated); err != nil {
		return err
	}
	if populated {
		return ErrProviderModeUnmarked
	}
	if _, err = tx.Exec(ctx, `INSERT INTO app_settings(key,value) VALUES('combat_provider_mode',$1)`, mode); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) combatHash(code string) []byte {
	mac := hmac.New(sha256.New, s.options.HMACSecret)
	_, _ = mac.Write([]byte("combat\x00" + code))
	return mac.Sum(nil)
}

func (s *Store) LookupCombat(ctx context.Context, code string) (*application.CombatReport, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	var missing bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM combat_negative_lookups WHERE key_hash=$1 AND expires_at>clock_timestamp())`, s.combatHash(code)).Scan(&missing); err != nil {
		return nil, err
	}
	if missing {
		return nil, character.NewFault("REPORT_UNAVAILABLE", false)
	}
	r := &application.CombatReport{}
	var version *string
	var fetched, fresh, valid *time.Time
	err = tx.QueryRow(ctx, `SELECT version::text,payload,fetched_at,fresh_until,valid_until,blocked_at IS NOT NULL FROM combat_reports WHERE code=$1`, code).Scan(&version, &r.Payload, &fetched, &fresh, &valid, &r.Blocked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, character.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if version != nil {
		r.ID = *version
	}
	if fetched != nil {
		r.FetchedAt = *fetched
	}
	if fresh != nil {
		r.FreshUntil = *fresh
	}
	if valid != nil {
		r.ValidUntil = *valid
	}
	return r, tx.Commit(ctx)
}
func (s *Store) LoadCombatAnalysis(ctx context.Context, code, version string, fightID, actorID int) (*application.CombatResource, error) {
	r := &application.CombatResource{}
	err := s.pool.QueryRow(ctx, `SELECT a.id::text,a.payload,a.fetched_at,LEAST(a.fresh_until,r.fresh_until),LEAST(a.valid_until,r.valid_until)
	 FROM combat_analyses a JOIN combat_reports r ON r.code=a.report_code AND r.version=a.report_version
	 WHERE a.report_code=$1 AND a.report_version=$2 AND a.fight_id=$3 AND a.actor_id=$4
	 AND r.blocked_at IS NULL AND r.payload->>'visibility'='public'
	 AND r.fresh_until>clock_timestamp() AND r.valid_until>clock_timestamp()
	 AND a.fresh_until>clock_timestamp() AND a.valid_until>clock_timestamp()`, code, version, fightID, actorID).Scan(&r.ID, &r.Payload, &r.FetchedAt, &r.FreshUntil, &r.ValidUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, character.ErrNotFound
	}
	return r, err
}

const combatJobSelect = `SELECT id::text,report_code,report_version::text,fight_id,actor_id,status,attempts,max_attempts,run_after,created_at,updated_at,finished_at,last_error_code FROM combat_jobs`

func scanCombatJob(row scanner) (application.CombatLease, error) {
	var l application.CombatLease
	err := row.Scan(&l.Job.ID, &l.Code, &l.ReportVersion, &l.FightID, &l.ActorID, &l.Job.Status, &l.Job.Attempts, &l.Job.MaxAttempts, &l.Job.RunAfter, &l.Job.CreatedAt, &l.Job.UpdatedAt, &l.Job.FinishedAt, &l.Job.ErrorCode)
	l.Job.Kind = "refresh_report"
	if l.FightID > 0 {
		l.Job.Kind = "analyze_fight"
	}
	return l, err
}
func (s *Store) GetCombatJob(ctx context.Context, id string) (character.Job, error) {
	l, err := scanCombatJob(s.pool.QueryRow(ctx, combatJobSelect+` WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return character.Job{}, character.ErrNotFound
	}
	return l.Job, err
}
func (s *Store) AdmitCombat(ctx context.Context, code string, fightID, actorID int) (character.Job, error) {
	if !application.ValidReportCode(code) || !(fightID == 0 && actorID == 0 || fightID > 0 && actorID > 0) {
		return character.Job{}, character.NewFault("INVALID_ARGUMENT", false)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return character.Job{}, err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(748215930007)`); err != nil {
		return character.Job{}, err
	}
	var missing bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM combat_negative_lookups WHERE key_hash=$1 AND expires_at>clock_timestamp())`, s.combatHash(code)).Scan(&missing); err != nil {
		return character.Job{}, err
	}
	if missing {
		return character.Job{}, character.NewFault("REPORT_UNAVAILABLE", false)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO combat_reports(code) VALUES($1) ON CONFLICT DO NOTHING`, code); err != nil {
		return character.Job{}, err
	}
	var version *string
	var blocked bool
	var fresh *time.Time
	if err = tx.QueryRow(ctx, `SELECT version::text,blocked_at IS NOT NULL,fresh_until FROM combat_reports WHERE code=$1 FOR UPDATE`, code).Scan(&version, &blocked, &fresh); err != nil {
		return character.Job{}, err
	}
	if fightID > 0 && (version == nil || blocked || fresh == nil || !time.Now().Before(*fresh)) {
		return character.Job{}, character.NewFault("PROVIDER_ACCESS_DENIED", true)
	}
	last, e := scanCombatJob(tx.QueryRow(ctx, combatJobSelect+` WHERE report_code=$1 AND fight_id=$2 AND actor_id=$3 ORDER BY created_at DESC,id DESC LIMIT 1`, code, fightID, actorID))
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return character.Job{}, e
	}
	if e == nil {
		if last.Job.Active() {
			return last.Job, tx.Commit(ctx)
		}
		until := last.Job.CreatedAt.Add(s.options.RefreshCooldown)
		if last.Job.Status == "failed" && last.Job.FinishedAt != nil {
			until = last.Job.FinishedAt.Add(s.options.FailureCooldown)
			if last.Job.RunAfter.After(until) {
				until = last.Job.RunAfter
			}
		}
		if time.Now().Before(until) {
			code := "REFRESH_COOLDOWN"
			if last.Job.Status == "failed" && last.Job.ErrorCode != nil {
				code = *last.Job.ErrorCode
			}
			return character.Job{}, &character.Fault{Code: code, Retryable: true, RetryAt: until, JobID: &last.Job.ID}
		}
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM combat_jobs WHERE status IN ('queued','running','retry_wait')`).Scan(&count); err != nil {
		return character.Job{}, err
	}
	if count >= s.options.QueueCapacity {
		return character.Job{}, character.NewFault("QUEUE_FULL", true)
	}
	id := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO combat_jobs(id,report_code,report_version,fight_id,actor_id) VALUES($1,$2,$3,$4,$5)`, id, code, version, fightID, actorID); err != nil {
		return character.Job{}, err
	}
	l, err := scanCombatJob(tx.QueryRow(ctx, combatJobSelect+` WHERE id=$1`, id))
	if err != nil {
		return character.Job{}, err
	}
	return l.Job, tx.Commit(ctx)
}

func lockCombat(ctx context.Context, tx pgx.Tx, l application.CombatLease) (application.CombatLease, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(748215930007)`); err != nil {
		return l, err
	}
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT status='running' AND lease_token=$2 AND lease_until>clock_timestamp() FROM combat_jobs WHERE id=$1 FOR UPDATE`, l.Job.ID, l.Token).Scan(&valid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return l, character.ErrLeaseLost
		}
		return l, err
	}
	if !valid {
		return l, character.ErrLeaseLost
	}
	actual, err := scanCombatJob(tx.QueryRow(ctx, combatJobSelect+` WHERE id=$1`, l.Job.ID))
	if err == nil && (actual.Code != l.Code || actual.FightID != l.FightID || actual.ActorID != l.ActorID) {
		return l, character.ErrLeaseLost
	}
	return actual, err
}
func finishCombat(ctx context.Context, tx pgx.Tx, l application.CombatLease) error {
	tag, err := tx.Exec(ctx, `UPDATE combat_jobs SET status='succeeded',finished_at=clock_timestamp(),updated_at=clock_timestamp(),last_error_code=NULL,lease_token=NULL,lease_owner=NULL,lease_until=NULL WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_until>clock_timestamp()`, l.Job.ID, l.Token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return character.ErrLeaseLost
	}
	return nil
}
func (s *Store) SaveCombatReport(ctx context.Context, l application.CombatLease, report combat.Report, finish bool) (*application.CombatResource, error) {
	if report.Code != l.Code || report.Visibility != "public" {
		return nil, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	payload, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	if _, err = lockCombat(ctx, tx, l); err != nil {
		return nil, err
	}
	var version *string
	var same bool
	if err = tx.QueryRow(ctx, `SELECT version::text,COALESCE(payload=$2::jsonb,false) FROM combat_reports WHERE code=$1 FOR UPDATE`, l.Code, payload).Scan(&version, &same); err != nil {
		return nil, err
	}
	if !same || version == nil {
		v := uuid.NewString()
		version = &v
		if _, err = tx.Exec(ctx, `DELETE FROM combat_analyses WHERE report_code=$1`, l.Code); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `UPDATE combat_jobs SET status='cancelled',finished_at=clock_timestamp(),updated_at=clock_timestamp(),last_error_code='CANCELLED',lease_token=NULL,lease_owner=NULL,lease_until=NULL WHERE report_code=$1 AND id<>$2 AND fight_id>0 AND status IN ('queued','running','retry_wait')`, l.Code, l.Job.ID); err != nil {
			return nil, err
		}
	}
	r := &application.CombatResource{ID: *version, Payload: payload}
	err = tx.QueryRow(ctx, `UPDATE combat_reports SET version=$2,payload=$3,fetched_at=statement_timestamp(),fresh_until=statement_timestamp()+interval '15 minutes',valid_until=statement_timestamp()+interval '24 hours',blocked_at=NULL WHERE code=$1 RETURNING fetched_at,fresh_until,valid_until`, l.Code, *version, payload).Scan(&r.FetchedAt, &r.FreshUntil, &r.ValidUntil)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM combat_negative_lookups WHERE key_hash=$1`, s.combatHash(l.Code)); err != nil {
		return nil, err
	}
	if finish {
		err = finishCombat(ctx, tx, l)
	} else {
		tag, updateErr := tx.Exec(ctx, `UPDATE combat_jobs SET report_version=$2 WHERE id=$1 AND status='running' AND lease_token=$3 AND lease_until>clock_timestamp()`, l.Job.ID, *version, l.Token)
		err = updateErr
		if err == nil && tag.RowsAffected() != 1 {
			err = character.ErrLeaseLost
		}
	}
	if err != nil {
		return nil, err
	}
	return r, tx.Commit(ctx)
}
func (s *Store) SaveCombatAnalysis(ctx context.Context, l application.CombatLease, version string, analysis combat.Analysis) error {
	if analysis.ReportCode != l.Code || analysis.FightID != l.FightID || analysis.ActorID != l.ActorID {
		return character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	payload, err := json.Marshal(analysis)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = lockCombat(ctx, tx, l); err != nil {
		return err
	}
	var fresh, valid time.Time
	err = tx.QueryRow(ctx, `SELECT fresh_until,valid_until FROM combat_reports WHERE code=$1 AND version=$2 AND blocked_at IS NULL AND payload->>'visibility'='public' AND fresh_until>clock_timestamp() AND valid_until>clock_timestamp() FOR UPDATE`, l.Code, version).Scan(&fresh, &valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return character.NewFault("PROVIDER_UNAVAILABLE", true)
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO combat_analyses(id,report_code,report_version,fight_id,actor_id,payload,fetched_at,fresh_until,valid_until) VALUES($1,$2,$3,$4,$5,$6,clock_timestamp(),LEAST($7,clock_timestamp()+interval '15 minutes'),LEAST($8,clock_timestamp()+interval '24 hours')) ON CONFLICT(report_code,report_version,fight_id,actor_id) DO UPDATE SET id=excluded.id,payload=excluded.payload,fetched_at=excluded.fetched_at,fresh_until=excluded.fresh_until,valid_until=excluded.valid_until`, uuid.NewString(), l.Code, version, l.FightID, l.ActorID, payload, fresh, valid)
	if err != nil {
		return err
	}
	if err = finishCombat(ctx, tx, l); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) QuarantineCombat(ctx context.Context, l application.CombatLease) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = lockCombat(ctx, tx, l); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE combat_reports SET blocked_at=clock_timestamp() WHERE code=$1`, l.Code); err != nil {
		return err
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT status='running' AND lease_token=$2 AND lease_until>clock_timestamp() FROM combat_jobs WHERE id=$1`, l.Job.ID, l.Token).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return character.ErrLeaseLost
	}
	return tx.Commit(ctx)
}
func (s *Store) PurgeCombat(ctx context.Context, l application.CombatLease) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = lockCombat(ctx, tx, l); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO combat_negative_lookups(key_hash,expires_at) VALUES($1,clock_timestamp()+interval '5 minutes') ON CONFLICT(key_hash) DO UPDATE SET expires_at=excluded.expires_at`, s.combatHash(l.Code)); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM combat_reports WHERE code=$1 AND EXISTS(SELECT 1 FROM combat_jobs WHERE id=$2 AND lease_token=$3 AND status='running' AND lease_until>clock_timestamp())`, l.Code, l.Job.ID, l.Token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return character.ErrLeaseLost
	}
	return tx.Commit(ctx)
}
func (s *Store) MaintainCombat(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(748215930007)`); err != nil {
		return err
	}
	for _, query := range []string{
		`DELETE FROM combat_reports WHERE valid_until<=clock_timestamp() OR payload IS NULL AND created_at<=clock_timestamp()-interval '24 hours'`,
		`DELETE FROM combat_analyses WHERE valid_until<=clock_timestamp() OR NOT EXISTS(SELECT 1 FROM combat_reports r WHERE r.code=report_code AND r.version=report_version)`,
		`DELETE FROM combat_jobs WHERE status IN ('succeeded','failed','cancelled') AND finished_at<=clock_timestamp()-interval '24 hours'`,
		`DELETE FROM combat_negative_lookups WHERE expires_at<=clock_timestamp()`,
	} {
		if _, err = tx.Exec(ctx, query); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
