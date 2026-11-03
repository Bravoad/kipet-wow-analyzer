package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"kipet-wow-analyzer/internal/domain/character"
)

type Options struct {
	HMACSecret      []byte
	LeaseDuration   time.Duration
	RefreshCooldown time.Duration
	FailureCooldown time.Duration
	QueueCapacity   int
}

type Store struct {
	pool    *pgxpool.Pool
	options Options
}

func New(pool *pgxpool.Pool, options Options) *Store {
	if options.LeaseDuration <= 0 {
		options.LeaseDuration = 60 * time.Second
	}
	if options.RefreshCooldown <= 0 {
		options.RefreshCooldown = 60 * time.Second
	}
	if options.FailureCooldown <= 0 {
		options.FailureCooldown = 15 * time.Minute
	}
	if options.QueueCapacity <= 0 {
		options.QueueCapacity = 10000
	}
	options.HMACSecret = append([]byte(nil), options.HMACSecret...)
	return &Store{pool: pool, options: options}
}

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) UpsertRealms(ctx context.Context, realms []character.Realm) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	for _, r := range realms {
		id := r.ID
		if id == "" {
			id = uuid.NewString()
		}
		fetched := r.FetchedAt
		if fetched.IsZero() {
			fetched = time.Now().UTC()
		}
		names, err := json.Marshal(r.Names)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO realms(id,region,provider_realm_id,slug,names,connected_realm_id,fetched_at)
		 VALUES($1,$2,$3::bigint,$4,$5::jsonb,$6::bigint,$7)
		 ON CONFLICT(region,provider_realm_id) DO UPDATE SET slug=excluded.slug,
		 names=excluded.names,connected_realm_id=excluded.connected_realm_id,
		 fetched_at=excluded.fetched_at`, id, r.Region, r.ProviderID, r.Slug, names, r.ConnectedID, fetched)
		if err != nil {
			return fmt.Errorf("upsert realm: %w", err)
		}
	}
	return tx.Commit(ctx)
}

type scanner interface{ Scan(...any) error }

func scanRealm(row scanner) (character.Realm, error) {
	var r character.Realm
	var names []byte
	err := row.Scan(&r.ID, &r.ProviderID, &r.Region, &r.Slug, &names, &r.ConnectedID, &r.FetchedAt)
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(names, &r.Names)
	return r, err
}

const realmColumns = `id::text,provider_realm_id::text,region,slug,names,connected_realm_id::text,fetched_at`

func (s *Store) ResolveRealm(ctx context.Context, region, slug string) (character.Realm, error) {
	r, err := scanRealm(s.pool.QueryRow(ctx, `SELECT `+realmColumns+` FROM realms WHERE region=$1 AND slug=$2 AND fetched_at>clock_timestamp()-interval '720 hours'`, region, slug))
	if errors.Is(err, pgx.ErrNoRows) {
		var available bool
		if e := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM realms WHERE region=$1 AND fetched_at>clock_timestamp()-interval '720 hours')`, region).Scan(&available); e != nil {
			return r, e
		}
		if !available {
			return r, character.NewFault("INFRASTRUCTURE_UNAVAILABLE", true)
		}
		return r, character.NewFault("REALM_NOT_FOUND", false)
	}
	return r, err
}

type realmCursor struct {
	Region  string
	Locale  string
	Query   string
	Slug    string
	ID      string
	Version string
}

func fold(s string) string { return norm.NFC.String(cases.Fold().String(norm.NFC.String(s))) }

func (s *Store) ListRealms(ctx context.Context, region, locale, query, after string, limit int) ([]character.Realm, string, error) {
	if limit < 1 || limit > 100 {
		return nil, "", character.NewFault("INVALID_ARGUMENT", false)
	}
	query = fold(query)
	var cursor realmCursor
	if after != "" {
		data, e := base64.RawURLEncoding.DecodeString(after)
		if e != nil || json.Unmarshal(data, &cursor) != nil || cursor.Region != region || cursor.Locale != locale || cursor.Query != query || cursor.Slug == "" || cursor.ID == "" {
			return nil, "", character.NewFault("INVALID_ARGUMENT", false)
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT `+realmColumns+` FROM realms WHERE region=$1 AND fetched_at>clock_timestamp()-interval '720 hours' ORDER BY slug,provider_realm_id`, region)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	all := make([]character.Realm, 0)
	for rows.Next() {
		r, e := scanRealm(rows)
		if e != nil {
			return nil, "", e
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(all) == 0 {
		return nil, "", character.NewFault("INFRASTRUCTURE_UNAVAILABLE", true)
	}
	versionHash := sha256.New()
	for _, r := range all {
		_, _ = fmt.Fprintf(versionHash, "%s\x00%s\x00%s\x00", r.ProviderID, r.Slug, r.FetchedAt.UTC().Format(time.RFC3339Nano))
	}
	version := base64.RawURLEncoding.EncodeToString(versionHash.Sum(nil))
	if after != "" && cursor.Version != version {
		return nil, "", character.NewFault("INVALID_ARGUMENT", false)
	}
	result := make([]character.Realm, 0, limit+1)
	seenCursor := after == ""
	for _, r := range all {
		if !seenCursor {
			if r.Slug == cursor.Slug && r.ProviderID == cursor.ID {
				seenCursor = true
			}
			continue
		}
		matches := strings.HasPrefix(fold(r.Slug), query)
		if !matches {
			for _, name := range r.Names {
				if strings.HasPrefix(fold(name), query) {
					matches = true
					break
				}
			}
		}
		if matches {
			result = append(result, r)
			if len(result) > limit {
				break
			}
		}
	}
	if !seenCursor {
		return nil, "", character.NewFault("INVALID_ARGUMENT", false)
	}
	next := ""
	if len(result) > limit {
		last := result[limit-1]
		raw, _ := json.Marshal(realmCursor{Region: region, Locale: locale, Query: query, Slug: last.Slug, ID: last.ProviderID, Version: version})
		next = base64.RawURLEncoding.EncodeToString(raw)
		result = result[:limit]
	}
	return result, next, nil
}

func (s *Store) Find(ctx context.Context, key character.Key) (*character.Record, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	var rec character.Record
	var names []byte
	var snapshotID *string
	err = tx.QueryRow(ctx, `SELECT c.id::text,c.name,c.provider_character_id::text,c.current_snapshot_id,
	 c.publication_blocked_at IS NOT NULL,r.id::text,r.provider_realm_id::text,r.region,r.slug,r.names,
	 r.connected_realm_id::text,r.fetched_at FROM characters c JOIN realms r ON r.id=c.realm_id
	 WHERE c.region=$1 AND r.slug=$2 AND c.name_normalized=$3`, key.Region, key.Realm, key.Name).
		Scan(&rec.ID, &rec.Name, &rec.ProviderID, &snapshotID, &rec.Blocked, &rec.Realm.ID, &rec.Realm.ProviderID, &rec.Realm.Region, &rec.Realm.Slug, &names, &rec.Realm.ConnectedID, &rec.Realm.FetchedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, character.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rec.Key = key
	rec.Key.LookupName = rec.Name
	if err := json.Unmarshal(names, &rec.Realm.Names); err != nil {
		return nil, err
	}
	if snapshotID != nil {
		snap := &character.Snapshot{ID: *snapshotID, CharacterID: rec.ID, DisplayName: rec.Name, Sections: map[string]character.Section{}}
		if rec.ProviderID != nil {
			snap.ProviderID = *rec.ProviderID
		}
		err = tx.QueryRow(ctx, `SELECT COALESCE(refresh_job_id::text,''),fetched_at,profile_verified_at,valid_until,patch,season_id::text FROM character_snapshots WHERE id=$1 AND character_id=$2`, *snapshotID, rec.ID).
			Scan(&snap.JobID, &snap.FetchedAt, &snap.ProfileVerifiedAt, &snap.ValidUntil, &snap.Patch, &snap.Season)
		if err != nil {
			return nil, err
		}
		rows, e := tx.Query(ctx, `SELECT section,state,error_code,fetched_at,source_updated_at,checked_at,fresh_until,valid_until,payload FROM snapshot_sections WHERE snapshot_id=$1`, *snapshotID)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var name string
			var sec character.Section
			var payload []byte
			if e = rows.Scan(&name, &sec.State, &sec.Reason, &sec.FetchedAt, &sec.SourceUpdatedAt, &sec.CheckedAt, &sec.FreshUntil, &sec.ValidUntil, &payload); e != nil {
				rows.Close()
				return nil, e
			}
			sec.Payload = json.RawMessage(payload)
			snap.Sections[name] = sec
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		rec.Snapshot = snap
	}
	job, e := queryJob(tx.QueryRow(ctx, jobSelect+` WHERE j.character_id=$1 ORDER BY j.created_at DESC,j.id DESC LIMIT 1`, rec.ID))
	if e == nil {
		rec.LastJob = &job
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *Store) missingHash(key character.Key) []byte {
	mac := hmac.New(sha256.New, s.options.HMACSecret)
	_, _ = mac.Write([]byte(key.Region + "\x00" + key.Realm + "\x00" + key.Name))
	return mac.Sum(nil)
}
func (s *Store) IsMissing(ctx context.Context, key character.Key) (bool, error) {
	var found bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM negative_lookups WHERE key_hash=$1 AND expires_at>clock_timestamp())`, s.missingHash(key)).Scan(&found)
	return found, err
}

const jobSelect = `SELECT j.id::text,j.character_id::text,c.region,r.slug,c.name_normalized,c.name,j.status,j.reason,j.attempts,j.max_attempts,j.run_after,j.created_at,j.updated_at,j.finished_at,j.last_error_code FROM refresh_jobs j JOIN characters c ON c.id=j.character_id JOIN realms r ON r.id=c.realm_id`

func queryJob(row scanner) (character.Job, error) {
	var j character.Job
	err := row.Scan(&j.ID, &j.CharacterID, &j.Key.Region, &j.Key.Realm, &j.Key.Name, &j.Key.LookupName, &j.Status, &j.Reason, &j.Attempts, &j.MaxAttempts, &j.RunAfter, &j.CreatedAt, &j.UpdatedAt, &j.FinishedAt, &j.ErrorCode)
	return j, err
}
func (s *Store) GetJob(ctx context.Context, id string) (character.Job, error) {
	j, e := queryJob(s.pool.QueryRow(ctx, jobSelect+` WHERE j.id=$1`, id))
	if errors.Is(e, pgx.ErrNoRows) {
		e = character.ErrNotFound
	}
	return j, e
}

// Enqueue serializes only short admission transactions. The advisory lock makes
// the queue capacity a global bound, and the partial index is the durable dedup.
func (s *Store) Enqueue(ctx context.Context, key character.Key, reason string, manual bool) (character.EnqueueResult, error) {
	var result character.EnqueueResult
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(748215930002)`); err != nil {
		return result, err
	}
	var missing bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM negative_lookups WHERE key_hash=$1 AND expires_at>clock_timestamp())`, s.missingHash(key)).Scan(&missing); err != nil {
		return result, err
	}
	if missing {
		return result, character.NewFault("CHARACTER_UNAVAILABLE", false)
	}
	var realmID string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM realms WHERE region=$1 AND slug=$2 AND fetched_at>clock_timestamp()-interval '720 hours'`, key.Region, key.Realm).Scan(&realmID); errors.Is(err, pgx.ErrNoRows) {
		return result, character.NewFault("REALM_NOT_FOUND", false)
	} else if err != nil {
		return result, err
	}
	charID := uuid.NewString()
	lookupName := key.LookupName
	if lookupName == "" {
		lookupName = key.Name
	}
	if _, err = tx.Exec(ctx, `INSERT INTO characters(id,region,realm_id,name,name_normalized) VALUES($1,$2,$3,$4,$5) ON CONFLICT(region,realm_id,name_normalized) DO NOTHING`, charID, key.Region, realmID, lookupName, key.Name); err != nil {
		return result, err
	}
	if err = tx.QueryRow(ctx, `SELECT id::text FROM characters WHERE region=$1 AND realm_id=$2 AND name_normalized=$3 FOR UPDATE`, key.Region, realmID, key.Name).Scan(&charID); err != nil {
		return result, err
	}
	job, e := queryJob(tx.QueryRow(ctx, jobSelect+` WHERE j.character_id=$1 ORDER BY j.created_at DESC,j.id DESC LIMIT 1`, charID))
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return result, e
	}
	if e == nil {
		if job.Active() {
			result.Job = job
			return result, tx.Commit(ctx)
		}
		now := time.Now().UTC()
		until := job.CreatedAt.Add(s.options.RefreshCooldown)
		if job.Status == "failed" && job.FinishedAt != nil {
			until = job.FinishedAt.Add(s.options.FailureCooldown)
			if job.RunAfter.After(until) {
				until = job.RunAfter
			}
		}
		if now.Before(until) {
			code := "REFRESH_COOLDOWN"
			if job.Status == "failed" && job.ErrorCode != nil {
				code = *job.ErrorCode
			}
			return result, &character.Fault{Code: code, Retryable: true, RetryAt: until, JobID: &job.ID}
		}
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM refresh_jobs WHERE status IN ('queued','running','retry_wait')`).Scan(&count); err != nil {
		return result, err
	}
	if count >= s.options.QueueCapacity {
		return result, character.NewFault("QUEUE_FULL", true)
	}
	id := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO refresh_jobs(id,character_id,reason) VALUES($1,$2,$3)`, id, charID, reason); err != nil {
		return result, err
	}
	job, err = queryJob(tx.QueryRow(ctx, jobSelect+` WHERE j.id=$1`, id))
	if err != nil {
		return result, err
	}
	result = character.EnqueueResult{Job: job, Created: true}
	return result, tx.Commit(ctx)
}

func sortedSections() []string {
	out := append([]string(nil), character.AllSections...)
	sort.Strings(out)
	return out
}
