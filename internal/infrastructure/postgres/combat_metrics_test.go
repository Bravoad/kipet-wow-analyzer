package postgres

import "testing"

func TestCombatMetricsKeepUnknownPointsDistinctFromZero(t *testing.T) {
	ctx, store, pool := testStore(t)
	metrics, err := store.Metrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if metrics["warcraftlogs_points_known"] != 0 {
		t.Fatal("unknown quota invented")
	}
	if _, exists := metrics["warcraftlogs_points_remaining"]; exists {
		t.Fatal("unknown quota became zero")
	}
	if _, err = store.AdmitCombat(ctx, "DEMOCombat000001", 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO provider_state(provider,scope,budget_remaining,budget_resets_at) VALUES('warcraftlogs','global',0,clock_timestamp()+interval '1 hour')`); err != nil {
		t.Fatal(err)
	}
	metrics, err = store.Metrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if metrics["combat_queue_active"] != 1 || metrics["warcraftlogs_points_known"] != 1 || metrics["warcraftlogs_points_remaining"] != 0 || metrics["warcraftlogs_points_reset_seconds"] <= 0 {
		t.Fatalf("wrong aggregate metrics: %+v", metrics)
	}
	if _, err = pool.Exec(ctx, `UPDATE provider_state SET budget_resets_at=clock_timestamp()-interval '1 second' WHERE provider='warcraftlogs'`); err != nil {
		t.Fatal(err)
	}
	metrics, err = store.Metrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if metrics["warcraftlogs_points_known"] != 0 {
		t.Fatal("expired observation still current")
	}
}
