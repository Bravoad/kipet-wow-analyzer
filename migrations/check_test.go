package migrations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func checkDatabase(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL required for real PostgreSQL check tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	root, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "kipet_check_test_" + hex.EncodeToString(random[:])
	if _, err = root.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		root.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = root.Exec(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`); root.Close() })
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return ctx, pool
}

func TestCheckIsReadOnlyAndRejectsMissingMigration(t *testing.T) {
	ctx, pool := checkDatabase(t)
	if err := Check(ctx, pool); !errors.Is(err, ErrSchemaNotReady) {
		t.Fatal("empty database accepted", err)
	}
	var created bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&created); err != nil || created {
		t.Fatal("readiness created ledger", err)
	}
	if err := Up(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := Check(ctx, pool); err != nil {
		t.Fatal("applied database rejected", err)
	}
	if err := Down(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := Check(ctx, pool); !errors.Is(err, ErrSchemaNotReady) {
		t.Fatal("incomplete database accepted", err)
	}
	var raiderKind bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_attribute
		WHERE attrelid='raiderio_profiles'::regclass AND attname='kind' AND NOT attisdropped)`).Scan(&raiderKind); err != nil || raiderKind {
		t.Fatal("readiness reapplied missing migration", err)
	}
}
func TestCheckRejectsCorruptedAndUnknownMigrationLedgers(t *testing.T) {
	for _, kind := range []string{"checksum", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			ctx, pool := checkDatabase(t)
			if err := Up(ctx, pool); err != nil {
				t.Fatal(err)
			}
			query := `UPDATE schema_migrations SET checksum='changed' WHERE version='0001_initial.up.sql'`
			if kind == "unknown" {
				query = `INSERT INTO schema_migrations(version,checksum) VALUES('9999_future.up.sql','future-binary')`
			}
			if _, err := pool.Exec(ctx, query); err != nil {
				t.Fatal(err)
			}
			if err := Check(ctx, pool); !errors.Is(err, ErrSchemaNotReady) {
				t.Fatal("incompatible ledger accepted", err)
			}
		})
	}
}
