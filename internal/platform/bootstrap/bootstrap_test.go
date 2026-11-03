package bootstrap

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"kipet-wow-analyzer/internal/infrastructure/postgres"
	"kipet-wow-analyzer/internal/platform/config"
	"kipet-wow-analyzer/migrations"
)

func TestCombatDatabaseKeepsFixtureMarkerAndQueuesSeparate(t *testing.T) {
	rawURL := os.Getenv("TEST_DATABASE_URL")
	if rawURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	primaryURL, dropPrimary := migratedTestSchema(t, ctx, admin, rawURL)
	defer dropPrimary()
	combatURL, dropCombat := migratedTestSchema(t, ctx, admin, rawURL)
	defer dropCombat()
	c := config.Config{
		DatabaseURL: primaryURL, RedisURL: "redis://127.0.0.1:6379/0",
		ProviderMode: "fixture", CombatMode: "fixture", HMACSecret: "synthetic-test-configuration-32bytes",
	}
	fixture, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.CombatStore.AdmitCombat(ctx, "FixtureReport001", 0, 0)
	fixture.Close()
	if err != nil {
		t.Fatal(err)
	}

	c.CombatMode = "warcraftlogs"
	if _, err = Open(ctx, c); !errors.Is(err, postgres.ErrProviderModeMismatch) {
		t.Fatalf("expected rejection of fixture combat database, got %v", err)
	}
	c.CombatDatabaseURL = combatURL
	live, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if err := live.Store.CheckCombatMode(ctx, "fixture"); err != nil {
		t.Fatal("primary database marker changed", err)
	}
	if err := live.CombatStore.CheckCombatMode(ctx, "warcraftlogs"); err != nil {
		t.Fatal("secondary database marker is not bound to Warcraft Logs", err)
	}
	metrics, err := live.Metrics(ctx)
	if err != nil || metrics["combat_queue_active"] != 0 {
		t.Fatalf("fixture jobs appeared in real combat queue metrics: %v, %v", metrics, err)
	}
}

func migratedTestSchema(t *testing.T, ctx context.Context, admin *pgxpool.Pool, rawURL string) (string, func()) {
	t.Helper()
	name := "bootstrap_test_" + uuid.NewString()
	identifier := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	drop := func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		drop()
		t.Fatal("invalid TEST_DATABASE_URL")
	}
	query := u.Query()
	query.Set("search_path", `"`+name+`"`)
	u.RawQuery = query.Encode()
	schemaURL := u.String()
	pool, err := pgxpool.New(ctx, schemaURL)
	if err != nil {
		drop()
		t.Fatal("invalid test schema database configuration")
	}
	err = migrations.Up(ctx, pool)
	pool.Close()
	if err != nil {
		drop()
		t.Fatal(err)
	}
	return schemaURL, drop
}
