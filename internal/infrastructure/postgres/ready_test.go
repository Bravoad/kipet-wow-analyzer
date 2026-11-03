package postgres

import (
	"errors"
	"testing"

	"kipet-wow-analyzer/migrations"
)

func TestReadinessFailsAfterRollbackDespiteLiveDatabase(t *testing.T) {
	ctx, store, pool := testStore(t)
	if err := store.CheckReady(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Down(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := store.Ping(ctx); err != nil {
		t.Fatal("live database lost connectivity", err)
	}
	if err := store.CheckReady(ctx); !errors.Is(err, migrations.ErrSchemaNotReady) {
		t.Fatal("rollback still ready", err)
	}
}
