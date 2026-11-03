// Package migrations applies the application's versioned PostgreSQL schema.
package migrations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed *.up.sql *.down.sql
var files embed.FS

const advisoryLock int64 = 748215930001

// Up applies outstanding migrations. A session lock serializes separate API and
// worker processes; each migration and its ledger entry commit atomically.
func Up(ctx context.Context, pool *pgxpool.Pool) error {
	return locked(ctx, pool, func(conn *pgxpool.Conn) error {
		if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
			version text PRIMARY KEY, checksum text NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
			return fmt.Errorf("create migration ledger: %w", err)
		}
		entries, err := files.ReadDir(".")
		if err != nil {
			return err
		}
		names := make([]string, 0)
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".up.sql") {
				names = append(names, entry.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			body, err := files.ReadFile(name)
			if err != nil {
				return err
			}
			checksum := fmt.Sprintf("%x", sha256.Sum256(body))
			var existing string
			var applied bool
			if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version=$1)`, name).Scan(&applied); err != nil {
				return err
			}
			if applied {
				if err := conn.QueryRow(ctx, `SELECT checksum FROM schema_migrations WHERE version=$1`, name).Scan(&existing); err != nil {
					return err
				}
				if existing != checksum {
					return fmt.Errorf("migration checksum changed: %s", name)
				}
				continue
			}
			tx, err := conn.Begin(ctx)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, string(bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf}))); err == nil {
				_, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version,checksum) VALUES($1,$2)`, name, checksum)
			}
			if err != nil {
				_ = tx.Rollback(context.Background())
				return fmt.Errorf("apply %s: %w", name, err)
			}
			if err := tx.Commit(ctx); err != nil {
				return fmt.Errorf("commit %s: %w", name, err)
			}
		}
		return nil
	})
}

// Down rolls back the latest applied migration. It is intended for controlled
// development and migration tests; normal application startup calls only Up.
func Down(ctx context.Context, pool *pgxpool.Pool) error {
	return locked(ctx, pool, func(conn *pgxpool.Conn) error {
		var name string
		if err := conn.QueryRow(ctx, `SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&name); err != nil {
			return err
		}
		body, err := files.ReadFile(strings.TrimSuffix(name, ".up.sql") + ".down.sql")
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, string(bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf}))); err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version=$1`, name)
		}
		if err != nil {
			_ = tx.Rollback(context.Background())
			return err
		}
		return tx.Commit(ctx)
	})
}

func locked(ctx context.Context, pool *pgxpool.Pool, action func(*pgxpool.Conn) error) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, advisoryLock); err != nil {
		return err
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryLock) }()
	return action(conn)
}
