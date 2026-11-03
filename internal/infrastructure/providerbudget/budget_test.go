package providerbudget

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"kipet-wow-analyzer/migrations"
)

func TestProviderBudgetsAndObservedPointsAreSharedAndIsolated(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "budget_" + uuid.New().String()[:8]
	if _, err = admin.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`) }()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = migrations.Up(ctx, pool); err != nil {
		t.Fatal(err)
	}
	blizzard := &Budget{Pool: pool}
	wcl := &Budget{Pool: pool, Provider: "warcraftlogs"}
	if err = blizzard.Wait(ctx, "eu"); err != nil {
		t.Fatal(err)
	}
	if err = wcl.Wait(ctx, "global"); err != nil {
		t.Fatal(err)
	}
	// A separate worker shares WCL's spacing, without consuming Blizzard's row.
	other := &Budget{Pool: pool, Provider: "warcraftlogs"}
	short, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	err = other.Wait(short, "global")
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shared spacing not enforced: %v", err)
	}
	reset := time.Now().Add(time.Hour)
	if err = wcl.ObserveUsage(ctx, 3, reset); err != nil {
		t.Fatal(err)
	}
	if err = other.ObserveUsage(ctx, 9, reset); err != nil {
		t.Fatal(err)
	}
	var remaining float64
	if err = pool.QueryRow(ctx, `SELECT budget_remaining::float8 FROM provider_state WHERE provider='warcraftlogs'`).Scan(&remaining); err != nil || remaining != 3 {
		t.Fatalf("out-of-order usage refilled: %v %v", remaining, err)
	}
	if err = wcl.ObserveUsage(ctx, 0, reset); err != nil {
		t.Fatal(err)
	}
	short, stop = context.WithTimeout(ctx, 30*time.Millisecond)
	err = other.Wait(short, "global")
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("exhausted points ignored: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE provider_state SET next_allowed_at=NULL WHERE provider='blizzard'`); err != nil {
		t.Fatal(err)
	}
	if err = blizzard.Wait(ctx, "us"); err != nil {
		t.Fatal(err)
	}
	// The provider's next points window may replenish; no process restart needed.
	if _, err = pool.Exec(ctx, `UPDATE provider_state SET budget_resets_at=clock_timestamp()-interval '1 second',next_allowed_at=NULL WHERE provider='warcraftlogs'`); err != nil {
		t.Fatal(err)
	}
	if err = wcl.ObserveUsage(ctx, 10, reset.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = other.Wait(ctx, "global"); err != nil {
		t.Fatal(err)
	}
}
