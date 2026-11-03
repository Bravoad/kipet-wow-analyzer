package postgres

import (
	"testing"

	"kipet-wow-analyzer/internal/domain/guild"
)

func TestMetricsSeparateDelayedWorkOverdueWorkAndExpiredLeases(t *testing.T) {
	ctx, store, pool := testStore(t)
	metrics, err := store.Metrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"", "combat_", "guild_"} {
		for _, name := range []string{"queue_active", "queue_running", "queue_due", "queue_retry_wait", "queue_failed", "queue_expired_leases", "oldest_pending_seconds", "oldest_due_seconds"} {
			if value, exists := metrics[prefix+name]; !exists || value != 0 {
				t.Fatal("empty queue lost zero", prefix+name, value, exists)
			}
		}
	}
	if _, err = store.Enqueue(ctx, testKey(), "cache_miss", false); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE refresh_jobs SET created_at=clock_timestamp()-interval '10 minutes',run_after=clock_timestamp()-interval '5 minutes'`); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"first-guild", "second-guild"} {
		if _, err = store.AdmitGuild(ctx, guild.Key{Region: "eu", Realm: "synthetic", Slug: slug}, false); err != nil {
			t.Fatal(err)
		}
	}
	lease, err := store.ClaimGuild(ctx, "operational-test")
	if err != nil || lease == nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE guild_jobs SET created_at=clock_timestamp()-interval '10 minutes',lease_until=clock_timestamp()-interval '1 second' WHERE status='running'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE guild_jobs SET run_after=clock_timestamp()+interval '5 minutes' WHERE status='queued'`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.AdmitCombat(ctx, "DEMOCombat000001", 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE combat_jobs SET status='retry_wait',run_after=clock_timestamp()+interval '5 minutes'`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.AdmitCombat(ctx, "DEMOCombat000002", 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE combat_jobs SET status='failed',finished_at=clock_timestamp(),last_error_code='PROVIDER_UNAVAILABLE' WHERE report_code='DEMOCombat000002'`); err != nil {
		t.Fatal(err)
	}
	metrics, err = store.Metrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]float64{
		"queue_active": 1, "queue_running": 0, "queue_due": 1, "queue_expired_leases": 0,
		"guild_queue_active": 2, "guild_queue_running": 1, "guild_queue_due": 0, "guild_queue_expired_leases": 1,
		"combat_queue_active": 1, "combat_queue_running": 0, "combat_queue_due": 0, "combat_queue_retry_wait": 1, "combat_queue_failed": 1,
	} {
		if metrics[name] != want {
			t.Fatalf("%s=%v want %v", name, metrics[name], want)
		}
	}
	if metrics["oldest_due_seconds"] < 299 || metrics["oldest_due_seconds"] > 310 || metrics["oldest_pending_seconds"] < 599 || metrics["guild_oldest_pending_seconds"] < 599 {
		t.Fatal("age of due work not measured independently", metrics)
	}
	if metrics["guild_oldest_due_seconds"] != 0 || metrics["combat_oldest_due_seconds"] != 0 {
		t.Fatal("future retry created false overdue alert", metrics)
	}
}
