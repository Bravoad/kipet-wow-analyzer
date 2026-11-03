package application

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
)

type Worker struct {
	Store       Store
	Provider    Provider
	Budget      ProviderBudget
	Logger      *slog.Logger
	Concurrency int
}

func (w *Worker) Run(ctx context.Context) error {
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	if w.Concurrency < 1 {
		w.Concurrency = 4
	}
	var group sync.WaitGroup
	group.Add(1)
	go func() { defer group.Done(); w.catalogLoop(ctx) }()
	group.Add(1)
	go func() {
		defer group.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			if err := w.Store.Maintain(ctx); err != nil && ctx.Err() == nil {
				w.Logger.Error("maintenance failed", "code", character.AsFault(err).Code)
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

func (w *Worker) catalogLoop(ctx context.Context) {
	delay := time.Duration(0)
	for {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		allOK := true
		for _, region := range []string{"eu", "us"} {
			request, cancel := context.WithTimeout(ctx, 2*time.Minute)
			realms, err := w.Provider.Realms(request, region)
			if err == nil {
				err = w.Store.UpsertRealms(request, realms)
			}
			cancel()
			if err != nil {
				allOK = false
				if ctx.Err() == nil {
					w.Logger.Error("realm catalog refresh failed", "region", region, "code", character.AsFault(err).Code)
				}
			}
		}
		if ctx.Err() != nil {
			return
		}
		delay = 24 * time.Hour
		if !allOK {
			delay = 5 * time.Minute
		}
	}
}

func (w *Worker) loop(ctx context.Context, index int) {
	for ctx.Err() == nil {
		lease, err := w.Store.Claim(ctx, "worker-"+string(rune('a'+index)))
		if err != nil {
			if ctx.Err() == nil {
				w.Logger.Error("claim failed", "code", character.AsFault(err).Code)
			}
		}
		if lease != nil && err == nil {
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

func (w *Worker) execute(parent context.Context, lease character.Lease) {
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
				if _, err := w.Store.Renew(ctx, lease); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { close(heartbeatDone); <-heartbeatStopped }()
	// A permit before counting the attempt prevents quota waiting from consuming retries.
	if w.Budget != nil {
		if err := w.Budget.Wait(ctx, lease.Job.Key.Region); err != nil {
			w.fail(parent, lease, err)
			return
		}
	}
	if err := w.Store.StartAttempt(ctx, lease); err != nil {
		return
	}
	realm, err := w.Store.ResolveRealm(ctx, lease.Job.Key.Region, lease.Job.Key.Realm)
	if err != nil {
		w.fail(parent, lease, err)
		return
	}
	snapshot, err := w.Provider.Fetch(ctx, lease.Job.Key, realm)
	if err != nil {
		fault := character.AsFault(err)
		switch fault.Code {
		case "CHARACTER_UNAVAILABLE":
			err = w.Store.Purge(ctx, lease)
		case "PROVIDER_ACCESS_DENIED":
			err = w.Store.Quarantine(ctx, lease)
			if err == nil {
				w.fail(parent, lease, fault)
				return
			}
		default:
			w.fail(parent, lease, err)
			return
		}
		if err != nil && !errors.Is(err, character.ErrLeaseLost) && ctx.Err() == nil {
			w.Logger.Error("provider state persistence failed", "code", character.AsFault(err).Code)
		}
		return
	}
	// Temporary optional failures may reuse permissible previous data without changing its age.
	if previous, e := w.Store.Find(ctx, lease.Job.Key); e == nil && previous.Snapshot != nil && !previous.Blocked && previous.ProviderID != nil && *previous.ProviderID == snapshot.ProviderID {
		for _, name := range []string{"equipment", "statistics", "talents"} {
			current := snapshot.Sections[name]
			old := previous.Snapshot.Sections[name]
			if current.State == "unavailable" && current.Reason != nil && (*current.Reason == "provider_unavailable" || *current.Reason == "invalid_response") && old.Publishable(time.Now()) {
				if name == "talents" && !sameSpecialization(previous.Snapshot.Sections["profile"].Payload, snapshot.Sections["profile"].Payload) {
					continue
				}
				old.CheckedAt = current.CheckedAt
				old.State = "partial"
				old.Reason = current.Reason
				snapshot.Sections[name] = old
			}
		}
	}
	if err := character.ValidateSnapshot(snapshot); err != nil {
		w.fail(parent, lease, character.NewFault("PROVIDER_INVALID_RESPONSE", false))
		return
	}
	if err := w.Store.Publish(ctx, lease, snapshot); err != nil && !errors.Is(err, character.ErrLeaseLost) && ctx.Err() == nil {
		w.Logger.Error("snapshot publish failed", "code", character.AsFault(err).Code)
		w.fail(parent, lease, err)
	}
}

func (w *Worker) fail(ctx context.Context, lease character.Lease, err error) {
	if ctx.Err() != nil || errors.Is(err, character.ErrLeaseLost) {
		return
	}
	fault := character.AsFault(err)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		fault = character.NewFault("PROVIDER_UNAVAILABLE", true)
	}
	attempt := min(lease.Job.Attempts, 8)
	delay := min(2*time.Second*time.Duration(1<<attempt), 5*time.Minute)
	next := time.Now().UTC().Add(time.Duration(rand.Int64N(int64(delay) + 1)))
	if fault.RetryAt.After(next) {
		next = fault.RetryAt
	}
	if e := w.Store.Fail(ctx, lease, fault, next); e != nil && !errors.Is(e, character.ErrLeaseLost) {
		w.Logger.Error("job failure persistence failed", "code", character.AsFault(e).Code)
	}
}

func specialization(payload json.RawMessage) string {
	var p struct {
		Specialization *struct {
			ID string `json:"id"`
		} `json:"active_specialization"`
	}
	if json.Unmarshal(payload, &p) != nil || p.Specialization == nil {
		return ""
	}
	return p.Specialization.ID
}

func sameSpecialization(a, b json.RawMessage) bool {
	old := specialization(a)
	return old != "" && old == specialization(b)
}
