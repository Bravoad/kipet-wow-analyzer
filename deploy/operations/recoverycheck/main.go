// recoverycheck verifies a restored synthetic database while it is offline.
// It deliberately refuses arbitrary database names and requires a per-run guard.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"kipet-wow-analyzer/internal/application"
	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/infrastructure/postgres"
	"kipet-wow-analyzer/migrations"
)

var drillName = regexp.MustCompile(`^kipet_recovery_drill_[a-f0-9]{16}$`)
var drillToken = regexp.MustCompile(`^[a-f0-9]{32}$`)

func validateTarget(raw, token string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "postgres" || u.Hostname() != "127.0.0.1" || u.RawQuery != "sslmode=disable" || u.Fragment != "" || !drillName.MatchString(strings.TrimPrefix(u.Path, "/")) || !drillToken.MatchString(token) {
		return "", errors.New("recovery verification requires a loopback drill database and a random guard token")
	}
	return strings.TrimPrefix(u.Path, "/"), nil
}

func main() {
	result, err := run(context.Background(), os.Getenv("RECOVERY_DATABASE_URL"), os.Getenv("RECOVERY_DRILL_TOKEN"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Recovery verification failed:", err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(result)
}

func run(parent context.Context, raw, token string) (map[string]any, error) {
	expected, err := validateTarget(raw, token)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, raw)
	if err != nil {
		return nil, errors.New("connect restored database")
	}
	defer pool.Close()
	var actual, guard, source, combatSource string
	if err = pool.QueryRow(ctx, `SELECT current_database(),(SELECT token FROM recovery_drill_guard),(SELECT value FROM app_settings WHERE key='provider_mode'),(SELECT value FROM app_settings WHERE key='combat_provider_mode')`).Scan(&actual, &guard, &source, &combatSource); err != nil || actual != expected || guard != token || source != "fixture" || combatSource != "fixture" {
		return nil, errors.New("restored database guard or synthetic source marker does not match")
	}
	// This also rejects changed migration checksums in the archive.
	if err = migrations.Check(ctx, pool); err != nil {
		return nil, fmt.Errorf("migration integrity: %w", err)
	}
	store := postgres.New(pool, postgres.Options{HMACSecret: []byte("recovery-drill-isolated-synthetic-key")})
	var before, snapshots, guilds, reports int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM characters),(SELECT count(*) FROM character_snapshots),(SELECT count(*) FROM guild_snapshots),(SELECT count(*) FROM combat_reports)`).Scan(&before, &snapshots, &guilds, &reports); err != nil {
		return nil, err
	}
	if before == 0 || snapshots == 0 {
		return nil, errors.New("source has no synthetic character snapshot; load a demo before the drill")
	}
	var region, realm, name, id string
	var validUntil time.Time
	if err = pool.QueryRow(ctx, `SELECT c.region,r.slug,c.name,c.id::text,c.data_valid_until FROM characters c JOIN realms r ON r.id=c.realm_id JOIN character_snapshots s ON s.id=c.current_snapshot_id WHERE c.publication_blocked_at IS NULL AND c.data_valid_until>clock_timestamp() AND s.valid_until>clock_timestamp() AND r.fetched_at>clock_timestamp()-interval '720 hours' ORDER BY c.id LIMIT 1`).Scan(&region, &realm, &name, &id, &validUntil); err != nil {
		return nil, errors.New("no publishable demo snapshot restored")
	}
	key, err := character.NormalizeKey(region, realm, name)
	if err != nil {
		return nil, err
	}
	project := func() (*application.CharacterData, error) {
		records, e := store.FindMany(ctx, []character.Key{key})
		if e != nil || len(records) != 1 {
			return nil, errors.New("restored profile batch could not be read")
		}
		return application.ProjectCharacter(records[0], key, "en-US", "fixture", time.Now().UTC()).Data, nil
	}
	data, err := project()
	if err != nil || data == nil {
		return nil, errors.New("restored allowed profile is not publishable")
	}
	if _, err = pool.Exec(ctx, `UPDATE characters SET publication_blocked_at=clock_timestamp(),publication_block_reason='provider_access_denied' WHERE id=$1`, id); err != nil {
		return nil, err
	}
	data, err = project()
	if err != nil || data != nil {
		return nil, errors.New("restored quarantine guard failed")
	}
	if _, err = pool.Exec(ctx, `UPDATE characters SET publication_blocked_at=NULL,publication_block_reason=NULL,data_valid_until=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
		return nil, err
	}
	data, err = project()
	if err != nil || data != nil {
		return nil, errors.New("restored expiry guard failed")
	}
	if err = store.Maintain(ctx); err != nil {
		return nil, fmt.Errorf("restored cleanup: %w", err)
	}
	if err = store.MaintainCombat(ctx); err != nil {
		return nil, fmt.Errorf("restored combat cleanup: %w", err)
	}
	if err = store.MaintainGuild(ctx); err != nil {
		return nil, fmt.Errorf("restored guild cleanup: %w", err)
	}
	var remains bool
	if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM characters WHERE id=$1)`, id).Scan(&remains); err != nil || remains {
		return nil, errors.New("expired restored identity was not removed")
	}
	return map[string]any{"characters_restored": before, "snapshots_restored": snapshots, "guild_snapshots_restored": guilds, "combat_reports_restored": reports, "migration_checksums": "verified", "allowed_profile": true, "quarantine_hidden": true, "expiry_hidden": true, "expired_identity_removed": true}, nil
}
