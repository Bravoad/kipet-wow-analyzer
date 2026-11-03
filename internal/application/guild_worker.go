package application

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/guild"
)

type GuildWorker struct {
	Repository  GuildRepository
	Characters  Store
	Provider    GuildProvider
	Mode        string
	Logger      *slog.Logger
	Budget      ProviderBudget
	Concurrency int
}

func (w *GuildWorker) Run(ctx context.Context) error {
	if w.Repository == nil || w.Characters == nil || w.Provider == nil || (w.Mode != "fixture" && w.Mode != "blizzard") {
		return fmt.Errorf("guild worker dependencies unavailable")
	}
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	if w.Concurrency < 1 {
		w.Concurrency = 2
	}
	w.Concurrency = min(w.Concurrency, 4)
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			if err := w.Repository.MaintainGuild(ctx); err != nil && ctx.Err() == nil {
				w.Logger.Error("guild maintenance failed", "code", character.AsFault(err).Code)
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
		go func(index int) {
			defer group.Done()
			for ctx.Err() == nil {
				l, err := w.Repository.ClaimGuild(ctx, fmt.Sprintf("guild-worker-%d", index))
				if err != nil && ctx.Err() == nil {
					w.Logger.Error("guild claim failed", "code", character.AsFault(err).Code)
				}
				if err == nil && l != nil {
					w.execute(ctx, *l)
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
		}(i)
	}
	<-ctx.Done()
	group.Wait()
	return nil
}
func (w *GuildWorker) execute(parent context.Context, lease GuildLease) {
	ctx, cancel := context.WithTimeout(parent, 120*time.Second)
	defer cancel()
	heartbeatDone := make(chan struct{})
	heartbeatStopped := make(chan struct{})
	go func() {
		defer close(heartbeatStopped)
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-heartbeatDone:
				return
			case <-ctx.Done():
				return
			case <-tick.C:
				if w.Repository.RenewGuild(ctx, lease) != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { close(heartbeatDone); <-heartbeatStopped }()
	var resource *GuildRecord
	if lease.SnapshotID != nil {
		r, err := w.Repository.LookupGuild(ctx, lease.Key)
		if err != nil {
			w.failure(parent, lease, err)
			return
		}
		if !r.Blocked && r.ID == *lease.SnapshotID && time.Now().Before(r.FreshUntil) && time.Now().Before(r.ValidUntil) {
			resource = r
		}
	}
	if resource == nil {
		if lease.Job.Attempts >= lease.Job.MaxAttempts {
			w.failure(parent, lease, character.NewFault("PROVIDER_UNAVAILABLE", false))
			return
		}
		if w.Budget != nil {
			if err := w.Budget.Wait(ctx, "global"); err != nil {
				w.failure(parent, lease, err)
				return
			}
		}
		if err := w.Repository.StartGuildAttempt(ctx, lease); err != nil {
			return
		}
		realm, err := w.Characters.ResolveRealm(ctx, lease.Key.Region, lease.Key.Realm)
		if err != nil {
			w.failure(parent, lease, mapRealmError(err))
			return
		}
		bundle, err := w.Provider.FetchGuild(ctx, lease.Key, realm)
		if err != nil {
			w.failure(parent, lease, err)
			return
		}
		resource, err = w.Repository.SaveGuild(ctx, lease, bundle)
		if err != nil {
			w.failure(parent, lease, err)
			return
		}
		lease.SnapshotID = &resource.ID
	}
	var bundle guild.Bundle
	if json.Unmarshal(resource.Payload, &bundle) != nil || len(bundle.Roster.Members) > 1000 {
		w.failure(parent, lease, character.NewFault("PROVIDER_INVALID_RESPONSE", false))
		return
	}
	for ctx.Err() == nil {
		before := lease.Cursor
		next, done, err := w.Repository.ScheduleGuildChunk(ctx, lease)
		if err != nil {
			w.failure(parent, lease, err)
			return
		}
		if done {
			return
		}
		lease.Cursor = next
		if next == before {
			w.failure(parent, lease, character.NewFault("QUEUE_FULL", true))
			return
		}
	}
	if parent.Err() == nil {
		w.failure(parent, lease, character.NewFault("INFRASTRUCTURE_UNAVAILABLE", true))
	}
}
func (w *GuildWorker) failure(ctx context.Context, l GuildLease, err error) {
	if ctx.Err() != nil {
		return
	}
	f := character.AsFault(err)
	if f.Code == "GUILD_UNAVAILABLE" {
		if e := w.Repository.PurgeGuild(ctx, l); e != nil && ctx.Err() == nil {
			w.Logger.Error("guild purge failed", "code", character.AsFault(e).Code)
		}
		return
	}
	if f.Code == "PROVIDER_ACCESS_DENIED" {
		if e := w.Repository.QuarantineGuild(ctx, l); e != nil {
			return
		}
	}
	delay := time.Duration(float64(2*time.Second*time.Duration(1<<min(8, max(0, l.Job.Attempts)))) * (0.5 + rand.Float64()))
	retry := time.Now().Add(min(delay, 5*time.Minute))
	if f.Code == "QUEUE_FULL" {
		retry = time.Now().Add(15 * time.Second)
	}
	if f.RetryAt.After(retry) {
		retry = f.RetryAt
	}
	if e := w.Repository.FailGuild(ctx, l, f, retry); e != nil && ctx.Err() == nil {
		w.Logger.Error("guild transition failed", "code", character.AsFault(e).Code)
	}
}
