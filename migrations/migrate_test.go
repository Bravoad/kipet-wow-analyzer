package migrations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrationLifecycle(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL migration integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bootstrap, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer bootstrap.Close()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "kipet_migration_test_" + hex.EncodeToString(random[:])
	if _, err := bootstrap.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = bootstrap.Exec(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`) }()
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for i := 0; i < 2; i++ {
		if err := Up(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 10 {
		t.Fatalf("migration count = %d, want 10", count)
	}
	if err := Down(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO raiderio_profiles(id,region,realm,name,version,payload,fetched_at,fresh_until,valid_until)
		VALUES('00000000-0000-4000-8000-000000000001','eu','realm','hero','00000000-0000-4000-8000-000000000002',
		       '{"name":"Hero"}',now(),now()+interval '1 minute',now()+interval '1 hour');
		INSERT INTO raiderio_jobs(id,profile_id) VALUES('00000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000001');
		INSERT INTO raiderio_negative_lookups(key_hash,expires_at) VALUES(decode(repeat('ab',32),'hex'),now()+interval '1 minute');
	`); err != nil {
		t.Fatal("seed existing Raider.IO character data", err)
	}
	if err := Up(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO raiderio_profiles(id,kind,region,realm,name)
		VALUES('00000000-0000-4000-8000-000000000004','guild','eu','realm','hero');
		INSERT INTO raiderio_jobs(id,profile_id) VALUES('00000000-0000-4000-8000-000000000005','00000000-0000-4000-8000-000000000004');
		INSERT INTO raiderio_negative_lookups(key_hash,kind,expires_at) VALUES(decode(repeat('cd',32),'hex'),'guild',now()+interval '1 minute');
	`); err != nil {
		t.Fatal("guild identity must coexist with an existing character identity", err)
	}
	if err := Down(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var characterName, negativeHash string
	var jobs int
	if err := pool.QueryRow(ctx, `SELECT (SELECT payload->>'name' FROM raiderio_profiles),
		(SELECT count(*) FROM raiderio_jobs), (SELECT encode(key_hash,'hex') FROM raiderio_negative_lookups)`).
		Scan(&characterName, &jobs, &negativeHash); err != nil || characterName != "Hero" || jobs != 1 || negativeHash != strings.Repeat("ab", 32) {
		t.Fatal("guild rollback must preserve character snapshot, job, and negative lookup", err)
	}
	if err := Down(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var raiderExists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('raiderio_profiles') IS NOT NULL OR to_regclass('raiderio_jobs') IS NOT NULL`).Scan(&raiderExists); err != nil || raiderExists {
		t.Fatal("Raider.IO tables survived rollback", err)
	}
	if err := Down(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var guildExists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('guilds') IS NOT NULL OR to_regclass('guild_jobs') IS NOT NULL`).Scan(&guildExists); err != nil || guildExists {
		t.Fatal("guild tables survived rollback", err)
	}
	if err := Down(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var combatExists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('combat_reports') IS NOT NULL OR to_regclass('combat_jobs') IS NOT NULL`).Scan(&combatExists); err != nil {
		t.Fatal(err)
	}
	if combatExists {
		t.Fatal("combat tables survived rollback")
	}
	if err := Down(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var sectionConstraint string
	if err := pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint
	 WHERE conrelid='snapshot_sections'::regclass AND conname='snapshot_sections_section_check'`).Scan(&sectionConstraint); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sectionConstraint, "mythic_plus") || strings.Contains(sectionConstraint, "raids") {
		t.Fatal("progress values survived section constraint rollback")
	}
	if err := Down(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var reportExists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('analysis_results') IS NOT NULL`).Scan(&reportExists); err != nil {
		t.Fatal(err)
	}
	if reportExists {
		t.Fatal("analysis_results survived down migration")
	}
	if err := Down(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := Down(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := Down(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('negative_lookups') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("negative_lookups survived down migration")
	}
	if err := Down(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT to_regclass('characters') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("characters survived down migration")
	}
	if err := Up(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE schema_migrations SET checksum='modified' WHERE version='0001_initial.up.sql'`); err != nil {
		t.Fatal(err)
	}
	if err := Up(ctx, pool); err == nil {
		t.Fatal("modified checksum was accepted")
	}
}
