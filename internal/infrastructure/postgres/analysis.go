package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"kipet-wow-analyzer/internal/domain/character"
)

var ErrInvalidAnalysis = errors.New("invalid analysis cache record")
var analysisDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validAnalysisKey(snapshotID, digest, content string) bool {
	id, err := uuid.Parse(snapshotID)
	if err != nil || id.String() != snapshotID || !analysisDigestPattern.MatchString(digest) {
		return false
	}
	return content == "general_pve" || content == "raid" || content == "mythic_plus"
}

// LoadAnalysis is a cache read, not an independent publication permission.
// Current identity/snapshot and the four analysis source sections are checked in the same
// PostgreSQL statement; stale Redis or a historic report cannot bypass purge,
// quarantine, replacement, or a section's own hard deadline.
func (s *Store) LoadAnalysis(ctx context.Context, snapshotID, engineDigest, content string) (json.RawMessage, error) {
	if !validAnalysisKey(snapshotID, engineDigest, content) {
		return nil, ErrInvalidAnalysis
	}
	var payload []byte
	err := s.pool.QueryRow(ctx, `SELECT a.payload FROM analysis_results a
	 JOIN character_snapshots x ON x.id=a.snapshot_id
	 JOIN characters c ON c.id=x.character_id AND c.current_snapshot_id=x.id
	 JOIN snapshot_sections p ON p.snapshot_id=x.id AND p.section='profile'
	 WHERE a.snapshot_id=$1 AND a.engine_digest=$2 AND a.content=$3
	 AND a.valid_until>clock_timestamp() AND x.valid_until>clock_timestamp()
	 AND c.publication_blocked_at IS NULL
	 AND COALESCE(c.data_valid_until,c.created_at+interval '720 hours')>clock_timestamp()
	 AND p.state='complete' AND p.valid_until>clock_timestamp()
	 AND a.valid_until<=x.valid_until AND a.valid_until<=p.valid_until
	 AND (SELECT count(*) FROM snapshot_sections v WHERE v.snapshot_id=x.id
	   AND v.section IN ('profile','equipment','statistics','talents'))=4
	 AND NOT EXISTS(SELECT 1 FROM snapshot_sections v WHERE v.snapshot_id=x.id
	   AND v.section IN ('profile','equipment','statistics','talents')
	   AND v.state<>'unavailable' AND (v.valid_until<=clock_timestamp() OR a.valid_until>v.valid_until))`, snapshotID, engineDigest, content).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, character.ErrNotFound
	}
	return json.RawMessage(payload), err
}

// SaveAnalysis serializes with publication/purge/quarantine through the character
// row. An unexpired key keeps its original payload even if another request
// calculated the same report concurrently. Expired entries may be refreshed.
func (s *Store) SaveAnalysis(ctx context.Context, snapshotID, engineDigest, content string, payload json.RawMessage, validUntil time.Time) error {
	if !validAnalysisKey(snapshotID, engineDigest, content) || len(payload) == 0 || len(payload) > 1<<20 || !json.Valid(payload) {
		return ErrInvalidAnalysis
	}
	var identity struct {
		SnapshotID string `json:"snapshot_id"`
		Context    struct {
			Content string `json:"content"`
		} `json:"context"`
	}
	if json.Unmarshal(payload, &identity) != nil || identity.SnapshotID != snapshotID || identity.Context.Content != content {
		return ErrInvalidAnalysis
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var ceiling, now time.Time
	err = tx.QueryRow(ctx, `SELECT LEAST(x.valid_until,p.valid_until,
	 COALESCE(c.data_valid_until,c.created_at+interval '720 hours')),clock_timestamp()
	 FROM character_snapshots x
	 JOIN characters c ON c.id=x.character_id AND c.current_snapshot_id=x.id
	 JOIN snapshot_sections p ON p.snapshot_id=x.id AND p.section='profile'
	 WHERE x.id=$1 AND c.publication_blocked_at IS NULL
	 AND x.valid_until>clock_timestamp() AND p.state='complete' AND p.valid_until>clock_timestamp()
	 AND COALESCE(c.data_valid_until,c.created_at+interval '720 hours')>clock_timestamp()
	 FOR UPDATE OF c`, snapshotID).Scan(&ceiling, &now)
	if errors.Is(err, pgx.ErrNoRows) {
		return character.ErrNotFound
	}
	if err != nil {
		return err
	}
	var hard, fresh *time.Time
	var available bool
	var count int
	err = tx.QueryRow(ctx, `SELECT
	 min(valid_until) FILTER(WHERE state<>'unavailable'),
	 min(fresh_until) FILTER(WHERE state<>'unavailable' AND fresh_until>clock_timestamp()),
	 COALESCE(bool_and(state='unavailable' OR valid_until>clock_timestamp()),false),count(*)
	 FROM snapshot_sections WHERE snapshot_id=$1
	 AND section IN ('profile','equipment','statistics','talents')`, snapshotID).Scan(&hard, &fresh, &available, &count)
	if err != nil {
		return err
	}
	if !available || count != 4 {
		// A newly computed report may still be returned with an expired section
		// sanitized to Unknown. It is deliberately not persisted until cleanup
		// has removed the expired source payload.
		return character.ErrNotFound
	}
	if hard != nil && hard.Before(ceiling) {
		ceiling = *hard
	}
	if fresh != nil && fresh.Before(ceiling) {
		ceiling = *fresh
	}
	if validUntil.After(ceiling) {
		validUntil = ceiling
	}
	if !validUntil.After(now) {
		return character.ErrNotFound
	}
	_, err = tx.Exec(ctx, `INSERT INTO analysis_results(snapshot_id,engine_digest,content,payload,valid_until)
	 VALUES($1,$2,$3,$4::jsonb,$5)
	 ON CONFLICT(snapshot_id,engine_digest,content) DO UPDATE SET
	 payload=EXCLUDED.payload,valid_until=EXCLUDED.valid_until,created_at=clock_timestamp()
	 WHERE analysis_results.valid_until<=clock_timestamp()`, snapshotID, engineDigest, content, []byte(payload), validUntil.UTC())
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
