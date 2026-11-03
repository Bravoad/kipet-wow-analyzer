package postgres

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/migrations"
)

func syntheticProgressSnapshot() character.Snapshot {
	snapshot := syntheticSnapshot("1")
	section := snapshot.Sections["statistics"]
	section.Payload = json.RawMessage(`{"character_id":"00000000-0000-4000-8000-000000000099","season":{"id":"900005001","name":null,"is_current":null},"rating":null,"run_scope":"season_best","best_runs":[],"available_seasons":[]}`)
	snapshot.Sections["mythic_plus"] = section
	section.Payload = json.RawMessage(`{"character_id":"00000000-0000-4000-8000-000000000099","expansions":[]}`)
	snapshot.Sections["raids"] = section
	return snapshot
}

func TestProgressSectionsPersistIdentityAndPurge(t *testing.T) {
	ctx, store, pool := testStore(t)
	store.options.RefreshCooldown = time.Nanosecond
	if _, err := store.Enqueue(ctx, testKey(), "cache_miss", false); err != nil {
		t.Fatal(err)
	}
	lease := requireLease(t, ctx, store)
	snapshot := syntheticProgressSnapshot()
	snapshot.ID = uuid.NewString()
	// PostgreSQL timestamps retain microseconds, so compare at storage precision.
	older := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	fresh := older.Add(15 * time.Minute)
	valid := older.Add(720 * time.Hour)
	progress := snapshot.Sections["mythic_plus"]
	progress.FetchedAt = &older
	progress.FreshUntil = &fresh
	progress.ValidUntil = &valid
	snapshot.Sections["mythic_plus"] = progress
	if err := store.Publish(ctx, *lease, snapshot); err != nil {
		t.Fatal(err)
	}
	record, err := store.Find(ctx, testKey())
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Snapshot.Sections) != 6 {
		t.Fatalf("persisted sections=%d, want6", len(record.Snapshot.Sections))
	}
	for _, name := range []string{"mythic_plus", "raids"} {
		section := record.Snapshot.Sections[name]
		if section.State != "complete" || !section.Publishable(time.Now()) {
			t.Fatalf("%s was not published: %+v", name, section)
		}
		var payload struct {
			CharacterID string `json:"character_id"`
		}
		if json.Unmarshal(section.Payload, &payload) != nil || payload.CharacterID != record.ID {
			t.Fatalf("%s leaked provider/internal placeholder identity", name)
		}
	}
	stored := record.Snapshot.Sections["mythic_plus"]
	if stored.FetchedAt == nil || !stored.FetchedAt.Equal(older) || stored.Fresh(time.Now()) {
		t.Fatal("progress received a false fresh timestamp")
	}
	if _, err = store.Enqueue(ctx, testKey(), "revalidation", false); err != nil {
		t.Fatal(err)
	}
	lease = requireLease(t, ctx, store)
	if err = store.Purge(ctx, *lease); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Find(ctx, testKey()); !errors.Is(err, character.ErrNotFound) {
		t.Fatal("purged character remains readable")
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM snapshot_sections WHERE snapshot_id=$1`, snapshot.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("progress did not cascade: count=%d err=%v", count, err)
	}
}

func TestLegacySnapshotsAcquireUnknownProgressSections(t *testing.T) {
	ctx, store, pool, snapshot := storedAnalysisSnapshot(t)
	for _, name := range []string{"mythic_plus", "raids"} {
		section := snapshot.Sections[name]
		if section.State != "unavailable" || section.Reason == nil || *section.Reason != "never_fetched" || section.Payload != nil || section.FetchedAt != nil {
			t.Fatalf("missing %s became observed empty data: %+v", name, section)
		}
	}
	profileBefore := append([]byte(nil), snapshot.Sections["profile"].Payload...)
	rollbackThrough(t, ctx, pool, "0006_progress_sections.up.sql")
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM snapshot_sections WHERE snapshot_id=$1`, snapshot.ID).Scan(&count); err != nil || count != 4 {
		t.Fatalf("rollback did not preserve old core shape: count=%d err=%v", count, err)
	}
	if err := migrations.Up(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx, pool); err != nil {
		t.Fatal(err)
	}
	record, err := store.Find(ctx, testKey())
	if err != nil {
		t.Fatal(err)
	}
	if record.Snapshot.ID != snapshot.ID || len(record.Snapshot.Sections) != 6 || string(record.Snapshot.Sections["profile"].Payload) != string(profileBefore) {
		t.Fatal("progress backfill changed existing profile identity/data")
	}
	for _, name := range []string{"mythic_plus", "raids"} {
		section := record.Snapshot.Sections[name]
		if section.State != "unavailable" || section.Payload != nil || section.Reason == nil || *section.Reason != "never_fetched" {
			t.Fatalf("legacy %s was not explicitly unknown", name)
		}
	}
}

func TestProgressExpiryIsIsolatedFromCoreAnalysis(t *testing.T) {
	ctx, store, pool := testStore(t)
	if _, err := store.Enqueue(ctx, testKey(), "cache_miss", false); err != nil {
		t.Fatal(err)
	}
	lease := requireLease(t, ctx, store)
	snapshot := syntheticProgressSnapshot()
	snapshot.ID = uuid.NewString()
	earlier := time.Now().UTC().Add(-time.Minute)
	nearFresh := time.Now().UTC().Add(time.Second)
	nearValid := time.Now().UTC().Add(time.Minute)
	section := snapshot.Sections["mythic_plus"]
	section.FetchedAt = &earlier
	section.FreshUntil = &nearFresh
	section.ValidUntil = &nearValid
	snapshot.Sections["mythic_plus"] = section
	if err := store.Publish(ctx, *lease, snapshot); err != nil {
		t.Fatal(err)
	}
	report := syntheticAnalysisPayload(snapshot.ID, testAnalysisContent, "core-rules-only")
	if err := store.SaveAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent, report, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var deadline time.Time
	if err := pool.QueryRow(ctx, `SELECT valid_until FROM analysis_results WHERE snapshot_id=$1`, snapshot.ID).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	if !deadline.After(nearValid) {
		t.Fatal("progress deadline incorrectly bounded unrelated core analysis")
	}
	if _, err := pool.Exec(ctx, `UPDATE snapshot_sections SET fetched_at=clock_timestamp()-interval '2 hours',fresh_until=clock_timestamp()-interval '1 hour',valid_until=clock_timestamp()-interval '1 second' WHERE snapshot_id=$1 AND section='mythic_plus'`, snapshot.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent)
	if err != nil || analysisMarker(t, loaded) != "core-rules-only" {
		t.Fatalf("expired progress invalidated core analysis: %v", err)
	}
	record, err := store.Find(ctx, testKey())
	if err != nil {
		t.Fatal(err)
	}
	if record.Snapshot.Sections["mythic_plus"].Publishable(time.Now()) || !record.Snapshot.Sections["raids"].Publishable(time.Now()) {
		t.Fatal("section-specific expiry was lost")
	}
	if err = store.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	record, err = store.Find(ctx, testKey())
	if err != nil {
		t.Fatal(err)
	}
	cleared := record.Snapshot.Sections["mythic_plus"]
	if cleared.State != "unavailable" || cleared.Payload != nil || cleared.ValidUntil != nil || cleared.Reason == nil || *cleared.Reason != "expired" {
		t.Fatal("expired progress payload survived cleanup")
	}
	loaded, err = store.LoadAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent)
	if err != nil || analysisMarker(t, loaded) != "core-rules-only" {
		t.Fatalf("progress GC deleted unrelated core analysis: %v", err)
	}
}
