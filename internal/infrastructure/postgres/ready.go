package postgres

import (
	"context"

	"kipet-wow-analyzer/migrations"
)

// CheckReady verifies that this binary's complete migration ledger is present.
// It does not run migrations or rewrite provider-mode markers.
func (s *Store) CheckReady(ctx context.Context) error { return migrations.Check(ctx, s.pool) }
