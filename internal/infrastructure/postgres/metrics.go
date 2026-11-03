package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Metrics exposes aggregate operational values only. No character identifiers
// become Prometheus labels or metric values.
func (s *Store) Metrics(ctx context.Context) (map[string]float64, error) {
	result := map[string]float64{}
	for _, prefix := range []string{"", "combat_", "guild_"} {
		for _, name := range []string{"queue_active", "queue_running", "queue_due", "queue_retry_wait", "queue_failed", "queue_expired_leases", "oldest_pending_seconds", "oldest_due_seconds"} {
			result[prefix+name] = 0
		}
	}
	rows, err := s.pool.Query(ctx, `WITH jobs AS (
	 SELECT '' AS prefix,status,created_at,run_after,lease_until FROM refresh_jobs
	 UNION ALL SELECT 'combat_',status,created_at,run_after,lease_until FROM combat_jobs
	 UNION ALL SELECT 'guild_',status,created_at,run_after,lease_until FROM guild_jobs
	)
	SELECT prefix,
	 count(*) FILTER(WHERE status IN ('queued','running','retry_wait'))::float8,
	 count(*) FILTER(WHERE status='running')::float8,
	 count(*) FILTER(WHERE status IN ('queued','retry_wait') AND run_after<=clock_timestamp())::float8,
	 count(*) FILTER(WHERE status='retry_wait')::float8,
	 count(*) FILTER(WHERE status='failed')::float8,
	 count(*) FILTER(WHERE status='running' AND lease_until<=clock_timestamp())::float8,
	 COALESCE(GREATEST(0,EXTRACT(EPOCH FROM clock_timestamp()-min(created_at) FILTER(WHERE status IN ('queued','running','retry_wait')))),0)::float8,
	 COALESCE(GREATEST(0,EXTRACT(EPOCH FROM clock_timestamp()-min(run_after) FILTER(WHERE status IN ('queued','retry_wait') AND run_after<=clock_timestamp()))),0)::float8
	FROM jobs GROUP BY prefix`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var prefix string
		var active, running, due, retry, failed, expired, age, overdue float64
		if err = rows.Scan(&prefix, &active, &running, &due, &retry, &failed, &expired, &age, &overdue); err != nil {
			rows.Close()
			return nil, err
		}
		result[prefix+"queue_active"], result[prefix+"queue_running"], result[prefix+"queue_due"] = active, running, due
		result[prefix+"queue_retry_wait"], result[prefix+"queue_failed"], result[prefix+"queue_expired_leases"] = retry, failed, expired
		result[prefix+"oldest_pending_seconds"], result[prefix+"oldest_due_seconds"] = age, overdue
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var snapshots, profileAge float64
	if err = s.pool.QueryRow(ctx, `SELECT count(*)::float8,COALESCE(GREATEST(0,EXTRACT(EPOCH FROM clock_timestamp()-min(profile_verified_at))),0)::float8 FROM character_snapshots WHERE valid_until>clock_timestamp()`).Scan(&snapshots, &profileAge); err != nil {
		return nil, err
	}
	result["snapshots_total"] = snapshots
	result["oldest_profile_seconds"] = profileAge
	result["warcraftlogs_points_known"] = 0
	var points *float64
	var resetIn float64
	err = s.pool.QueryRow(ctx, `SELECT budget_remaining::float8,GREATEST(0,EXTRACT(EPOCH FROM budget_resets_at-clock_timestamp()))::float8
	 FROM provider_state WHERE provider='warcraftlogs' AND budget_resets_at>clock_timestamp()`).Scan(&points, &resetIn)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil && points != nil {
		result["warcraftlogs_points_known"] = 1
		result["warcraftlogs_points_remaining"], result["warcraftlogs_points_reset_seconds"] = *points, resetIn
	}
	return result, nil
}
