package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"kipet-wow-analyzer/internal/application"
	"kipet-wow-analyzer/internal/infrastructure/blizzard"
	"kipet-wow-analyzer/internal/infrastructure/combatfixture"
	"kipet-wow-analyzer/internal/infrastructure/fixture"
	"kipet-wow-analyzer/internal/infrastructure/providerbudget"
	"kipet-wow-analyzer/internal/infrastructure/raiderio"
	"kipet-wow-analyzer/internal/infrastructure/warcraftlogs"
	"kipet-wow-analyzer/internal/platform/bootstrap"
	"kipet-wow-analyzer/internal/platform/config"
	"kipet-wow-analyzer/internal/platform/observability"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker stopped", "error", err.Error())
		os.Exit(1)
	}
}
func run() error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	c, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	deps, err := bootstrap.Open(startup, c)
	cancel()
	if err != nil {
		return errors.New("database initialization failed; verify configuration and migrations")
	}
	defer deps.Close()
	budget := &providerbudget.Budget{Pool: deps.Pool}
	var provider application.Provider
	if c.ProviderMode == "fixture" {
		provider = fixture.New()
	} else {
		provider, err = blizzard.New(blizzard.Config{ClientID: c.ClientID, ClientSecret: c.ClientSecret, Permit: func(ctx context.Context) error { return budget.Wait(ctx, "global") }, PauseUntil: func(ctx context.Context, until time.Time) error { return budget.Defer(ctx, "global", until) }})
		if err != nil {
			return err
		}
	}
	worker := application.Worker{Store: deps.Store, Provider: provider, Logger: logger, Concurrency: c.Workers}
	// Fixture mode has no external requests and uses the same durable worker path.
	if c.ProviderMode == "blizzard" {
		worker.Budget = budget
	}
	logger.Info("worker started", "provider_mode", c.ProviderMode, "concurrency", c.Workers)
	var combatProvider application.CombatProvider
	var combatBudget *providerbudget.Budget
	if c.CombatMode == "fixture" {
		combatProvider = combatfixture.New()
	} else if c.CombatMode == "warcraftlogs" {
		combatBudget = &providerbudget.Budget{Pool: deps.CombatPool, Provider: "warcraftlogs"}
		combatProvider, err = warcraftlogs.New(warcraftlogs.Config{ClientID: c.WCLClientID, ClientSecret: c.WCLClientSecret,
			Permit:       func(ctx context.Context) error { return combatBudget.Wait(ctx, "global") },
			PauseUntil:   func(ctx context.Context, until time.Time) error { return combatBudget.Defer(ctx, "global", until) },
			ObserveUsage: combatBudget.ObserveUsage})
		if err != nil {
			return err
		}
	}
	guildProvider, ok := provider.(application.GuildProvider)
	if !ok {
		return errors.New("guild provider is unavailable")
	}
	guildWorker := &application.GuildWorker{Repository: deps.Store, Characters: deps.Store, Provider: guildProvider, Mode: c.ProviderMode, Logger: logger}
	if c.ProviderMode == "blizzard" {
		guildWorker.Budget = budget
	}
	runners := []func(context.Context) error{worker.Run, guildWorker.Run}
	runnerNames := []string{"characters", "guilds"}
	if c.RaiderEnabled {
		raiderBudget := &providerbudget.Budget{Pool: deps.Pool, Provider: "raiderio"}
		client, err := raiderio.New(raiderio.Config{
			PauseUntil: func(ctx context.Context, until time.Time) error {
				return raiderBudget.Defer(ctx, "global", until)
			},
		})
		if err != nil {
			return err
		}
		raiderWorker := &application.RaiderWorker{Repository: deps.Store, Provider: client, Budget: raiderBudget}
		runners = append(runners, raiderWorker.Run)
		runnerNames = append(runnerNames, "raiderio")
	}
	if combatProvider != nil {
		combatWorker := &application.CombatWorker{Repository: deps.CombatStore, Provider: combatProvider, Mode: c.CombatMode, Logger: logger, Concurrency: 2}
		if combatBudget != nil {
			combatWorker.Budget = combatBudget
		}
		runners = append(runners, combatWorker.Run)
		runnerNames = append(runnerNames, "combat")
	}
	workers, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()
	health := observability.NewWorkerHealth(workers, deps, deps.Cache, runnerNames)
	internal := &http.Server{Addr: c.WorkerInternalAddr, Handler: health.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 << 10}
	listener, err := net.Listen("tcp", c.WorkerInternalAddr)
	if err != nil {
		return errors.New("worker internal listener unavailable")
	}
	serverStopped := make(chan error, 1)
	go func() { serverStopped <- internal.Serve(listener) }()
	logger.Info("worker internal listening", "address", c.WorkerInternalAddr)
	stopped := make(chan error, len(runners))
	for i, runWorker := range runners {
		go func(run func(context.Context) error) { stopped <- run(workers) }(health.Track(runnerNames[i], runWorker))
	}
	finished := 0
	select {
	case <-ctx.Done():
	case err = <-stopped:
		finished = 1
	case err = <-serverStopped:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	cancelWorkers()
	shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	_ = internal.Shutdown(shutdown)
	for i := finished; i < len(runners); i++ {
		other := <-stopped
		if err == nil {
			err = other
		}
	}
	return err
}
