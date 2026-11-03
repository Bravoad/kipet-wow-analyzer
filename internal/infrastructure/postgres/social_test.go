package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"kipet-wow-analyzer/internal/application"
	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/guild"
)

func TestFindManyAlignsSnapshotUnknownAndNegativeResults(t *testing.T) {
	ctx, s, pool, snapshot := storedAnalysisSnapshot(t)
	missing := character.Key{Region: "eu", Realm: "synthetic", Name: "пропавший"}
	if _, err := pool.Exec(ctx, `INSERT INTO negative_lookups(key_hash,reason,expires_at) VALUES($1,'CHARACTER_UNAVAILABLE',clock_timestamp()+interval '1 minute')`, s.missingHash(missing)); err != nil {
		t.Fatal(err)
	}
	unknown := character.Key{Region: "eu", Realm: "synthetic", Name: "неизвестный"}
	results, err := s.FindMany(ctx, []character.Key{unknown, testKey(), missing, testKey()})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 || results[0] != nil || results[1] == nil || results[1].Snapshot == nil || results[1].Snapshot.ID != snapshot.ID || !results[2].Missing || results[3].ID != results[1].ID {
		t.Fatalf("unaligned batch: %#v", results)
	}
	if len(results[1].Snapshot.Sections) != 6 {
		t.Fatal("batch lost source sections")
	}
	p := application.ProjectCharacter(results[1], testKey(), "en-US", "fixture", time.Now().UTC())
	if p.Data == nil {
		t.Fatal("authorized batch profile not publishable")
	}
	if _, err = pool.Exec(ctx, `UPDATE characters SET publication_blocked_at=clock_timestamp(),publication_block_reason='provider_access_denied' WHERE id=$1`, results[1].ID); err != nil {
		t.Fatal(err)
	}
	results, err = s.FindMany(ctx, []character.Key{testKey()})
	if err != nil {
		t.Fatal(err)
	}
	if application.ProjectCharacter(results[0], testKey(), "ru-RU", "fixture", time.Now().UTC()).Data != nil {
		t.Fatal("batch bypassed quarantine")
	}
}

func TestColdComparisonSchedulesDurableJobsAndDeduplicates(t *testing.T) {
	ctx, store, pool := testStore(t)
	service := application.ComparisonService{Store: store, Mode: "fixture"}
	keys := []guild.CharacterKey{{Region: "eu", Realm: "synthetic", Name: "Кипет"}, {Region: "eu", Realm: "synthetic", Name: "Ледник"}}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := service.Read(ctx, keys, "ru-RU")
		if err != nil {
			t.Fatal(err)
		}
		comparison := result.Data.(application.Comparison)
		for _, entry := range comparison.Entries {
			if entry.Status != "pending" || entry.Data != nil || entry.Meta.JobID == nil {
				t.Fatalf("cold comparison failed to enqueue: %+v", entry)
			}
		}
	}
	var jobs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM refresh_jobs WHERE status='queued' AND reason='cache_miss'`).Scan(&jobs); err != nil || jobs != 2 {
		t.Fatal("cold comparison lost or duplicated jobs", jobs, err)
	}
}

func TestGuildCursorBatchesReclaimAndPublicGuards(t *testing.T) {
	ctx, s, pool := testStore(t)
	key := guild.Key{Region: "eu", Realm: "synthetic", Slug: "synthetic-guild"}
	job, err := s.AdmitGuild(ctx, key, false)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.AdmitGuild(ctx, key, false)
	if err != nil || duplicate.ID != job.ID {
		t.Fatal("guild admission was not deduplicated", err)
	}
	lease, err := s.ClaimGuild(ctx, "one")
	if err != nil || lease == nil {
		t.Fatal(err)
	}
	if err = s.StartGuildAttempt(ctx, *lease); err != nil {
		t.Fatal(err)
	}
	members := make([]guild.Member, 120)
	for i := range members {
		members[i] = guild.Member{Key: guild.CharacterKey{Region: "eu", Realm: "synthetic", Name: fmt.Sprintf("Игрок%c%c", 'а'+i/26, 'а'+i%26)}, ProviderID: strconv.Itoa(i + 1)}
	}
	bundle := guild.Bundle{Profile: guild.Profile{ProviderID: "900", Region: "eu", Slug: key.Slug, Name: "Synthetic Guild", Faction: "unknown"}, Roster: guild.Roster{Members: members}}
	resource, err := s.SaveGuild(ctx, *lease, bundle)
	if err != nil {
		t.Fatal(err)
	}
	lease.SnapshotID = &resource.ID
	next, done, err := s.ScheduleGuildChunk(ctx, *lease)
	if err != nil || done || next != 50 {
		t.Fatalf("first chunk %d %v %v", next, done, err)
	}
	var active int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM refresh_jobs`).Scan(&active); err != nil || active != 50 {
		t.Fatal("unbounded first chunk", active, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE guild_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := s.ClaimGuild(ctx, "two")
	if err != nil || reclaimed == nil || reclaimed.Cursor != 50 || reclaimed.SnapshotID == nil {
		t.Fatal("durable cursor lost", err)
	}
	if _, _, err = s.ScheduleGuildChunk(ctx, *lease); !errors.Is(err, character.ErrLeaseLost) {
		t.Fatal("old lease scheduled members", err)
	}
	next, done, err = s.ScheduleGuildChunk(ctx, *reclaimed)
	if err != nil || done || next != 100 {
		t.Fatal(next, done, err)
	}
	next, done, err = s.ScheduleGuildChunk(ctx, *reclaimed)
	if err != nil || !done || next != 120 {
		t.Fatal(next, done, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM refresh_jobs`).Scan(&active); err != nil || active != 120 {
		t.Fatal("cursor duplicated admission", active, err)
	}
	var payload guild.Bundle
	_ = json.Unmarshal(resource.Payload, &payload)
	if payload.Profile.ID != resource.GuildID || payload.Roster.GuildID != resource.GuildID {
		t.Fatal("internal guild identity not assigned")
	}
	// A separate refresh can quarantine/purge the roster without publishing it.
	s.options.RefreshCooldown = time.Nanosecond
	if _, err = s.AdmitGuild(ctx, key, true); err != nil {
		t.Fatal(err)
	}
	lease, err = s.ClaimGuild(ctx, "guard")
	if err != nil || lease == nil {
		t.Fatal(err)
	}
	if err = s.QuarantineGuild(ctx, *lease); err != nil {
		t.Fatal(err)
	}
	svc := application.GuildService{Repository: s, Characters: s, Mode: "fixture"}
	if result, err := svc.ReadProfile(ctx, key, "ru-RU"); character.AsFault(err).Code != "PROVIDER_ACCESS_DENIED" || result.Data != nil {
		t.Fatal("quarantine leaked roster", err)
	}
	if err = s.PurgeGuild(ctx, *lease); err != nil {
		t.Fatal(err)
	}
	if _, err = s.LookupGuild(ctx, key); character.AsFault(err).Code != "GUILD_UNAVAILABLE" {
		t.Fatal("purged roster survived", err)
	}
	if _, err = s.GetGuildJob(ctx, lease.Job.ID); !errors.Is(err, character.ErrNotFound) {
		t.Fatal("purged job survived", err)
	}
}

func TestGuildAnalysisReschedulesAfterNegativeExpiryWithoutRefetchingFreshRoster(t *testing.T) {
	ctx, s, pool := testStore(t)
	key := guild.Key{Region: "eu", Realm: "synthetic", Slug: "synthetic-guild"}
	memberKey, err := character.NormalizeKey("eu", "synthetic", "Пропавший")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO negative_lookups(key_hash,reason,expires_at) VALUES($1,'CHARACTER_UNAVAILABLE',clock_timestamp()+interval '5 minutes')`, s.missingHash(memberKey)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdmitGuild(ctx, key, false); err != nil {
		t.Fatal(err)
	}
	lease, err := s.ClaimGuild(ctx, "initial-roster")
	if err != nil || lease == nil {
		t.Fatal(err)
	}
	if err = s.StartGuildAttempt(ctx, *lease); err != nil {
		t.Fatal(err)
	}
	bundle := guild.Bundle{Profile: guild.Profile{ProviderID: "900", Region: "eu", Slug: key.Slug, Name: "Synthetic Guild", Faction: "unknown"}, Roster: guild.Roster{Members: []guild.Member{{Key: guild.CharacterKey{Region: "eu", Realm: "synthetic", Name: "Пропавший"}, ProviderID: "99"}}}}
	resource, err := s.SaveGuild(ctx, *lease, bundle)
	if err != nil {
		t.Fatal(err)
	}
	lease.SnapshotID = &resource.ID
	if _, done, e := s.ScheduleGuildChunk(ctx, *lease); e != nil || !done {
		t.Fatal("initial roster not completed", e)
	}
	// Deterministically move beyond guild refresh cooldown without sleeping.
	if _, err = pool.Exec(ctx, `UPDATE guild_jobs SET created_at=clock_timestamp()-interval '10 minutes'`); err != nil {
		t.Fatal(err)
	}
	svc := application.GuildService{Repository: s, Characters: s, Mode: "fixture"}
	result, err := svc.ReadAnalysis(ctx, key, "ru-RU")
	if err != nil || result.Meta.JobID != nil || result.Data.(guild.Analysis).Coverage.Unavailable != 1 {
		t.Fatal("valid negative should stay unavailable without re-admission", err)
	}
	var active, characterJobs int
	if err = pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status IN ('queued','running','retry_wait')) FROM guild_jobs`).Scan(&active); err != nil || active != 0 {
		t.Fatal("valid negative scheduled work", active, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE negative_lookups SET expires_at=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	result, err = svc.ReadAnalysis(ctx, key, "ru-RU")
	if err != nil || result.Meta.JobID == nil || result.Data.(guild.Analysis).Coverage.Pending != 1 {
		t.Fatal("expired negative left member pending without job", err)
	}
	jobID := *result.Meta.JobID
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM refresh_jobs`).Scan(&characterJobs); err != nil || characterJobs != 0 {
		t.Fatal("HTTP read directly scheduled member jobs", characterJobs, err)
	}
	lease, err = s.ClaimGuild(ctx, "reuse-roster")
	if err != nil || lease == nil || lease.Job.ID != jobID || lease.SnapshotID == nil || *lease.SnapshotID != resource.ID || lease.Cursor != 0 || lease.Job.Attempts != 0 {
		t.Fatal("fresh roster was not reused", err)
	}
	again, err := svc.ReadAnalysis(ctx, key, "ru-RU")
	if err != nil || again.Meta.JobID == nil || *again.Meta.JobID != jobID {
		t.Fatal("repeat read lost deduplication", err)
	}
	if next, done, e := s.ScheduleGuildChunk(ctx, *lease); e != nil || !done || next != 1 {
		t.Fatal("reused roster failed member scheduling", next, done, e)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM refresh_jobs WHERE status='queued'`).Scan(&characterJobs); err != nil || characterJobs != 1 {
		t.Fatal("member was not admitted", characterJobs, err)
	}
	// A subsequent member failure still keeps the character failure cooldown.
	if _, err = pool.Exec(ctx, `UPDATE refresh_jobs SET status='failed',finished_at=clock_timestamp(),last_error_code='PROVIDER_UNAVAILABLE'; UPDATE guild_jobs SET created_at=clock_timestamp()-interval '10 minutes'`); err != nil {
		t.Fatal(err)
	}
	result, err = svc.ReadAnalysis(ctx, key, "ru-RU")
	if err != nil || result.Meta.JobID == nil {
		t.Fatal("retry scheduling job not admitted", err)
	}
	lease, err = s.ClaimGuild(ctx, "respect-member-cooldown")
	if err != nil || lease == nil || lease.SnapshotID == nil {
		t.Fatal(err)
	}
	if _, done, e := s.ScheduleGuildChunk(ctx, *lease); e != nil || !done {
		t.Fatal(e)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM refresh_jobs`).Scan(&characterJobs); err != nil || characterJobs != 1 {
		t.Fatal("guild read bypassed member failure cooldown", characterJobs, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE guild_jobs SET created_at=clock_timestamp()-interval '10 minutes'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdmitGuild(ctx, key, true); err != nil {
		t.Fatal(err)
	}
	lease, err = s.ClaimGuild(ctx, "manual-roster")
	if err != nil || lease == nil || lease.SnapshotID != nil {
		t.Fatal("manual refresh reused a cached roster", err)
	}
}
