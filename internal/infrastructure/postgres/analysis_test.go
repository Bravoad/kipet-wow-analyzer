package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"kipet-wow-analyzer/internal/domain/character"
)

const testAnalysisContent = "general_pve"

var testEngineDigest = strings.Repeat("a", 64)

func storedAnalysisSnapshot(t *testing.T) (context.Context, *Store, *pgxpool.Pool, character.Snapshot) {
	t.Helper()
	ctx, store, pool := testStore(t)
	if _, err := store.Enqueue(ctx, testKey(), "cache_miss", false); err != nil {
		t.Fatal(err)
	}
	lease := requireLease(t, ctx, store)
	snapshot := syntheticSnapshot("1")
	snapshot.ID = uuid.NewString()
	if err := store.Publish(ctx, *lease, snapshot); err != nil {
		t.Fatal(err)
	}
	record, err := store.Find(ctx, testKey())
	if err != nil {
		t.Fatal(err)
	}
	return ctx, store, pool, *record.Snapshot
}

func syntheticAnalysisPayload(snapshotID, content, marker string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{
		"snapshot_id":      snapshotID,
		"context":          map[string]any{"content": content},
		"synthetic_marker": marker,
	})
	return raw
}

func analysisMarker(t *testing.T, payload json.RawMessage) string {
	t.Helper()
	var out struct {
		Marker string `json:"synthetic_marker"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatal(err)
	}
	return out.Marker
}

func requireAnalysisMiss(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, character.ErrNotFound) {
		t.Fatalf("want analysis cache miss, got %v", err)
	}
}

func TestAnalysisCacheIsIdempotentContextualAndFreshnessBounded(t *testing.T) {
	ctx, s, pool, snapshot := storedAnalysisSnapshot(t)
	first := syntheticAnalysisPayload(snapshot.ID, testAnalysisContent, "original")
	if err := s.SaveAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent, first, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var deadline time.Time
	if err := pool.QueryRow(ctx, `SELECT valid_until FROM analysis_results WHERE snapshot_id=$1`, snapshot.ID).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	profileFresh := snapshot.Sections["profile"].FreshUntil
	if profileFresh == nil || deadline.After(profileFresh.Add(time.Microsecond)) {
		t.Fatal("cache deadline exceeded the next freshness transition")
	}
	changed := syntheticAnalysisPayload(snapshot.ID, testAnalysisContent, "replacement")
	if err := s.SaveAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent, changed, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent)
	if err != nil || analysisMarker(t, loaded) != "original" {
		t.Fatalf("unexpired key was overwritten: %s / %v", loaded, err)
	}
	_, err = s.LoadAnalysis(ctx, snapshot.ID, strings.Repeat("b", 64), testAnalysisContent)
	requireAnalysisMiss(t, err)
	_, err = s.LoadAnalysis(ctx, snapshot.ID, testEngineDigest, "raid")
	requireAnalysisMiss(t, err)
	if _, err = pool.Exec(ctx, `UPDATE analysis_results SET valid_until=clock_timestamp()-interval '1 second' WHERE snapshot_id=$1`, snapshot.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.LoadAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent)
	requireAnalysisMiss(t, err)
	if err = s.SaveAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent, changed, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	loaded, err = s.LoadAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent)
	if err != nil || analysisMarker(t, loaded) != "replacement" {
		t.Fatalf("expired report did not refresh: %s / %v", loaded, err)
	}
}

func TestAnalysisCacheCannotBypassSectionExpiryOrCleanup(t *testing.T) {
	ctx, s, pool, snapshot := storedAnalysisSnapshot(t)
	report := syntheticAnalysisPayload(snapshot.ID, testAnalysisContent, "contains-equipment-evidence")
	if err := s.SaveAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent, report, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Simulate an earlier hard deadline imposed by a source-data correction.
	if _, err := pool.Exec(ctx, `UPDATE snapshot_sections SET fetched_at=clock_timestamp()-interval '2 hours',
	 fresh_until=clock_timestamp()-interval '1 hour',valid_until=clock_timestamp()-interval '1 second'
	 WHERE snapshot_id=$1 AND section='equipment'`, snapshot.ID); err != nil {
		t.Fatal(err)
	}
	_, err := s.LoadAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent)
	requireAnalysisMiss(t, err)
	requireAnalysisMiss(t, s.SaveAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent, report, time.Now().Add(time.Minute)))
	if err = s.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM analysis_results WHERE snapshot_id=$1`, snapshot.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("source-derived report survived cleanup: count=%d err=%v", count, err)
	}
	_, err = s.LoadAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent)
	requireAnalysisMiss(t, err)
	// A new report can now describe equipment as unavailable without old evidence.
	unknown := syntheticAnalysisPayload(snapshot.ID, testAnalysisContent, "equipment-unknown")
	if err = s.SaveAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent, unknown, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestAnalysisCacheCannotBypassQuarantinePurgeOrReplacement(t *testing.T) {
	ctx, s, pool, snapshot := storedAnalysisSnapshot(t)
	s.options.RefreshCooldown = time.Nanosecond
	report := syntheticAnalysisPayload(snapshot.ID, testAnalysisContent, "permitted")
	if err := s.SaveAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent, report, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enqueue(ctx, testKey(), "revalidation", false); err != nil {
		t.Fatal(err)
	}
	lease := requireLease(t, ctx, s)
	if err := s.Quarantine(ctx, *lease); err != nil {
		t.Fatal(err)
	}
	_, err := s.LoadAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent)
	requireAnalysisMiss(t, err)
	requireAnalysisMiss(t, s.SaveAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent, report, time.Now().Add(time.Minute)))
	if err = s.Publish(ctx, *lease, syntheticSnapshot("1")); err != nil {
		t.Fatal(err)
	}
	_, err = s.LoadAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent)
	requireAnalysisMiss(t, err)
	requireAnalysisMiss(t, s.SaveAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent, report, time.Now().Add(time.Minute)))
	if _, err = s.Enqueue(ctx, testKey(), "revalidation", false); err != nil {
		t.Fatal(err)
	}
	lease = requireLease(t, ctx, s)
	if err = s.Purge(ctx, *lease); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM analysis_results`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("analysis did not cascade with purge: count=%d err=%v", count, err)
	}
	requireAnalysisMiss(t, s.SaveAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent, report, time.Now().Add(time.Minute)))
}

func TestConcurrentAnalysisWritesKeepOneImmutableReport(t *testing.T) {
	ctx, s, pool, snapshot := storedAnalysisSnapshot(t)
	var group sync.WaitGroup
	errors := make(chan error, 12)
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			payload := syntheticAnalysisPayload(snapshot.ID, testAnalysisContent, fmt.Sprintf("candidate-%d", i))
			errors <- s.SaveAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent, payload, time.Now().Add(time.Minute))
		}(i)
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM analysis_results`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate analysis reports: count=%d err=%v", count, err)
	}
	loaded, err := s.LoadAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent)
	if err != nil || !strings.HasPrefix(analysisMarker(t, loaded), "candidate-") {
		t.Fatalf("invalid winning report: %s / %v", loaded, err)
	}
}

func TestAnalysisCacheRejectsMismatchedReportAndInvalidKeys(t *testing.T) {
	ctx, s, _, snapshot := storedAnalysisSnapshot(t)
	valid := syntheticAnalysisPayload(snapshot.ID, testAnalysisContent, "valid")
	for _, input := range []struct {
		id, digest, content string
		payload             json.RawMessage
	}{
		{snapshot.ID, "not-a-digest", testAnalysisContent, valid},
		{"not-a-uuid", testEngineDigest, testAnalysisContent, valid},
		{snapshot.ID, testEngineDigest, "pvp", valid},
		{snapshot.ID, testEngineDigest, testAnalysisContent, syntheticAnalysisPayload(uuid.NewString(), testAnalysisContent, "other-snapshot")},
		{snapshot.ID, testEngineDigest, testAnalysisContent, syntheticAnalysisPayload(snapshot.ID, "raid", "other-context")},
		{snapshot.ID, testEngineDigest, testAnalysisContent, json.RawMessage(`{`)},
	} {
		if err := s.SaveAnalysis(ctx, input.id, input.digest, input.content, input.payload, time.Now().Add(time.Minute)); !errors.Is(err, ErrInvalidAnalysis) {
			t.Fatalf("invalid report accepted: %+v: %v", input, err)
		}
	}
	requireAnalysisMiss(t, s.SaveAnalysis(ctx, snapshot.ID, testEngineDigest, testAnalysisContent, valid, time.Now().Add(-time.Minute)))
}
