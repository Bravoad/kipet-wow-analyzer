package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"kipet-wow-analyzer/internal/application"
	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/guild"
)

func (s *Store) ClaimGuild(ctx context.Context, worker string) (*application.GuildLease, error) {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, guildLock); e != nil {
		return nil, e
	}
	_, e = tx.Exec(ctx, `WITH expired AS(SELECT id FROM guild_jobs WHERE status='running' AND lease_until<=clock_timestamp() FOR UPDATE SKIP LOCKED LIMIT 100)
	UPDATE guild_jobs j SET status=CASE WHEN attempts>=max_attempts AND snapshot_id IS NULL THEN 'failed' ELSE 'retry_wait' END,
	run_after=clock_timestamp(),finished_at=CASE WHEN attempts>=max_attempts AND snapshot_id IS NULL THEN clock_timestamp() ELSE NULL END,
	last_error_code=CASE WHEN attempts>=max_attempts AND snapshot_id IS NULL THEN 'PROVIDER_UNAVAILABLE' ELSE last_error_code END,
	lease_token=NULL,lease_owner=NULL,lease_until=NULL,updated_at=clock_timestamp() FROM expired e WHERE j.id=e.id`)
	if e != nil {
		return nil, e
	}
	var id string
	e = tx.QueryRow(ctx, `SELECT id::text FROM guild_jobs WHERE status IN ('queued','retry_wait') AND run_after<=clock_timestamp() AND (attempts<max_attempts OR snapshot_id IS NOT NULL) ORDER BY run_after,created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, tx.Commit(ctx)
	}
	if e != nil {
		return nil, e
	}
	l, e := scanGuildJob(tx.QueryRow(ctx, guildJobSelect+` WHERE j.id=$1`, id))
	if e != nil {
		return nil, e
	}
	l.Token = uuid.NewString()
	e = tx.QueryRow(ctx, `UPDATE guild_jobs SET status='running',lease_token=$2,lease_owner=$3,lease_until=clock_timestamp()+($4*interval '1 millisecond'),updated_at=clock_timestamp() WHERE id=$1 RETURNING lease_until`, id, l.Token, worker, s.options.LeaseDuration.Milliseconds()).Scan(&l.ExpiresAt)
	if e != nil {
		return nil, e
	}
	l.Job.Status = "running"
	return &l, tx.Commit(ctx)
}
func (s *Store) RenewGuild(ctx context.Context, l application.GuildLease) error {
	tag, e := s.pool.Exec(ctx, `UPDATE guild_jobs SET lease_until=clock_timestamp()+($3*interval '1 millisecond'),updated_at=clock_timestamp() WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_until>clock_timestamp()`, l.Job.ID, l.Token, s.options.LeaseDuration.Milliseconds())
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return character.ErrLeaseLost
	}
	return nil
}
func (s *Store) StartGuildAttempt(ctx context.Context, l application.GuildLease) error {
	tag, e := s.pool.Exec(ctx, `UPDATE guild_jobs SET attempts=attempts+1,snapshot_id=NULL,schedule_cursor=0,updated_at=clock_timestamp() WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_until>clock_timestamp() AND attempts<max_attempts`, l.Job.ID, l.Token)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return character.ErrLeaseLost
	}
	return nil
}
func (s *Store) FailGuild(ctx context.Context, l application.GuildLease, fault *character.Fault, retryAt time.Time) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	actual, e := lockGuild(ctx, tx, l)
	if e != nil {
		return e
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
	if !fault.Retryable || actual.Job.Attempts >= actual.Job.MaxAttempts && actual.SnapshotID == nil {
		status = "failed"
		now := time.Now().UTC()
		finished = &now
	}
	tag, e := tx.Exec(ctx, `UPDATE guild_jobs SET status=$3,last_error_code=$4,run_after=$5,finished_at=$6,lease_token=NULL,lease_owner=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_until>clock_timestamp()`, l.Job.ID, l.Token, status, fault.Code, retryAt, finished)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return character.ErrLeaseLost
	}
	return tx.Commit(ctx)
}

// ScheduleGuildChunk serializes the roster fence with character admission.
// At most fifty identities and the cursor are committed together. Retrying a
// chunk can neither bypass character cooldowns/capacity nor duplicate work.
func (s *Store) ScheduleGuildChunk(ctx context.Context, l application.GuildLease) (int, bool, error) {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return 0, false, e
	}
	defer tx.Rollback(context.Background())
	actual, e := lockGuild(ctx, tx, l)
	if e != nil {
		return 0, false, e
	}
	if actual.SnapshotID == nil {
		return 0, false, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	var payload []byte
	e = tx.QueryRow(ctx, `SELECT s.payload FROM guilds g JOIN guild_snapshots s ON s.id=g.current_snapshot_id AND s.guild_id=g.id WHERE g.id=$1 AND s.id=$2 AND g.blocked_at IS NULL AND s.fresh_until>clock_timestamp() AND s.valid_until>clock_timestamp() FOR UPDATE OF g`, l.GuildID, *actual.SnapshotID).Scan(&payload)
	if errors.Is(e, pgx.ErrNoRows) {
		return actual.Cursor, false, character.NewFault("PROVIDER_UNAVAILABLE", true)
	}
	if e != nil {
		return 0, false, e
	}
	var bundle guild.Bundle
	if json.Unmarshal(payload, &bundle) != nil || len(bundle.Roster.Members) > 1000 || actual.Cursor > len(bundle.Roster.Members) {
		return 0, false, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(748215930002)`); e != nil {
		return 0, false, e
	}
	var active int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM refresh_jobs WHERE status IN ('queued','running','retry_wait')`).Scan(&active); e != nil {
		return 0, false, e
	}
	next := actual.Cursor
	end := min(len(bundle.Roster.Members), next+50)
	for next < end {
		member := bundle.Roster.Members[next]
		key, err := character.NormalizeKey(member.Key.Region, member.Key.Realm, member.Key.Name)
		if err != nil || key.Region != actual.Key.Region {
			return next, false, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
		}
		admitted, blocked, err := s.admitGuildCharacter(ctx, tx, key, member.ProviderID, active)
		if err != nil {
			return next, false, err
		}
		if blocked {
			break
		}
		if admitted {
			active++
		}
		next++
	}
	done := next == len(bundle.Roster.Members)
	if done {
		tag, err := tx.Exec(ctx, `UPDATE guild_jobs SET schedule_cursor=$3,status='succeeded',finished_at=clock_timestamp(),updated_at=clock_timestamp(),last_error_code=NULL,lease_token=NULL,lease_owner=NULL,lease_until=NULL WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_until>clock_timestamp()`, l.Job.ID, l.Token, next)
		if err != nil {
			return next, false, err
		}
		if tag.RowsAffected() != 1 {
			return next, false, character.ErrLeaseLost
		}
	} else {
		tag, err := tx.Exec(ctx, `UPDATE guild_jobs SET schedule_cursor=$3,updated_at=clock_timestamp() WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_until>clock_timestamp()`, l.Job.ID, l.Token, next)
		if err != nil {
			return next, false, err
		}
		if tag.RowsAffected() != 1 {
			return next, false, character.ErrLeaseLost
		}
	}
	return next, done, tx.Commit(ctx)
}

func (s *Store) admitGuildCharacter(ctx context.Context, tx pgx.Tx, key character.Key, providerID string, active int) (bool, bool, error) {
	var missing bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM negative_lookups WHERE key_hash=$1 AND expires_at>clock_timestamp())`, s.missingHash(key)).Scan(&missing); err != nil {
		return false, false, err
	}
	if missing {
		return false, false, nil
	}
	var realmID string
	err := tx.QueryRow(ctx, `SELECT id::text FROM realms WHERE region=$1 AND slug=$2 AND fetched_at>clock_timestamp()-interval '720 hours'`, key.Region, key.Realm).Scan(&realmID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	var charID string
	var fresh bool
	err = tx.QueryRow(ctx, `SELECT c.id::text,c.publication_blocked_at IS NULL AND c.provider_character_id::text=$4 AND c.data_valid_until>clock_timestamp()
	 AND EXISTS(SELECT 1 FROM character_snapshots s WHERE s.id=c.current_snapshot_id AND s.character_id=c.id AND s.valid_until>clock_timestamp())
	 AND (SELECT count(*) FROM snapshot_sections sec WHERE sec.snapshot_id=c.current_snapshot_id AND sec.state<>'unavailable' AND sec.fresh_until>clock_timestamp() AND sec.valid_until>clock_timestamp())=6
	 FROM characters c WHERE c.region=$1 AND c.realm_id=$2 AND c.name_normalized=$3 FOR UPDATE`, key.Region, realmID, key.Name, providerID).Scan(&charID, &fresh)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, false, err
	}
	exists := err == nil
	if fresh {
		return false, false, nil
	}
	if exists {
		last, e := queryJob(tx.QueryRow(ctx, jobSelect+` WHERE j.character_id=$1 ORDER BY j.created_at DESC,j.id DESC LIMIT 1`, charID))
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return false, false, e
		}
		if e == nil {
			if last.Active() {
				return false, false, nil
			}
			until := last.CreatedAt.Add(s.options.RefreshCooldown)
			if last.Status == "failed" && last.FinishedAt != nil {
				until = last.FinishedAt.Add(s.options.FailureCooldown)
				if last.RunAfter.After(until) {
					until = last.RunAfter
				}
			}
			if time.Now().Before(until) {
				return false, false, nil
			}
		}
	}
	if active >= s.options.QueueCapacity {
		return false, true, nil
	}
	if !exists {
		charID = uuid.NewString()
		if _, err = tx.Exec(ctx, `INSERT INTO characters(id,region,realm_id,name,name_normalized) VALUES($1,$2,$3,$4,$5)`, charID, key.Region, realmID, key.LookupName, key.Name); err != nil {
			return false, false, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO refresh_jobs(id,character_id,reason) VALUES($1,$2,'revalidation')`, uuid.NewString(), charID)
	return err == nil, false, err
}
