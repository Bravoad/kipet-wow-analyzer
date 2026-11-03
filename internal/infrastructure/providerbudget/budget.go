package providerbudget

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Budget serializes every actual provider request across workers and replicas.
// Conservative 2 requests/s (burst 1); never stored in a process-local limiter.
type Budget struct {
	Pool     *pgxpool.Pool
	Provider string // Empty retains Blizzard's existing global budget.
}

func (b *Budget) provider() (string, error) {
	if b.Provider == "" || b.Provider == "blizzard" {
		return "blizzard", nil
	}
	if b.Provider == "warcraftlogs" || b.Provider == "raiderio" {
		return b.Provider, nil
	}
	return "", errors.New("unsupported provider budget")
}

func (b *Budget) Wait(ctx context.Context, region string) error {
	provider, err := b.provider()
	if err != nil {
		return err
	}
	for {
		tx, err := b.Pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "INSERT INTO provider_state(provider,scope) VALUES ($1,'global') ON CONFLICT DO NOTHING", provider)
		var at *time.Time
		var remaining *float64
		var resets *time.Time
		var now time.Time
		if err == nil {
			err = tx.QueryRow(ctx, "SELECT next_allowed_at,clock_timestamp(),budget_remaining::double precision,budget_resets_at FROM provider_state WHERE provider=$1 AND scope='global' FOR UPDATE", provider).Scan(&at, &now, &remaining, &resets)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		wait := time.Duration(0)
		if at != nil {
			wait = at.Sub(now)
		}
		if remaining != nil && *remaining <= 0 && resets != nil && resets.After(now) && resets.Sub(now) > wait {
			wait = resets.Sub(now)
		}
		if wait <= 0 {
			_, err = tx.Exec(ctx, "UPDATE provider_state SET next_allowed_at=clock_timestamp()+interval '500 milliseconds',revision=revision+1,updated_at=clock_timestamp() WHERE provider=$1 AND scope='global'", provider)
			if err == nil {
				err = tx.Commit(ctx)
			} else {
				_ = tx.Rollback(ctx)
			}
			return err
		}
		_ = tx.Rollback(ctx)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (b *Budget) Defer(ctx context.Context, region string, until time.Time) error {
	provider, err := b.provider()
	if err != nil {
		return err
	}
	_, err = b.Pool.Exec(ctx, `INSERT INTO provider_state(provider,scope,next_allowed_at) VALUES($1,'global',$2) ON CONFLICT(provider,scope) DO UPDATE SET next_allowed_at=GREATEST(provider_state.next_allowed_at,EXCLUDED.next_allowed_at),revision=provider_state.revision+1,updated_at=clock_timestamp()`, provider, until)
	return err
}

// ObserveUsage persists the provider's measured points. It does not pretend to
// predict a GraphQL query's cost. Out-of-order observations cannot refill an
// unexpired budget; the provider's reset boundary opens the next window.
func (b *Budget) ObserveUsage(ctx context.Context, remaining float64, resets time.Time) error {
	provider, err := b.provider()
	if err != nil {
		return err
	}
	if math.IsNaN(remaining) || math.IsInf(remaining, 0) || remaining < 0 || resets.IsZero() {
		return errors.New("invalid provider usage")
	}
	_, err = b.Pool.Exec(ctx, `INSERT INTO provider_state(provider,scope,budget_remaining,budget_refilled_at,budget_resets_at)
	VALUES($1,'global',$2,clock_timestamp(),$3)
	ON CONFLICT(provider,scope) DO UPDATE SET
	budget_remaining=CASE WHEN provider_state.budget_resets_at>clock_timestamp() THEN LEAST(provider_state.budget_remaining,EXCLUDED.budget_remaining) ELSE EXCLUDED.budget_remaining END,
	budget_refilled_at=clock_timestamp(),budget_resets_at=EXCLUDED.budget_resets_at,
	revision=provider_state.revision+1,updated_at=clock_timestamp()`, provider, remaining, resets)
	return err
}
