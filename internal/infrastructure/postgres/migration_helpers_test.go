package postgres

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"kipet-wow-analyzer/migrations"
	"testing"
)

func rollbackThrough(t *testing.T, ctx context.Context, pool *pgxpool.Pool, target string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		var latest string
		if err := pool.QueryRow(ctx, `SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&latest); err != nil {
			t.Fatal(err)
		}
		if latest < target {
			t.Fatalf("rollback target absent: %s", target)
		}
		if err := migrations.Down(ctx, pool); err != nil {
			t.Fatal(err)
		}
		if latest == target {
			return
		}
	}
	t.Fatal("migration rollback did not reach target")
}
