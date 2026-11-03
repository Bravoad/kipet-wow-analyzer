package application

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/combat"
)

type CombatWorker struct {
	Repository  CombatRepository
	Provider    CombatProvider
	Mode        string
	Logger      *slog.Logger
	Concurrency int
	Budget      ProviderBudget
}

func (w *CombatWorker) Run(ctx context.Context) error {
	if w.Mode == "disabled" {
		<-ctx.Done()
		return nil
	}
	if w.Repository == nil || w.Provider == nil || (w.Mode != "fixture" && w.Mode != "warcraftlogs") {
		return fmt.Errorf("combat worker dependencies are unavailable")
	}
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	if w.Concurrency < 1 {
		w.Concurrency = 2
	}
	if w.Concurrency > 4 {
		w.Concurrency = 4
	}
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			if err := w.Repository.MaintainCombat(ctx); err != nil && ctx.Err() == nil {
				w.Logger.Error("combat maintenance failed", "code", character.AsFault(err).Code)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	for i := 0; i < w.Concurrency; i++ {
		group.Add(1)
		go func(index int) { defer group.Done(); w.loop(ctx, index) }(i)
	}
	<-ctx.Done()
	group.Wait()
	return nil
}
func (w *CombatWorker) loop(ctx context.Context, index int) {
	for ctx.Err() == nil {
		lease, err := w.Repository.ClaimCombat(ctx, fmt.Sprintf("combat-worker-%d", index))
		if err != nil && ctx.Err() == nil {
			w.Logger.Error("combat claim failed", "code", character.AsFault(err).Code)
		}
		if err == nil && lease != nil {
			w.execute(ctx, *lease)
			continue
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func (w *CombatWorker) execute(parent context.Context, lease CombatLease) {
	ctx, cancel := context.WithTimeout(parent, 120*time.Second)
	defer cancel()
	heartbeatDone := make(chan struct{})
	heartbeatStopped := make(chan struct{})
	go func() {
		defer close(heartbeatStopped)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatDone:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := w.Repository.RenewCombat(ctx, lease); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { close(heartbeatDone); <-heartbeatStopped }()
	if w.Budget != nil {
		if err := w.Budget.Wait(ctx, "global"); err != nil {
			w.fail(parent, lease, err)
			return
		}
	}
	if err := w.Repository.StartCombatAttempt(ctx, lease); err != nil {
		return
	}
	report, err := w.Provider.Report(ctx, lease.Code)
	if err != nil {
		w.providerFailure(parent, lease, err)
		return
	}
	if report.Code != lease.Code || report.Visibility != "public" {
		w.providerFailure(parent, lease, character.NewFault("PROVIDER_INVALID_RESPONSE", false))
		return
	}
	resource, err := w.Repository.SaveCombatReport(ctx, lease, report, lease.FightID == 0)
	if err != nil {
		w.fail(parent, lease, err)
		return
	}
	if lease.FightID == 0 {
		return
	}
	if !CombatParticipant(report, lease.FightID, lease.ActorID) {
		w.fail(parent, lease, character.NewFault("INVALID_ARGUMENT", false))
		return
	}
	collection, err := w.Provider.Collect(ctx, report, lease.FightID, lease.ActorID)
	if err != nil {
		w.providerFailure(parent, lease, err)
		return
	}
	if collection.Report.Code != report.Code || collection.FightID != lease.FightID || collection.ActorID != lease.ActorID {
		w.fail(parent, lease, character.NewFault("PROVIDER_INVALID_RESPONSE", false))
		return
	}
	analysis, err := combat.Calculate(collection, w.Mode)
	if err != nil {
		w.fail(parent, lease, err)
		return
	}
	if err = w.Repository.SaveCombatAnalysis(ctx, lease, resource.ID, analysis); err != nil {
		w.fail(parent, lease, err)
	}
}
func (w *CombatWorker) providerFailure(ctx context.Context, lease CombatLease, err error) {
	fault := character.AsFault(err)
	if fault.Code == "REPORT_UNAVAILABLE" {
		if purgeErr := w.Repository.PurgeCombat(ctx, lease); purgeErr != nil && ctx.Err() == nil {
			w.Logger.Error("combat purge failed", "code", character.AsFault(purgeErr).Code)
		}
		return
	}
	if fault.Code == "PROVIDER_ACCESS_DENIED" {
		if quarantineErr := w.Repository.QuarantineCombat(ctx, lease); quarantineErr != nil {
			if ctx.Err() == nil {
				w.Logger.Error("combat quarantine failed", "code", character.AsFault(quarantineErr).Code)
			}
			return
		}
	}
	w.fail(ctx, lease, err)
}
func (w *CombatWorker) fail(ctx context.Context, lease CombatLease, err error) {
	if ctx.Err() != nil {
		return
	}
	fault := character.AsFault(err)
	delay := time.Duration(float64(2*time.Second*time.Duration(1<<min(8, max(0, lease.Job.Attempts)))) * (0.5 + rand.Float64()))
	retry := time.Now().Add(min(delay, 5*time.Minute))
	if fault.RetryAt.After(retry) {
		retry = fault.RetryAt
	}
	if err = w.Repository.FailCombat(ctx, lease, fault, retry); err != nil && ctx.Err() == nil {
		w.Logger.Error("combat task transition failed", "code", character.AsFault(err).Code)
	}
}
