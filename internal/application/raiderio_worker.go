package application

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/raiderio"
)

type RaiderProvider interface {
	Fetch(context.Context, raiderio.Key) (raiderio.Snapshot, error)
}

type RaiderWorker struct {
	Repository RaiderRepository
	Provider   RaiderProvider
	Budget     ProviderBudget
}

func (w *RaiderWorker) Run(ctx context.Context) error {
	if w.Repository == nil || w.Provider == nil || w.Budget == nil {
		return errors.New("Raider.IO worker dependencies unavailable")
	}
	nextCleanup := time.Time{}
	for ctx.Err() == nil {
		if time.Now().After(nextCleanup) {
			err := w.Repository.MaintainRaider(ctx)
			w.logFailure(ctx, "Raider.IO cleanup failed", err)
			nextCleanup = time.Now().Add(time.Minute)
		}
		lease, err := w.Repository.ClaimRaider(ctx)
		w.logFailure(ctx, "Raider.IO job claim failed", err)
		if err == nil && lease != nil {
			w.execute(ctx, *lease)
			continue
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return nil
}

func (w *RaiderWorker) execute(parent context.Context, lease RaiderLease) {
	// One bounded provider request fits within the 60-second fenced lease.
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	err := w.Budget.Wait(ctx, "global")
	if err == nil {
		err = w.Repository.StartRaiderAttempt(ctx, lease)
		if err == nil {
			var snapshot raiderio.Snapshot
			snapshot, err = w.Provider.Fetch(ctx, lease.Key)
			if err == nil {
				err = w.Repository.SaveRaider(ctx, lease, snapshot)
			}
		}
	}
	if err == nil || errors.Is(err, character.ErrLeaseLost) || parent.Err() != nil {
		return
	}
	fault := character.AsFault(err)
	if fault.Code == "CHARACTER_UNAVAILABLE" || fault.Code == "GUILD_UNAVAILABLE" {
		w.logFailure(parent, "Raider.IO purge failed", w.Repository.PurgeRaider(parent, lease))
		return
	}
	delay := time.Duration(float64(time.Second*time.Duration(1<<min(lease.Job.Attempts+1, 8))) * (0.5 + rand.Float64()))
	w.logFailure(parent, "Raider.IO job transition failed", w.Repository.FailRaider(parent, lease, fault, time.Now().Add(delay)))
}

func (w *RaiderWorker) logFailure(ctx context.Context, message string, err error) {
	if err != nil && ctx.Err() == nil && !errors.Is(err, character.ErrLeaseLost) {
		slog.Error(message, "code", character.AsFault(err).Code)
	}
}
