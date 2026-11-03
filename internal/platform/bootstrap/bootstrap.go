package bootstrap

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"kipet-wow-analyzer/internal/infrastructure/postgres"
	rediscache "kipet-wow-analyzer/internal/infrastructure/redis"
	"kipet-wow-analyzer/internal/platform/config"
	"kipet-wow-analyzer/migrations"
)

type Dependencies struct {
	*postgres.Store
	Pool        *pgxpool.Pool
	Cache       *rediscache.Cache
	CombatPool  *pgxpool.Pool
	CombatStore *postgres.Store
}

func Open(ctx context.Context, c config.Config) (*Dependencies, error) {
	pool, err := openDatabase(ctx, c.DatabaseURL)
	if err != nil {
		return nil, err
	}
	cache, err := rediscache.New(c.RedisURL)
	if err != nil {
		pool.Close()
		return nil, err
	}
	storeOptions := postgres.Options{HMACSecret: []byte(c.HMACSecret)}
	d := &Dependencies{Pool: pool, Store: postgres.New(pool, storeOptions), Cache: cache}
	if err = d.Store.CheckProviderMode(ctx, c.ProviderMode); err != nil {
		d.Close()
		return nil, err
	}
	if c.CombatMode == "disabled" {
		return d, nil
	}
	d.CombatPool, d.CombatStore = d.Pool, d.Store
	if c.CombatDatabaseURL != "" && c.CombatDatabaseURL != c.DatabaseURL {
		combatPool, err := openDatabase(ctx, c.CombatDatabaseURL)
		if err != nil {
			d.Close()
			return nil, err
		}
		d.CombatPool, d.CombatStore = combatPool, postgres.New(combatPool, storeOptions)
	}
	if err = d.CombatStore.CheckCombatMode(ctx, c.CombatMode); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func openDatabase(ctx context.Context, rawURL string) (*pgxpool.Pool, error) {
	opts, err := pgxpool.ParseConfig(rawURL)
	if err != nil {
		return nil, err
	}
	opts.MaxConns = 20
	opts.ConnConfig.RuntimeParams["timezone"] = "UTC"
	opts.ConnConfig.ConnectTimeout = 5 * time.Second
	opts.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		conn.TypeMap().RegisterType(&pgtype.Type{Name: "timestamptz", OID: pgtype.TimestamptzOID, Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC}})
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, opts)
	if err != nil {
		return nil, err
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err = migrations.Check(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func (d *Dependencies) CheckReady(ctx context.Context) error {
	if err := d.Store.CheckReady(ctx); err != nil {
		return err
	}
	if d.CombatStore != nil && d.CombatStore != d.Store {
		return d.CombatStore.CheckReady(ctx)
	}
	return nil
}

func (d *Dependencies) Metrics(ctx context.Context) (map[string]float64, error) {
	metrics, err := d.Store.Metrics(ctx)
	if err != nil || d.CombatStore == nil || d.CombatStore == d.Store {
		return metrics, err
	}
	combatMetrics, err := d.CombatStore.Metrics(ctx)
	if err != nil {
		return nil, err
	}
	isCombatMetric := func(name string) bool {
		return strings.HasPrefix(name, "combat_") || strings.HasPrefix(name, "warcraftlogs_")
	}
	for name := range metrics {
		if isCombatMetric(name) {
			delete(metrics, name)
		}
	}
	for name, value := range combatMetrics {
		if isCombatMetric(name) {
			metrics[name] = value
		}
	}
	return metrics, nil
}

func (d *Dependencies) Close() {
	_ = d.Cache.Close()
	if d.CombatPool != nil && d.CombatPool != d.Pool {
		d.CombatPool.Close()
	}
	d.Pool.Close()
}
