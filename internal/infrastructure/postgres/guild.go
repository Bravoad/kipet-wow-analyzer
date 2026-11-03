package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"kipet-wow-analyzer/internal/application"
	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/guild"
)

const guildLock int64 = 748215930009

func (s *Store) guildHash(key guild.Key) []byte {
	mac := hmac.New(sha256.New, s.options.HMACSecret)
	_, _ = mac.Write([]byte("guild\x00" + key.Region + "\x00" + key.Realm + "\x00" + key.Slug))
	return mac.Sum(nil)
}
func (s *Store) LookupGuild(ctx context.Context, key guild.Key) (*application.GuildRecord, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	var missing bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM guild_negative_lookups WHERE key_hash=$1 AND expires_at>clock_timestamp())`, s.guildHash(key)).Scan(&missing); err != nil {
		return nil, err
	}
	if missing {
		return nil, character.NewFault("GUILD_UNAVAILABLE", false)
	}
	r := &application.GuildRecord{Key: key}
	var version *string
	var fetched, fresh, valid *time.Time
	var names []byte
	err = tx.QueryRow(ctx, `SELECT g.id::text,g.current_snapshot_id::text,g.blocked_at IS NOT NULL,s.payload,s.fetched_at,s.fresh_until,s.valid_until,r.id::text,r.provider_realm_id::text,r.region,r.slug,r.names,r.connected_realm_id::text,r.fetched_at
	FROM guilds g JOIN realms r ON r.id=g.realm_id LEFT JOIN guild_snapshots s ON s.id=g.current_snapshot_id AND s.guild_id=g.id WHERE g.region=$1 AND r.slug=$2 AND g.slug=$3`, key.Region, key.Realm, key.Slug).Scan(&r.GuildID, &version, &r.Blocked, &r.Payload, &fetched, &fresh, &valid, &r.Realm.ID, &r.Realm.ProviderID, &r.Realm.Region, &r.Realm.Slug, &names, &r.Realm.ConnectedID, &r.Realm.FetchedAt)
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
	if json.Unmarshal(names, &r.Realm.Names) != nil {
		return nil, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	l, e := scanGuildJob(tx.QueryRow(ctx, guildJobSelect+` WHERE j.guild_id=$1 ORDER BY j.created_at DESC,j.id DESC LIMIT 1`, r.GuildID))
	if e == nil {
		r.LastJob = &l.Job
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	return r, tx.Commit(ctx)
}

const guildJobSelect = `SELECT j.id::text,j.guild_id::text,g.region,r.slug,g.slug,j.snapshot_id::text,j.schedule_cursor,j.status,j.attempts,j.max_attempts,j.run_after,j.created_at,j.updated_at,j.finished_at,j.last_error_code FROM guild_jobs j JOIN guilds g ON g.id=j.guild_id JOIN realms r ON r.id=g.realm_id`

func scanGuildJob(row scanner) (application.GuildLease, error) {
	var l application.GuildLease
	err := row.Scan(&l.Job.ID, &l.GuildID, &l.Key.Region, &l.Key.Realm, &l.Key.Slug, &l.SnapshotID, &l.Cursor, &l.Job.Status, &l.Job.Attempts, &l.Job.MaxAttempts, &l.Job.RunAfter, &l.Job.CreatedAt, &l.Job.UpdatedAt, &l.Job.FinishedAt, &l.Job.ErrorCode)
	l.Job.Kind = "refresh_guild"
	return l, err
}
func (s *Store) GetGuildJob(ctx context.Context, id string) (character.Job, error) {
	l, e := scanGuildJob(s.pool.QueryRow(ctx, guildJobSelect+` WHERE j.id=$1`, id))
	if errors.Is(e, pgx.ErrNoRows) {
		return character.Job{}, character.ErrNotFound
	}
	return l.Job, e
}
func (s *Store) AdmitGuild(ctx context.Context, key guild.Key, manual bool) (character.Job, error) {
	canonical, e := guild.NormalizeKey(key.Region, key.Realm, key.Slug)
	if e != nil || canonical != key {
		return character.Job{}, character.NewFault("INVALID_ARGUMENT", false)
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return character.Job{}, e
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, guildLock); e != nil {
		return character.Job{}, e
	}
	var missing bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM guild_negative_lookups WHERE key_hash=$1 AND expires_at>clock_timestamp())`, s.guildHash(key)).Scan(&missing); e != nil {
		return character.Job{}, e
	}
	if missing {
		return character.Job{}, character.NewFault("GUILD_UNAVAILABLE", false)
	}
	var realmID string
	if e = tx.QueryRow(ctx, `SELECT id::text FROM realms WHERE region=$1 AND slug=$2 AND fetched_at>clock_timestamp()-interval '720 hours'`, key.Region, key.Realm).Scan(&realmID); errors.Is(e, pgx.ErrNoRows) {
		return character.Job{}, character.NewFault("REALM_NOT_FOUND", false)
	} else if e != nil {
		return character.Job{}, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO guilds(id,region,realm_id,slug) VALUES($1,$2,$3,$4) ON CONFLICT(region,realm_id,slug) DO NOTHING`, uuid.NewString(), key.Region, realmID, key.Slug); e != nil {
		return character.Job{}, e
	}
	var guildID string
	var reusableSnapshot *string
	if e = tx.QueryRow(ctx, `SELECT g.id::text,CASE WHEN NOT $4 AND g.blocked_at IS NULL AND s.fresh_until>clock_timestamp() AND s.valid_until>clock_timestamp() THEN s.id::text END
	 FROM guilds g LEFT JOIN guild_snapshots s ON s.id=g.current_snapshot_id AND s.guild_id=g.id
	 WHERE g.region=$1 AND g.realm_id=$2 AND g.slug=$3 FOR UPDATE OF g`, key.Region, realmID, key.Slug, manual).Scan(&guildID, &reusableSnapshot); e != nil {
		return character.Job{}, e
	}
	last, e := scanGuildJob(tx.QueryRow(ctx, guildJobSelect+` WHERE j.guild_id=$1 ORDER BY j.created_at DESC,j.id DESC LIMIT 1`, guildID))
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
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM guild_jobs WHERE status IN ('queued','running','retry_wait')`).Scan(&count); e != nil {
		return character.Job{}, e
	}
	if count >= s.options.QueueCapacity {
		return character.Job{}, character.NewFault("QUEUE_FULL", true)
	}
	id := uuid.NewString()
	if _, e = tx.Exec(ctx, `INSERT INTO guild_jobs(id,guild_id,snapshot_id) VALUES($1,$2,$3)`, id, guildID, reusableSnapshot); e != nil {
		return character.Job{}, e
	}
	l, e := scanGuildJob(tx.QueryRow(ctx, guildJobSelect+` WHERE j.id=$1`, id))
	if e != nil {
		return character.Job{}, e
	}
	return l.Job, tx.Commit(ctx)
}
func lockGuild(ctx context.Context, tx pgx.Tx, l application.GuildLease) (application.GuildLease, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, guildLock); err != nil {
		return l, err
	}
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT status='running' AND lease_token=$2 AND lease_until>clock_timestamp() FROM guild_jobs WHERE id=$1 FOR UPDATE`, l.Job.ID, l.Token).Scan(&valid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return l, character.ErrLeaseLost
		}
		return l, err
	}
	if !valid {
		return l, character.ErrLeaseLost
	}
	actual, err := scanGuildJob(tx.QueryRow(ctx, guildJobSelect+` WHERE j.id=$1`, l.Job.ID))
	if err == nil && (actual.Key != l.Key || actual.GuildID != l.GuildID) {
		return l, character.ErrLeaseLost
	}
	return actual, err
}
func guildFence(ctx context.Context, tx pgx.Tx, l application.GuildLease) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT status='running' AND lease_token=$2 AND lease_until>clock_timestamp() FROM guild_jobs WHERE id=$1`, l.Job.ID, l.Token).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !valid {
		return character.ErrLeaseLost
	}
	return err
}
func (s *Store) SaveGuild(ctx context.Context, l application.GuildLease, b guild.Bundle) (*application.GuildRecord, error) {
	providerID, err := strconv.ParseInt(b.Profile.ProviderID, 10, 64)
	if err != nil || providerID <= 0 || strconv.FormatInt(providerID, 10) != b.Profile.ProviderID || b.Profile.Region != l.Key.Region || b.Profile.Slug != l.Key.Slug || b.Profile.Name == "" || len(b.Roster.Members) > 1000 {
		return nil, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	if _, err = lockGuild(ctx, tx, l); err != nil {
		return nil, err
	}
	var previous *string
	var realmID string
	if err = tx.QueryRow(ctx, `SELECT provider_id,realm_id::text FROM guilds WHERE id=$1 FOR UPDATE`, l.GuildID).Scan(&previous, &realmID); err != nil {
		return nil, err
	}
	if previous != nil && *previous != b.Profile.ProviderID {
		// A different source identity under the same lookup key starts afresh.
		if _, err = tx.Exec(ctx, `DELETE FROM guilds WHERE id=$1`, l.GuildID); err != nil {
			return nil, err
		}
		newID := uuid.NewString()
		if _, err = tx.Exec(ctx, `INSERT INTO guilds(id,region,realm_id,slug) VALUES($1,$2,$3,$4)`, newID, l.Key.Region, realmID, l.Key.Slug); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO guild_jobs(id,guild_id) VALUES($1,$2)`, uuid.NewString(), newID); err != nil {
			return nil, err
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, character.ErrLeaseLost
	}
	b.Profile.ID = l.GuildID
	b.Roster.GuildID = l.GuildID
	payload, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	r := &application.GuildRecord{ID: uuid.NewString(), GuildID: l.GuildID, Key: l.Key, Payload: payload}
	err = tx.QueryRow(ctx, `INSERT INTO guild_snapshots(id,guild_id,payload,fetched_at,fresh_until,valid_until) VALUES($1,$2,$3,statement_timestamp(),statement_timestamp()+interval '60 minutes',statement_timestamp()+interval '720 hours') RETURNING fetched_at,fresh_until,valid_until`, r.ID, l.GuildID, payload).Scan(&r.FetchedAt, &r.FreshUntil, &r.ValidUntil)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE guilds SET provider_id=$2,current_snapshot_id=$3,blocked_at=NULL WHERE id=$1`, l.GuildID, b.Profile.ProviderID, r.ID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE guild_jobs SET snapshot_id=$2,schedule_cursor=0 WHERE id=$1`, l.Job.ID, r.ID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM guild_negative_lookups WHERE key_hash=$1`, s.guildHash(l.Key)); err != nil {
		return nil, err
	}
	if err = guildFence(ctx, tx, l); err != nil {
		return nil, err
	}
	return r, tx.Commit(ctx)
}
func (s *Store) QuarantineGuild(ctx context.Context, l application.GuildLease) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if _, e = lockGuild(ctx, tx, l); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE guilds SET blocked_at=clock_timestamp() WHERE id=$1`, l.GuildID); e != nil {
		return e
	}
	if e = guildFence(ctx, tx, l); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) PurgeGuild(ctx context.Context, l application.GuildLease) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if _, e = lockGuild(ctx, tx, l); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO guild_negative_lookups(key_hash,expires_at) VALUES($1,clock_timestamp()+interval '5 minutes') ON CONFLICT(key_hash) DO UPDATE SET expires_at=excluded.expires_at`, s.guildHash(l.Key)); e != nil {
		return e
	}
	tag, e := tx.Exec(ctx, `DELETE FROM guilds WHERE id=$1 AND EXISTS(SELECT 1 FROM guild_jobs WHERE id=$2 AND status='running' AND lease_token=$3 AND lease_until>clock_timestamp())`, l.GuildID, l.Job.ID, l.Token)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return character.ErrLeaseLost
	}
	return tx.Commit(ctx)
}
func (s *Store) MaintainGuild(ctx context.Context) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, guildLock); e != nil {
		return e
	}
	for _, q := range []string{
		`DELETE FROM guilds WHERE current_snapshot_id IS NULL AND created_at<=clock_timestamp()-interval '720 hours' OR current_snapshot_id IN (SELECT id FROM guild_snapshots WHERE valid_until<=clock_timestamp())`,
		`DELETE FROM guild_snapshots WHERE valid_until<=clock_timestamp()`,
		`DELETE FROM guild_jobs WHERE status IN ('succeeded','failed','cancelled') AND finished_at<=clock_timestamp()-interval '24 hours'`,
		`DELETE FROM guild_negative_lookups WHERE expires_at<=clock_timestamp()`,
	} {
		if _, e = tx.Exec(ctx, q); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
