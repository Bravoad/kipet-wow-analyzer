package application

import (
	"context"
	"encoding/json"
	"kipet-wow-analyzer/internal/domain/character"
	"time"
)

type Store interface {
	Ping(context.Context) error
	UpsertRealms(context.Context, []character.Realm) error
	ListRealms(ctx context.Context, region, locale, query, after string, limit int) ([]character.Realm, string, error)
	ResolveRealm(ctx context.Context, region, slug string) (character.Realm, error)
	Find(ctx context.Context, key character.Key) (*character.Record, error)
	IsMissing(ctx context.Context, key character.Key) (bool, error)
	Enqueue(ctx context.Context, key character.Key, reason string, manual bool) (character.EnqueueResult, error)
	GetJob(ctx context.Context, id string) (character.Job, error)
	Claim(ctx context.Context, worker string) (*character.Lease, error)
	Renew(ctx context.Context, lease character.Lease) (character.Lease, error)
	StartAttempt(ctx context.Context, lease character.Lease) error
	Publish(ctx context.Context, lease character.Lease, snapshot character.Snapshot) error
	Fail(ctx context.Context, lease character.Lease, fault *character.Fault, retryAt time.Time) error
	Quarantine(ctx context.Context, lease character.Lease) error
	Purge(ctx context.Context, lease character.Lease) error
	Maintain(context.Context) error
}

type Provider interface {
	Fetch(ctx context.Context, key character.Key, realm character.Realm) (character.Snapshot, error)
	Realms(ctx context.Context, region string) ([]character.Realm, error)
}

type ProviderBudget interface {
	Wait(ctx context.Context, region string) error
	Defer(ctx context.Context, region string, until time.Time) error
}

type SnapshotCache interface {
	Get(ctx context.Context, snapshotID, section, locale string) ([]byte, error)
	Put(ctx context.Context, snapshotID, section, locale string, data []byte, until time.Time) error
	Allow(ctx context.Context, bucket string, limit, burst int, per time.Duration) (bool, time.Duration, error)
	Ping(context.Context) error
}

// AnalysisRepository stores language-independent results. Every cache operation
// must recheck the current snapshot and publication permission in PostgreSQL.
type AnalysisRepository interface {
	LoadAnalysis(ctx context.Context, snapshotID, engineDigest, content string) (json.RawMessage, error)
	SaveAnalysis(ctx context.Context, snapshotID, engineDigest, content string, payload json.RawMessage, validUntil time.Time) error
}
