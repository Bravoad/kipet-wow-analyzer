package migrations

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrSchemaNotReady = errors.New("database migration ledger is not compatible with this binary")

// Check only reads the migration ledger. It never creates a ledger, applies a
// missing migration or accepts a newer/modified ledger on an older binary.
// Matching ledger entries are not a replacement for a full schema audit.
func Check(ctx context.Context, pool *pgxpool.Pool) error {
	expected := map[string]string{}
	entries, err := files.ReadDir(".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		body, e := files.ReadFile(entry.Name())
		if e != nil {
			return e
		}
		expected[entry.Name()] = fmt.Sprintf("%x", sha256.Sum256(body))
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("%w: readiness transaction unavailable", ErrSchemaNotReady)
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT version,checksum FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("%w: ledger unavailable", ErrSchemaNotReady)
	}
	seen := 0
	for rows.Next() {
		var version, checksum string
		if err = rows.Scan(&version, &checksum); err != nil {
			rows.Close()
			return fmt.Errorf("%w: malformed ledger", ErrSchemaNotReady)
		}
		want, known := expected[version]
		if !known || want != checksum {
			rows.Close()
			return fmt.Errorf("%w: unknown version or checksum mismatch", ErrSchemaNotReady)
		}
		seen++
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return fmt.Errorf("%w: ledger read failed", ErrSchemaNotReady)
	}
	if seen != len(expected) {
		return fmt.Errorf("%w: missing migration", ErrSchemaNotReady)
	}
	return tx.Commit(ctx)
}
