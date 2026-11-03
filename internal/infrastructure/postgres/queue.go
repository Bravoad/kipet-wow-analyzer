package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"kipet-wow-analyzer/internal/domain/character"
)

func (s *Store) Claim(ctx context.Context, worker string) (*character.Lease, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	// Expired owners are fenced before a new worker can claim their tasks.
	_, err = tx.Exec(ctx, `WITH expired AS (
	 SELECT id FROM refresh_jobs WHERE status='running' AND lease_until<=clock_timestamp()
	 ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 100)
	 UPDATE refresh_jobs j SET status=CASE WHEN attempts>=max_attempts THEN 'failed' ELSE 'retry_wait' END,
	 run_after=clock_timestamp(),last_error_code=CASE WHEN attempts>=max_attempts THEN 'PROVIDER_UNAVAILABLE' ELSE last_error_code END,
	 finished_at=CASE WHEN attempts>=max_attempts THEN clock_timestamp() ELSE NULL END,
	 lease_token=NULL,lease_owner=NULL,leased_at=NULL,lease_until=NULL,updated_at=clock_timestamp()
	 FROM expired e WHERE j.id=e.id`)
	if err != nil {
		return nil, err
	}
	var id string
	err = tx.QueryRow(ctx, `SELECT id::text FROM refresh_jobs WHERE status IN ('queued','retry_wait') AND run_after<=clock_timestamp() AND attempts<max_attempts ORDER BY priority DESC,run_after,created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tx.Commit(ctx)
	}
	if err != nil {
		return nil, err
	}
	lease := &character.Lease{Token: uuid.NewString()}
	err = tx.QueryRow(ctx, `UPDATE refresh_jobs SET status='running',lease_token=$2,lease_owner=$3,
	 leased_at=clock_timestamp(),lease_until=clock_timestamp()+($4*interval '1 millisecond'),updated_at=clock_timestamp()
	 WHERE id=$1 RETURNING lease_until`, id, lease.Token, worker, s.options.LeaseDuration.Milliseconds()).Scan(&lease.ExpiresAt)
	if err != nil {
		return nil, err
	}
	lease.Job, err = queryJob(tx.QueryRow(ctx, jobSelect+` WHERE j.id=$1`, id))
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return lease, nil
}

func (s *Store) Renew(ctx context.Context, lease character.Lease) (character.Lease, error) {
	err := s.pool.QueryRow(ctx, `UPDATE refresh_jobs SET lease_until=clock_timestamp()+($3*interval '1 millisecond'),updated_at=clock_timestamp()
	 WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_until>clock_timestamp() RETURNING lease_until`, lease.Job.ID, lease.Token, s.options.LeaseDuration.Milliseconds()).Scan(&lease.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return lease, character.ErrLeaseLost
	}
	return lease, err
}

func (s *Store) StartAttempt(ctx context.Context, lease character.Lease) error {
	tag, err := s.pool.Exec(ctx, `UPDATE refresh_jobs SET attempts=attempts+1,updated_at=clock_timestamp()
	 WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_until>clock_timestamp() AND attempts<max_attempts`, lease.Job.ID, lease.Token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return character.ErrLeaseLost
	}
	return nil
}

func lockLease(ctx context.Context, tx pgx.Tx, lease character.Lease) (character.Job, error) {
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT status='running' AND lease_token=$2 AND lease_until>clock_timestamp()
	 FROM refresh_jobs WHERE id=$1 FOR UPDATE`, lease.Job.ID, lease.Token).Scan(&valid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return character.Job{}, character.ErrLeaseLost
		}
		return character.Job{}, err
	}
	if !valid {
		return character.Job{}, character.ErrLeaseLost
	}
	job, err := queryJob(tx.QueryRow(ctx, jobSelect+` WHERE j.id=$1`, lease.Job.ID))
	return job, err
}

func (s *Store) Fail(ctx context.Context, lease character.Lease, fault *character.Fault, retryAt time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	job, err := lockLease(ctx, tx, lease)
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
	terminal := !fault.Retryable || job.Attempts >= job.MaxAttempts
	status := "retry_wait"
	var finished *time.Time
	if terminal {
		status = "failed"
		now := time.Now().UTC()
		finished = &now
	}
	tag, err := tx.Exec(ctx, `UPDATE refresh_jobs SET status=$3,last_error_code=$4,run_after=$5,finished_at=$6,
	 lease_token=NULL,lease_owner=NULL,leased_at=NULL,lease_until=NULL,updated_at=clock_timestamp()
	 WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_until>clock_timestamp()`, job.ID, lease.Token, status, fault.Code, retryAt, finished)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return character.ErrLeaseLost
	}
	return tx.Commit(ctx)
}

func (s *Store) Quarantine(ctx context.Context, lease character.Lease) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	job, err := lockLease(ctx, tx, lease)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE characters SET publication_blocked_at=clock_timestamp(),publication_block_reason='provider_access_denied' WHERE id=$1`, job.CharacterID); err != nil {
		return err
	}
	// Keep the lease for Fail to choose a retry; publication is already denied.
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT lease_until>clock_timestamp() FROM refresh_jobs WHERE id=$1 AND lease_token=$2`, job.ID, lease.Token).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return character.ErrLeaseLost
	}
	return tx.Commit(ctx)
}

func (s *Store) Purge(ctx context.Context, lease character.Lease) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	job, err := lockLease(ctx, tx, lease)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM characters WHERE id=$1 FOR UPDATE`, job.CharacterID); err != nil {
		return err
	}
	// Negative lookup stores only a keyed digest; all identifying rows cascade.
	if _, err = tx.Exec(ctx, `INSERT INTO negative_lookups(key_hash,reason,expires_at) VALUES($1,'CHARACTER_UNAVAILABLE',clock_timestamp()+interval '5 minutes') ON CONFLICT(key_hash) DO UPDATE SET expires_at=excluded.expires_at`, s.missingHash(job.Key)); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM characters c WHERE c.id=$1 AND EXISTS (SELECT 1 FROM refresh_jobs j WHERE j.id=$2 AND j.character_id=c.id AND j.status='running' AND j.lease_token=$3 AND j.lease_until>clock_timestamp())`, job.CharacterID, job.ID, lease.Token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return character.ErrLeaseLost
	}
	return tx.Commit(ctx)
}
