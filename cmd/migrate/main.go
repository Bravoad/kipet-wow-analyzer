package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"kipet-wow-analyzer/migrations"
)

func main() {
	down := flag.Bool("down", false, "Remove the application schema; destructive, intended for isolated development databases")
	flag.Parse()
	if err := run(*down); err != nil {
		slog.Error("migration failed", "error", err.Error())
		os.Exit(1)
	}
}
func run(down bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	primary := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if primary == "" {
		primary = "postgres://kipet:kipet@127.0.0.1:5432/kipet?sslmode=disable"
	}
	if err := migrateDatabase(ctx, primary, "primary", down); err != nil {
		return err
	}
	secondary := strings.TrimSpace(os.Getenv("COMBAT_DATABASE_URL"))
	if !down && secondary != "" && secondary != primary {
		return migrateDatabase(ctx, secondary, "combat", false)
	}
	return nil
}

func migrateDatabase(ctx context.Context, raw, name string, down bool) error {
	pool, err := pgxpool.New(ctx, raw)
	if err != nil {
		return fmt.Errorf("invalid %s database configuration", name)
	}
	defer pool.Close()
	if err = pool.Ping(ctx); err != nil {
		return fmt.Errorf("cannot connect to %s database", name)
	}
	if down {
		err = migrations.Down(ctx, pool)
	} else {
		err = migrations.Up(ctx, pool)
	}
	if err != nil {
		return fmt.Errorf("%s database: %w", name, err)
	}
	return nil
}
