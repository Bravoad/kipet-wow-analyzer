package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"kipet-wow-analyzer/internal/application"
	"kipet-wow-analyzer/internal/platform/bootstrap"
	"kipet-wow-analyzer/internal/platform/config"
	httptransport "kipet-wow-analyzer/internal/transport/http"
	"kipet-wow-analyzer/rules"
)

func main() {
	if err := run(); err != nil {
		slog.Error("API stopped", "error", err.Error())
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
	engine, err := rules.Load(c.ProviderMode)
	if err != nil {
		return errors.New("analysis rule catalog is invalid; startup refused")
	}
	profiles := &application.CharacterService{Store: deps.Store, Cache: deps.Cache}
	h := &httptransport.Handler{Service: profiles, Analysis: &application.AnalysisService{Characters: profiles, Store: deps.Store, Repository: deps.Store, Engine: engine, Mode: c.ProviderMode}, Store: deps, Cache: deps.Cache, Mode: c.ProviderMode, CombatMode: c.CombatMode, CombatSetupRequired: c.CombatSetupRequired, HMACSecret: []byte(c.HMACSecret), Replicas: c.Replicas, Logger: logger, TrustedProxies: c.TrustedProxies}
	if deps.CombatStore != nil {
		h.Combat = &application.CombatService{Repository: deps.CombatStore, Mode: c.CombatMode}
	}
	h.Guild = &application.GuildService{Repository: deps.Store, Characters: deps.Store, Mode: c.ProviderMode, Engine: engine}
	h.Comparison = &application.ComparisonService{Store: deps.Store, Mode: c.ProviderMode}
	if c.RaiderEnabled {
		h.Raider = &application.RaiderService{Repository: deps.Store}
	}
	api := server(c.APIAddr, h.Router())
	internal := server(c.InternalAddr, h.InternalRouter())
	errs := make(chan error, 2)
	go func() {
		logger.Info("API listening", "address", c.APIAddr, "provider_mode", c.ProviderMode)
		errs <- api.ListenAndServe()
	}()
	go func() { errs <- internal.ListenAndServe() }()
	select {
	case <-ctx.Done():
	case err = <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			stop()
		}
	}
	shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	_ = api.Shutdown(shutdown)
	_ = internal.Shutdown(shutdown)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func server(addr string, h http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
}
