package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/migrations"
)

func testStore(t *testing.T) (context.Context, *Store, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL required for real PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	bootstrap, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := "kipet_store_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = bootstrap.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		bootstrap.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = bootstrap.Exec(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`)
		bootstrap.Close()
	})
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 12
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = migrations.Up(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := New(pool, Options{HMACSecret: []byte("synthetic-test-hmac-key-32-bytes!"), RefreshCooldown: time.Millisecond})
	if err = store.UpsertRealms(ctx, []character.Realm{{ID: uuid.NewString(), ProviderID: "1", Region: "eu", Slug: "synthetic", Names: map[string]string{"ru-RU": "Синтетический сервер", "en-US": "Synthetic Realm"}, FetchedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	return ctx, store, pool
}

func testKey() character.Key {
	return character.Key{Region: "eu", Realm: "synthetic", Name: "синтетик"}
}
func requireLease(t *testing.T, ctx context.Context, s *Store) *character.Lease {
	t.Helper()
	lease, err := s.Claim(ctx, "synthetic-worker")
	if err != nil {
		t.Fatal(err)
	}
	if lease == nil {
		t.Fatal("expected lease")
	}
	if err = s.StartAttempt(ctx, *lease); err != nil {
		t.Fatal(err)
	}
	return lease
}

func syntheticSnapshot(providerID string) character.Snapshot {
	now := time.Now().UTC()
	valid := now.Add(720 * time.Hour)
	fresh := now.Add(15 * time.Minute)
	jsonOf := func(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }
	snap := character.Snapshot{ProviderID: providerID, DisplayName: "Синтетик", FetchedAt: now, ProfileVerifiedAt: now, ValidUntil: valid, Sections: map[string]character.Section{}}
	profile := map[string]any{"id": uuid.NewString(), "provider_id": providerID, "region": "eu", "realm": map[string]any{}, "name": "Синтетик", "name_normalized": "синтетик", "level": 1, "race": map[string]any{"id": "1", "name": nil}, "character_class": map[string]any{"id": "1", "name": nil}, "active_specialization": nil, "faction": "unknown", "average_item_level": nil, "equipped_item_level": nil, "source_last_login_at": nil}
	for _, name := range character.Sections {
		snap.Sections[name] = character.Section{State: "complete", FetchedAt: &now, CheckedAt: now, FreshUntil: &fresh, ValidUntil: &valid, Payload: jsonOf(map[string]any{})}
	}
	sec := snap.Sections["profile"]
	sec.Payload = jsonOf(profile)
	snap.Sections["profile"] = sec
	sec = snap.Sections["equipment"]
	sec.Payload = jsonOf(map[string]any{"character_id": uuid.NewString(), "items": []any{map[string]any{"slot": "head", "item_id": "1", "name": "Синтетический предмет", "item_level": 1, "quality": "common", "inventory_type": nil, "bonus_ids": []any{}, "enchantments": nil, "sockets": []any{}, "set_id": nil}}})
	snap.Sections["equipment"] = sec
	sec = snap.Sections["statistics"]
	sec.Payload = jsonOf(map[string]any{"character_id": uuid.NewString(), "health": nil})
	snap.Sections["statistics"] = sec
	sec = snap.Sections["talents"]
	sec.State = "partial"
	reason := "not_provided"
	sec.Reason = &reason
	sec.Payload = jsonOf(map[string]any{"character_id": uuid.NewString(), "loadouts": []any{map[string]any{"provider_id": nil, "specialization": map[string]any{"id": "1", "name": nil}, "is_active": nil, "export_code": nil, "tree_hash": nil, "nodes": nil}}})
	snap.Sections["talents"] = sec
	return snap
}

func TestConcurrentAdmissionAndClaims(t *testing.T) {
	ctx, s, _ := testStore(t)
	key := testKey()
	const n = 32
	ids := make(chan string, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := s.Enqueue(ctx, key, "cache_miss", false)
			if e != nil {
				errs <- e
				return
			}
			ids <- r.Job.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatalf("duplicate active jobs %s and %s", first, id)
		}
	}
	leases := make(chan *character.Lease, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, e := s.Claim(ctx, "synthetic-worker")
			if e != nil {
				t.Error(e)
			}
			if lease != nil {
				leases <- lease
			}
		}()
	}
	wg.Wait()
	close(leases)
	if len(leases) != 1 {
		t.Fatalf("claimed %d owners, want 1", len(leases))
	}
}

func TestExpiredLeaseCannotPublishOrRenew(t *testing.T) {
	ctx, s, pool := testStore(t)
	if _, err := s.Enqueue(ctx, testKey(), "cache_miss", false); err != nil {
		t.Fatal(err)
	}
	old := requireLease(t, ctx, s)
	if _, err := pool.Exec(ctx, `UPDATE refresh_jobs SET leased_at=clock_timestamp()-interval '2 minutes',lease_until=clock_timestamp()-interval '1 minute' WHERE id=$1`, old.Job.ID); err != nil {
		t.Fatal(err)
	}
	current := requireLease(t, ctx, s)
	if current.Token == old.Token {
		t.Fatal("fencing token reused")
	}
	if err := s.Publish(ctx, *old, syntheticSnapshot("1")); !errors.Is(err, character.ErrLeaseLost) {
		t.Fatalf("late publish: %v", err)
	}
	if _, err := s.Renew(ctx, *old); !errors.Is(err, character.ErrLeaseLost) {
		t.Fatalf("late renewal: %v", err)
	}
	if err := s.Publish(ctx, *current, syntheticSnapshot("1")); err != nil {
		t.Fatal(err)
	}
	job, err := s.GetJob(ctx, current.Job.ID)
	if err != nil || job.Status != "succeeded" {
		t.Fatalf("job=%+v err=%v", job, err)
	}
}

func TestPublishPreservesPartialAndPurgeCascades(t *testing.T) {
	ctx, s, pool := testStore(t)
	key := testKey()
	if _, err := s.Enqueue(ctx, key, "cache_miss", false); err != nil {
		t.Fatal(err)
	}
	lease := requireLease(t, ctx, s)
	snap := syntheticSnapshot("1")
	oldFetched := time.Now().UTC().Add(-2 * time.Hour)
	oldFresh := oldFetched.Add(15 * time.Minute)
	oldValid := oldFetched.Add(720 * time.Hour)
	gear := snap.Sections["equipment"]
	gear.FetchedAt = &oldFetched
	gear.FreshUntil = &oldFresh
	gear.ValidUntil = &oldValid
	snap.Sections["equipment"] = gear
	if err := s.Publish(ctx, *lease, snap); err != nil {
		t.Fatal(err)
	}
	rec, err := s.Find(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Snapshot == nil {
		t.Fatal("no snapshot")
	}
	stored := rec.Snapshot.Sections["equipment"]
	if stored.FetchedAt.Sub(oldFetched) > time.Microsecond || oldFetched.Sub(*stored.FetchedAt) > time.Microsecond {
		t.Fatal("old source timestamp changed")
	}
	var enchants []byte
	var sockets []byte
	if err = pool.QueryRow(ctx, `SELECT enchantments,sockets FROM equipment_items WHERE snapshot_id=$1`, rec.Snapshot.ID).Scan(&enchants, &sockets); err != nil {
		t.Fatal(err)
	}
	if enchants != nil || string(sockets) != "[]" {
		t.Fatalf("null vs empty lost: %s %s", enchants, sockets)
	}
	var active *bool
	var nodes []byte
	if err = pool.QueryRow(ctx, `SELECT is_active,selected_nodes FROM talent_loadouts WHERE snapshot_id=$1`, rec.Snapshot.ID).Scan(&active, &nodes); err != nil {
		t.Fatal(err)
	}
	if active != nil || nodes != nil {
		t.Fatal("unknown talents became false or empty")
	}
	if _, err = pool.Exec(ctx, `UPDATE refresh_jobs SET created_at=clock_timestamp()-interval '2 minutes' WHERE id=$1`, lease.Job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Enqueue(ctx, key, "user_refresh", true); err != nil {
		t.Fatal(err)
	}
	purgeLease := requireLease(t, ctx, s)
	if err = s.Purge(ctx, *purgeLease); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Find(ctx, key); !errors.Is(err, character.ErrNotFound) {
		t.Fatalf("purged record: %v", err)
	}
	missing, err := s.IsMissing(ctx, key)
	if err != nil || !missing {
		t.Fatalf("negative lookup missing=%v err=%v", missing, err)
	}
	for _, table := range []string{"characters", "character_snapshots", "snapshot_sections", "equipment_items", "talent_loadouts", "refresh_jobs"} {
		var count int
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d rows", table, count)
		}
	}
	if err = s.Publish(ctx, *purgeLease, syntheticSnapshot("1")); !errors.Is(err, character.ErrLeaseLost) {
		t.Fatalf("resurrection publish: %v", err)
	}
	if _, err = s.Enqueue(ctx, key, "cache_miss", false); character.AsFault(err).Code != "CHARACTER_UNAVAILABLE" {
		t.Fatalf("negative cache bypass: %v", err)
	}
}

func TestReplacementAndQuarantine(t *testing.T) {
	ctx, s, pool := testStore(t)
	key := testKey()
	if _, err := s.Enqueue(ctx, key, "cache_miss", false); err != nil {
		t.Fatal(err)
	}
	lease := requireLease(t, ctx, s)
	if err := s.Publish(ctx, *lease, syntheticSnapshot("1")); err != nil {
		t.Fatal(err)
	}
	before, err := s.Find(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE refresh_jobs SET created_at=clock_timestamp()-interval '2 minutes' WHERE id=$1`, lease.Job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Enqueue(ctx, key, "user_refresh", true); err != nil {
		t.Fatal(err)
	}
	lease = requireLease(t, ctx, s)
	if err = s.Quarantine(ctx, *lease); err != nil {
		t.Fatal(err)
	}
	blocked, err := s.Find(ctx, key)
	if err != nil || !blocked.Blocked {
		t.Fatalf("quarantine not persisted: %v", err)
	}
	if err = s.Publish(ctx, *lease, syntheticSnapshot("2")); err != nil {
		t.Fatal(err)
	}
	after, err := s.Find(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if before.ID == after.ID || after.Snapshot != nil || after.LastJob == nil || after.LastJob.Status != "queued" {
		t.Fatalf("incorrect replacement: %+v", after)
	}
	if _, err = s.GetJob(ctx, lease.Job.ID); !errors.Is(err, character.ErrNotFound) {
		t.Fatalf("old job survived: %v", err)
	}
	lease = requireLease(t, ctx, s)
	if err = s.Publish(ctx, *lease, syntheticSnapshot("2")); err != nil {
		t.Fatal(err)
	}
	after, err = s.Find(ctx, key)
	if err != nil || after.Blocked || after.Snapshot == nil {
		t.Fatalf("replacement publish: %v", err)
	}
}

func TestSectionRetentionAndFailureCooldown(t *testing.T) {
	ctx, s, pool := testStore(t)
	key := testKey()
	if _, err := s.Enqueue(ctx, key, "cache_miss", false); err != nil {
		t.Fatal(err)
	}
	lease := requireLease(t, ctx, s)
	snap := syntheticSnapshot("1")
	gear := snap.Sections["equipment"]
	fetched := time.Now().UTC().Add(-31 * 24 * time.Hour)
	fresh := fetched.Add(15 * time.Minute)
	valid := fetched.Add(720 * time.Hour)
	gear.FetchedAt = &fetched
	gear.FreshUntil = &fresh
	gear.ValidUntil = &valid
	snap.Sections["equipment"] = gear
	if err := s.Publish(ctx, *lease, snap); err != nil {
		t.Fatal(err)
	}
	if err := s.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	rec, err := s.Find(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Snapshot.Sections["equipment"].State != "unavailable" || rec.Snapshot.Sections["profile"].State != "complete" {
		t.Fatal("per-section expiry failed")
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM equipment_items`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expired child rows=%d err=%v", count, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE refresh_jobs SET created_at=clock_timestamp()-interval '2 minutes' WHERE id=$1`, lease.Job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Enqueue(ctx, key, "user_refresh", true); err != nil {
		t.Fatal(err)
	}
	lease = requireLease(t, ctx, s)
	if err = s.Fail(ctx, *lease, character.NewFault("PROVIDER_AUTH_FAILURE", false), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	_, err = s.Enqueue(ctx, key, "cache_miss", false)
	fault := character.AsFault(err)
	if err == nil || fault.Code != "PROVIDER_AUTH_FAILURE" || fault.RetryAt.IsZero() || fault.JobID == nil || *fault.JobID != lease.Job.ID {
		t.Fatalf("terminal cooldown not enforced: %v", err)
	}
}

func TestLookupNameCatalogCursorAndCapacity(t *testing.T) {
	ctx, s, pool := testStore(t)
	key, err := character.NormalizeKey("eu", "synthetic", "Straße")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Enqueue(ctx, key, "cache_miss", false); err != nil {
		t.Fatal(err)
	}
	lease, err := s.Claim(ctx, "synthetic-worker")
	if err != nil || lease == nil {
		t.Fatalf("claim: %v", err)
	}
	if lease.Job.Key.Name != "strasse" || lease.Job.Key.LookupName != "Straße" {
		t.Fatalf("lookup name or folded identity lost: %+v", lease.Job.Key)
	}
	s.options.QueueCapacity = 1
	_, err = s.Enqueue(ctx, character.Key{Region: "eu", Realm: "synthetic", Name: "другой"}, "cache_miss", false)
	if err == nil || character.AsFault(err).Code != "QUEUE_FULL" {
		t.Fatalf("capacity: %v", err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM characters`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rejected admission left identity count=%d err=%v", count, err)
	}
	if err = s.UpsertRealms(ctx, []character.Realm{{ProviderID: "2", Region: "eu", Slug: "synthetic-two", Names: map[string]string{"en-US": "Synthetic Two"}, FetchedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	page, cursor, err := s.ListRealms(ctx, "eu", "ru-RU", "", "", 1)
	if err != nil || len(page) != 1 || cursor == "" {
		t.Fatalf("first cursor page: %v %s", err, cursor)
	}
	second, next, err := s.ListRealms(ctx, "eu", "ru-RU", "", cursor, 1)
	if err != nil || len(second) != 1 || next != "" || page[0].ID == second[0].ID {
		t.Fatalf("second cursor page: %v", err)
	}
	if _, _, err = s.ListRealms(ctx, "eu", "en-US", "", cursor, 1); err == nil || character.AsFault(err).Code != "INVALID_ARGUMENT" {
		t.Fatalf("cross-locale cursor accepted: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE realms SET fetched_at=fetched_at+interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ListRealms(ctx, "eu", "ru-RU", "", cursor, 1); err == nil || character.AsFault(err).Code != "INVALID_ARGUMENT" {
		t.Fatalf("old catalog cursor accepted: %v", err)
	}
	metrics, err := s.Metrics(ctx)
	if err != nil || metrics["queue_running"] != 1 || metrics["queue_active"] != 1 {
		t.Fatalf("metrics=%v err=%v", metrics, err)
	}
}

func TestIdentityDeadlineIgnoresOperationalUpdates(t *testing.T) {
	ctx, s, pool := testStore(t)
	key := testKey()
	if _, err := s.Enqueue(ctx, key, "cache_miss", false); err != nil {
		t.Fatal(err)
	}
	lease := requireLease(t, ctx, s)
	if err := s.Publish(ctx, *lease, syntheticSnapshot("1")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE characters SET data_valid_until=clock_timestamp()-interval '1 second',updated_at=clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	if err := s.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Find(ctx, key); !errors.Is(err, character.ErrNotFound) {
		t.Fatalf("expired identity survived recent operational update: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM character_snapshots`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expired identity snapshot count=%d err=%v", count, err)
	}
}

func TestQuarantineCooldownAndSuccessfulRecheck(t *testing.T) {
	ctx, s, pool := testStore(t)
	key := testKey()
	if _, err := s.Enqueue(ctx, key, "cache_miss", false); err != nil {
		t.Fatal(err)
	}
	initial := requireLease(t, ctx, s)
	if err := s.Publish(ctx, *initial, syntheticSnapshot("1")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE refresh_jobs SET created_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, initial.Job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enqueue(ctx, key, "user_refresh", true); err != nil {
		t.Fatal(err)
	}
	denied := requireLease(t, ctx, s)
	if err := s.Quarantine(ctx, *denied); err != nil {
		t.Fatal(err)
	}
	if err := s.Fail(ctx, *denied, character.NewFault("PROVIDER_ACCESS_DENIED", false), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	_, err := s.Enqueue(ctx, key, "stale_read", false)
	fault := character.AsFault(err)
	if err == nil || fault.Code != "PROVIDER_ACCESS_DENIED" || fault.JobID == nil || *fault.JobID != denied.Job.ID || !fault.RetryAt.After(time.Now()) {
		t.Fatalf("quarantine cooldown missing failed job context: %+v, error %v", fault, err)
	}
	blocked, err := s.Find(ctx, key)
	if err != nil || !blocked.Blocked || blocked.LastJob == nil || blocked.LastJob.Status != "failed" {
		t.Fatalf("quarantine lost after failure: %+v %v", blocked, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE refresh_jobs SET created_at=clock_timestamp()-interval '16 minutes',finished_at=clock_timestamp()-interval '16 minutes',run_after=clock_timestamp()-interval '16 minutes' WHERE id=$1`, denied.Job.ID); err != nil {
		t.Fatal(err)
	}
	accepted, err := s.Enqueue(ctx, key, "user_refresh", true)
	if err != nil || !accepted.Created || accepted.Job.ID == denied.Job.ID {
		t.Fatalf("refresh after cooldown: %+v %v", accepted, err)
	}
	blocked, err = s.Find(ctx, key)
	if err != nil || !blocked.Blocked {
		t.Fatalf("enqueue prematurely removed quarantine: %v", err)
	}
	recheck := requireLease(t, ctx, s)
	if err = s.Publish(ctx, *recheck, syntheticSnapshot("1")); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.Find(ctx, key)
	if err != nil || recovered.Blocked || recovered.Snapshot == nil || recovered.LastJob == nil || recovered.LastJob.Status != "succeeded" {
		t.Fatalf("successful recheck did not recover publication: %+v %v", recovered, err)
	}
}

func TestProviderModeBinding(t *testing.T) {
	t.Run("fresh database binds once", func(t *testing.T) {
		ctx, s, pool := testStore(t)
		if _, err := pool.Exec(ctx, `DELETE FROM realms`); err != nil {
			t.Fatal(err)
		}
		if err := s.CheckProviderMode(ctx, "fixture"); err != nil {
			t.Fatal(err)
		}
		if err := s.CheckProviderMode(ctx, "fixture"); err != nil {
			t.Fatalf("same provider mode rejected: %v", err)
		}
		if err := s.CheckProviderMode(ctx, "blizzard"); !errors.Is(err, ErrProviderModeMismatch) {
			t.Fatalf("cross-mode database accepted: %v", err)
		}
		var stored string
		if err := pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key='provider_mode'`).Scan(&stored); err != nil || stored != "fixture" {
			t.Fatalf("binding changed: %s %v", stored, err)
		}
	})
	t.Run("populated legacy database refuses adoption", func(t *testing.T) {
		ctx, s, pool := testStore(t)
		for _, mode := range []string{"fixture", "blizzard"} {
			if err := s.CheckProviderMode(ctx, mode); !errors.Is(err, ErrProviderModeUnmarked) {
				t.Fatalf("unmarked populated database adopted %s: %v", mode, err)
			}
		}
		var settings, realms int
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM app_settings),(SELECT count(*) FROM realms)`).Scan(&settings, &realms); err != nil || settings != 0 || realms != 1 {
			t.Fatalf("refusal changed data: settings=%d realms=%d error=%v", settings, realms, err)
		}
	})
	t.Run("concurrent first boot preserves one provenance", func(t *testing.T) {
		ctx, s, pool := testStore(t)
		if _, err := pool.Exec(ctx, `DELETE FROM realms`); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for _, mode := range []string{"fixture", "blizzard"} {
			wg.Add(1)
			go func(mode string) { defer wg.Done(); results <- s.CheckProviderMode(ctx, mode) }(mode)
		}
		wg.Wait()
		close(results)
		accepted, rejected := 0, 0
		for err := range results {
			if err == nil {
				accepted++
			} else if errors.Is(err, ErrProviderModeMismatch) {
				rejected++
			} else {
				t.Fatal(err)
			}
		}
		if accepted != 1 || rejected != 1 {
			t.Fatalf("simultaneous modes accepted=%d rejected=%d", accepted, rejected)
		}
	})
}
