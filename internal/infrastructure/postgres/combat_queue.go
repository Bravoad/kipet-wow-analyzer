package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"kipet-wow-analyzer/internal/application"
	"kipet-wow-analyzer/internal/domain/character"
)

func (s *Store) ClaimCombat(ctx context.Context, worker string) (*application.CombatLease, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(748215930007)`); err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `WITH expired AS (SELECT id FROM combat_jobs WHERE status='running' AND lease_until<=clock_timestamp() FOR UPDATE SKIP LOCKED LIMIT 100)
	 UPDATE combat_jobs j SET status=CASE WHEN attempts>=max_attempts THEN 'failed' ELSE 'retry_wait' END,run_after=clock_timestamp(),last_error_code=CASE WHEN attempts>=max_attempts THEN 'PROVIDER_UNAVAILABLE' ELSE last_error_code END,finished_at=CASE WHEN attempts>=max_attempts THEN clock_timestamp() ELSE NULL END,lease_token=NULL,lease_owner=NULL,lease_until=NULL,updated_at=clock_timestamp() FROM expired e WHERE j.id=e.id`)
	if err != nil {
		return nil, err
	}
	var id string
	err = tx.QueryRow(ctx, `SELECT id::text FROM combat_jobs WHERE status IN ('queued','retry_wait') AND run_after<=clock_timestamp() AND attempts<max_attempts ORDER BY run_after,created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tx.Commit(ctx)
	}
	if err != nil {
		return nil, err
	}
	l, err := scanCombatJob(tx.QueryRow(ctx, combatJobSelect+` WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	l.Token = uuid.NewString()
	err = tx.QueryRow(ctx, `UPDATE combat_jobs SET status='running',lease_token=$2,lease_owner=$3,lease_until=clock_timestamp()+($4*interval '1 millisecond'),updated_at=clock_timestamp() WHERE id=$1 RETURNING lease_until`, id, l.Token, worker, s.options.LeaseDuration.Milliseconds()).Scan(&l.ExpiresAt)
	if err != nil {
		return nil, err
	}
	l.Job.Status = "running"
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &l, nil
}
func (s *Store) RenewCombat(ctx context.Context, l application.CombatLease) error {
	tag, err := s.pool.Exec(ctx, `UPDATE combat_jobs SET lease_until=clock_timestamp()+($3*interval '1 millisecond'),updated_at=clock_timestamp() WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_until>clock_timestamp()`, l.Job.ID, l.Token, s.options.LeaseDuration.Milliseconds())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return character.ErrLeaseLost
	}
	return nil
}
func (s *Store) StartCombatAttempt(ctx context.Context, l application.CombatLease) error {
	tag, err := s.pool.Exec(ctx, `UPDATE combat_jobs SET attempts=attempts+1,updated_at=clock_timestamp() WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_until>clock_timestamp() AND attempts<max_attempts`, l.Job.ID, l.Token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return character.ErrLeaseLost
	}
	return nil
}
func (s *Store) FailCombat(ctx context.Context, l application.CombatLease, fault *character.Fault, retryAt time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	actual, err := lockCombat(ctx, tx, l)
	if err != nil {
		return err
	}
	if fault == nil {
		fault = character.NewFault("PROVIDER_UNAVAILABLE", true)
	}
	if fault.RetryAt.After(retryAt) {
		retryAt = fault.RetryAt
	}
	if retryAt.IsZero() {
		retryAt = time.Now().UTC()
	}
	status := "retry_wait"
	var finished *time.Time
	if !fault.Retryable || actual.Job.Attempts >= actual.Job.MaxAttempts {
		status = "failed"
		now := time.Now().UTC()
		finished = &now
	}
	tag, err := tx.Exec(ctx, `UPDATE combat_jobs SET status=$3,last_error_code=$4,run_after=$5,finished_at=$6,lease_token=NULL,lease_owner=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_until>clock_timestamp()`, l.Job.ID, l.Token, status, fault.Code, retryAt, finished)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return character.ErrLeaseLost
	}
	return tx.Commit(ctx)
}
