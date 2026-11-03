package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var (
	ErrProviderModeMismatch = errors.New("database is bound to a different provider mode")
	ErrProviderModeUnmarked = errors.New("populated database has no provider mode marker; explicit provenance decision required")
)

// CheckProviderMode is called by bootstrap before reading or writing game data.
// It refuses silent adoption of legacy data and never clears existing records.
func (s *Store) CheckProviderMode(ctx context.Context, mode string) error {
	if mode != "fixture" && mode != "blizzard" {
		return fmt.Errorf("invalid provider mode")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(748215930004)`); err != nil {
		return err
	}
	var existing string
	err = tx.QueryRow(ctx, `SELECT value FROM app_settings WHERE key='provider_mode' FOR UPDATE`).Scan(&existing)
	if err == nil {
		if existing != mode {
			return fmt.Errorf("%w: configured=%s stored=%s", ErrProviderModeMismatch, mode, existing)
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var populated bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM characters) OR EXISTS(SELECT 1 FROM realms)`).Scan(&populated); err != nil {
		return err
	}
	if populated {
		return ErrProviderModeUnmarked
	}
	if _, err = tx.Exec(ctx, `INSERT INTO app_settings(key,value) VALUES('provider_mode',$1)`, mode); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
