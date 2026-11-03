package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"kipet-wow-analyzer/internal/application"
	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/raiderio"
)

const raiderAdmissionLock int64 = 748215930010

func raiderKind(key raiderio.Key) string {
	if key.Kind == "" {
		return "character"
	}
	return key.Kind
}

func (s *Store) raiderHash(key raiderio.Key) []byte {
	mac := hmac.New(sha256.New, s.options.HMACSecret)
	namespace := "raiderio\x00"
	if key.Kind == "guild" {
		namespace += "guild\x00"
	}
	_, _ = mac.Write([]byte(namespace + key.Region + "\x00" + key.Realm + "\x00" + key.Name))
	return mac.Sum(nil)
}

const raiderJobColumns = `j.id::text,j.status,j.attempts,j.run_after,j.created_at,j.updated_at,j.finished_at,j.last_error_code`
const raiderJobSelect = `SELECT ` + raiderJobColumns + ` FROM raiderio_jobs j`

func scanRaiderJob(row scanner) (character.Job, error) {
	job := character.Job{Kind: application.RaiderJobKind, MaxAttempts: 5}
	err := row.Scan(&job.ID, &job.Status, &job.Attempts, &job.RunAfter, &job.CreatedAt, &job.UpdatedAt, &job.FinishedAt, &job.ErrorCode)
	return job, err
}

func (s *Store) LookupRaider(ctx context.Context, key raiderio.Key) (*application.RaiderRecord, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	if err := s.checkRaiderNegative(ctx, tx, key); err != nil {
		return nil, err
	}
	var id string
	var payload []byte
	var fetched, fresh, valid *time.Time
	record := &application.RaiderRecord{}
	err = tx.QueryRow(ctx, `SELECT id::text,COALESCE(version::text,''),payload,fetched_at,fresh_until,valid_until,blocked_at IS NOT NULL
		FROM raiderio_profiles WHERE kind=$1 AND region=$2 AND realm=$3 AND name=$4`, raiderKind(key), key.Region, key.Realm, key.Name).
		Scan(&id, &record.Version, &payload, &fetched, &fresh, &valid, &record.Blocked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, character.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !record.Blocked && valid != nil && time.Now().Before(*valid) {
		var target any = &record.Snapshot.Profile
		if key.Kind == "guild" {
			target = &record.Snapshot.Guild
		}
		if err := json.Unmarshal(payload, target); err != nil {
			return nil, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
		}
		record.Snapshot.FetchedAt, record.Snapshot.FreshUntil, record.Snapshot.ValidUntil = *fetched, *fresh, *valid
	}
	job, err := scanRaiderJob(tx.QueryRow(ctx, raiderJobSelect+` WHERE profile_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, id))
	if err == nil {
		record.Job = &job
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return record, tx.Commit(ctx)
}

func (s *Store) checkRaiderNegative(ctx context.Context, tx pgx.Tx, key raiderio.Key) error {
	var missing bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM raiderio_negative_lookups
		WHERE key_hash=$1 AND kind=$2 AND expires_at>clock_timestamp())`, s.raiderHash(key), raiderKind(key)).Scan(&missing)
	if err == nil && missing {
		if key.Kind == "guild" {
			return character.NewFault("GUILD_UNAVAILABLE", false)
		}
		return character.NewFault("CHARACTER_UNAVAILABLE", false)
	}
	return err
}

func (s *Store) AdmitRaider(ctx context.Context, key raiderio.Key) (character.Job, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return character.Job{}, err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, raiderAdmissionLock); err != nil {
		return character.Job{}, err
	}
	if err = s.checkRaiderNegative(ctx, tx, key); err != nil {
		return character.Job{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO raiderio_profiles(id,kind,region,realm,name) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(kind,region,realm,name) DO NOTHING`, uuid.NewString(), raiderKind(key), key.Region, key.Realm, key.Name)
	if err != nil {
		return character.Job{}, err
	}
	var id string
	err = tx.QueryRow(ctx, `SELECT id::text FROM raiderio_profiles WHERE kind=$1 AND region=$2 AND realm=$3 AND name=$4`,
		raiderKind(key), key.Region, key.Realm, key.Name).Scan(&id)
	if err != nil {
		return character.Job{}, err
	}
	last, err := scanRaiderJob(tx.QueryRow(ctx, raiderJobSelect+` WHERE profile_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, id))
	if err == nil {
		if last.Active() {
			return last, tx.Commit(ctx)
		}
		until, code := last.CreatedAt.Add(s.options.RefreshCooldown), "REFRESH_COOLDOWN"
		if last.Status == "failed" && last.FinishedAt != nil {
			until = last.FinishedAt.Add(s.options.FailureCooldown)
			if last.RunAfter.After(until) {
				until = last.RunAfter
			}
			if last.ErrorCode != nil {
				code = *last.ErrorCode
			}
		}
		if time.Now().Before(until) {
			return character.Job{}, &character.Fault{Code: code, Retryable: true, RetryAt: until, JobID: &last.ID}
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return character.Job{}, err
	}
	var active int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM raiderio_jobs WHERE status IN ('queued','running','retry_wait')`).Scan(&active); err != nil {
		return character.Job{}, err
	}
	if active >= min(s.options.QueueCapacity, 1000) {
		return character.Job{}, character.NewFault("QUEUE_FULL", true)
	}
	job, err := scanRaiderJob(tx.QueryRow(ctx, `INSERT INTO raiderio_jobs AS j(id,profile_id) VALUES($1,$2) RETURNING `+raiderJobColumns, uuid.NewString(), id))
	if err != nil {
		return character.Job{}, err
	}
	return job, tx.Commit(ctx)
}

func (s *Store) GetRaiderJob(ctx context.Context, id string) (character.Job, error) {
	job, err := scanRaiderJob(s.pool.QueryRow(ctx, raiderJobSelect+` WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return job, character.ErrNotFound
	}
	return job, err
}

func (s *Store) ClaimRaider(ctx context.Context) (*application.RaiderLease, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(ctx, `WITH expired AS (
		SELECT id FROM raiderio_jobs WHERE status='running' AND lease_until<=clock_timestamp() FOR UPDATE SKIP LOCKED LIMIT 100)
		UPDATE raiderio_jobs j SET status=CASE WHEN attempts>=5 THEN 'failed' ELSE 'retry_wait' END,
		run_after=clock_timestamp(),last_error_code='PROVIDER_UNAVAILABLE',
		finished_at=CASE WHEN attempts>=5 THEN clock_timestamp() ELSE NULL END,
		lease_token=NULL,lease_until=NULL,updated_at=clock_timestamp() FROM expired e WHERE j.id=e.id`)
	if err != nil {
		return nil, err
	}
	lease := &application.RaiderLease{Token: uuid.NewString()}
	lease.Job, err = scanRaiderJob(tx.QueryRow(ctx, `WITH candidate AS (
		SELECT id FROM raiderio_jobs WHERE status IN ('queued','retry_wait') AND run_after<=clock_timestamp() AND attempts<5
		ORDER BY run_after,created_at,id FOR UPDATE SKIP LOCKED LIMIT 1)
		UPDATE raiderio_jobs j SET status='running',lease_token=$1,lease_until=clock_timestamp()+interval '60 seconds',
		updated_at=clock_timestamp() FROM candidate c WHERE j.id=c.id RETURNING `+raiderJobColumns, lease.Token))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tx.Commit(ctx)
	}
	if err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, `SELECT p.id::text,p.region,p.realm,p.name,p.kind
		FROM raiderio_jobs j JOIN raiderio_profiles p ON p.id=j.profile_id WHERE j.id=$1`, lease.Job.ID).
		Scan(&lease.ProfileID, &lease.Key.Region, &lease.Key.Realm, &lease.Key.Name, &lease.Key.Kind)
	if err != nil {
		return nil, err
	}
	if lease.Key.Kind == "character" {
		lease.Key.Kind = ""
	}
	return lease, tx.Commit(ctx)
}

func (s *Store) StartRaiderAttempt(ctx context.Context, lease application.RaiderLease) error {
	tag, err := s.pool.Exec(ctx, `UPDATE raiderio_jobs SET attempts=attempts+1,updated_at=clock_timestamp()
		WHERE id=$1 AND lease_token=$2 AND lease_until>clock_timestamp() AND attempts<5`, lease.Job.ID, lease.Token)
	if err == nil && tag.RowsAffected() != 1 {
		return character.ErrLeaseLost
	}
	return err
}

func (s *Store) SaveRaider(ctx context.Context, lease application.RaiderLease, snapshot raiderio.Snapshot) error {
	if err := raiderio.ValidateSnapshot(lease.Key, snapshot); err != nil {
		return err
	}
	var observed any = snapshot.Profile
	if lease.Key.Kind == "guild" {
		observed = snapshot.Guild
	}
	payload, err := json.Marshal(observed)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = lockRaider(ctx, tx, lease); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE raiderio_profiles SET version=$2,payload=$3,fetched_at=$4,fresh_until=$5,valid_until=$6,blocked_at=NULL WHERE id=$1`,
		lease.ProfileID, uuid.NewString(), payload, snapshot.FetchedAt, snapshot.FreshUntil, snapshot.ValidUntil)
	if err != nil {
		return err
	}
	if _, err = finishRaider(ctx, tx, lease, "succeeded", nil, time.Now()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lockRaider(ctx context.Context, tx pgx.Tx, lease application.RaiderLease) error {
	var id string
	err := tx.QueryRow(ctx, `SELECT j.id::text FROM raiderio_jobs j JOIN raiderio_profiles p ON p.id=j.profile_id
		WHERE j.id=$1 AND j.lease_token=$2 AND j.lease_until>clock_timestamp() AND p.id=$3
		AND p.kind=$4 AND p.region=$5 AND p.realm=$6 AND p.name=$7 FOR UPDATE OF j`,
		lease.Job.ID, lease.Token, lease.ProfileID, raiderKind(lease.Key), lease.Key.Region, lease.Key.Realm, lease.Key.Name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return character.ErrLeaseLost
	}
	return err
}

func finishRaider(ctx context.Context, tx pgx.Tx, lease application.RaiderLease, status string, code *string, retryAt time.Time) (string, error) {
	var result string
	err := tx.QueryRow(ctx, `UPDATE raiderio_jobs SET
		status=CASE WHEN $3='retry_wait' AND attempts>=5 THEN 'failed' ELSE $3 END,
		finished_at=CASE WHEN $3<>'retry_wait' OR attempts>=5 THEN clock_timestamp() ELSE NULL END,
		last_error_code=$4,run_after=$5,updated_at=clock_timestamp(),lease_token=NULL,lease_until=NULL
		WHERE id=$1 AND lease_token=$2 AND lease_until>clock_timestamp() AND profile_id=$6 RETURNING status`,
		lease.Job.ID, lease.Token, status, code, retryAt, lease.ProfileID).Scan(&result)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", character.ErrLeaseLost
	}
	return result, err
}

func (s *Store) FailRaider(ctx context.Context, lease application.RaiderLease, fault *character.Fault, retryAt time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	status := "failed"
	if fault.Retryable {
		status = "retry_wait"
	}
	if fault.RetryAt.After(retryAt) {
		retryAt = fault.RetryAt
	}
	if err = lockRaider(ctx, tx, lease); err != nil {
		return err
	}
	if fault.Code == "PROVIDER_ACCESS_DENIED" {
		if _, err = tx.Exec(ctx, `UPDATE raiderio_profiles SET blocked_at=clock_timestamp() WHERE id=$1`, lease.ProfileID); err != nil {
			return err
		}
	}
	if _, err = finishRaider(ctx, tx, lease, status, &fault.Code, retryAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) PurgeRaider(ctx context.Context, lease application.RaiderLease) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, raiderAdmissionLock); err != nil {
		return err
	}
	if err = lockRaider(ctx, tx, lease); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO raiderio_negative_lookups(key_hash,kind,expires_at) VALUES($1,$2,clock_timestamp()+interval '5 minutes')
		ON CONFLICT(key_hash) DO UPDATE SET expires_at=EXCLUDED.expires_at`, s.raiderHash(lease.Key), raiderKind(lease.Key))
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM raiderio_profiles WHERE id=$1 AND EXISTS(
		SELECT 1 FROM raiderio_jobs WHERE id=$2 AND lease_token=$3 AND lease_until>clock_timestamp() AND profile_id=$1)`, lease.ProfileID, lease.Job.ID, lease.Token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return character.ErrLeaseLost
	}
	return tx.Commit(ctx)
}

func (s *Store) MaintainRaider(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, raiderAdmissionLock); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE raiderio_profiles SET version=NULL,payload=NULL,fetched_at=NULL,fresh_until=NULL,valid_until=NULL
			WHERE valid_until<=clock_timestamp();
		DELETE FROM raiderio_jobs WHERE finished_at<=clock_timestamp()-interval '24 hours';
		DELETE FROM raiderio_profiles p WHERE payload IS NULL AND created_at<=clock_timestamp()-interval '24 hours'
			AND NOT EXISTS(SELECT 1 FROM raiderio_jobs j WHERE j.profile_id=p.id);
		DELETE FROM raiderio_negative_lookups WHERE expires_at<=clock_timestamp()`)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
