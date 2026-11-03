package postgres

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"kipet-wow-analyzer/internal/application"
	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/raiderio"
)

func TestRaiderJobFencesPublicationAndRevocation(t *testing.T) {
	ctx, store, pool := testStore(t)
	key, err := raiderio.NormalizeKey("eu", "gordunni", "Кипет")
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.AdmitRaider(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := store.AdmitRaider(ctx, key)
	if err != nil || job.ID != duplicate.ID {
		t.Fatal("Raider.IO admission did not deduplicate", err)
	}
	first, err := store.ClaimRaider(ctx)
	if err != nil || first == nil {
		t.Fatal("Raider.IO job was not claimed", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE raiderio_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimRaider(ctx)
	if err != nil || lease == nil || lease.Token == first.Token {
		t.Fatal("expired lease was not reclaimed", err)
	}
	now := time.Now().UTC()
	snapshot := raiderio.Snapshot{
		Profile:   raiderio.Profile{Name: "Кипет", Region: "eu", Realm: raiderio.Realm{Name: "Gordunni", Slug: "gordunni"}, ProfileURL: "https://raider.io/characters/eu/gordunni/Кипет"},
		FetchedAt: now, FreshUntil: now.Add(time.Minute), ValidUntil: now.Add(time.Hour),
	}
	if err := store.SaveRaider(ctx, *first, snapshot); !errors.Is(err, character.ErrLeaseLost) {
		t.Fatal("expired worker could publish", err)
	}
	if err := store.SaveRaider(ctx, *lease, snapshot); err != nil {
		t.Fatal(err)
	}
	service := application.RaiderService{Repository: store}
	result, err := service.Read(ctx, key, "ru-RU")
	if err != nil || result.Meta.Source != "raiderio" || result.Data.(raiderio.Profile).ProfileURL != "https://raider.io/characters/eu/gordunni/%D0%9A%D0%B8%D0%BF%D0%B5%D1%82" {
		t.Fatal("public envelope has invalid source or link", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE raiderio_jobs SET created_at=clock_timestamp()-interval '2 minutes' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AdmitRaider(ctx, key); err != nil {
		t.Fatal(err)
	}
	lease, err = store.ClaimRaider(ctx)
	if err != nil || lease == nil {
		t.Fatal("refresh was not claimed", err)
	}
	if err := store.FailRaider(ctx, *lease, character.NewFault("PROVIDER_ACCESS_DENIED", true), now); err != nil {
		t.Fatal(err)
	}
	result, err = service.Read(ctx, key, "ru-RU")
	if character.AsFault(err).Code != "PROVIDER_ACCESS_DENIED" || result.Data != nil {
		t.Fatal("quarantine leaked stored profile", err)
	}
	lease, err = store.ClaimRaider(ctx)
	if err != nil || lease == nil {
		t.Fatal("verification was not claimed", err)
	}
	if err := store.PurgeRaider(ctx, *lease); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupRaider(ctx, key); character.AsFault(err).Code != "CHARACTER_UNAVAILABLE" {
		t.Fatal("negative guard not applied", err)
	}
}

func TestRaiderGuildAndCharacterWithSameKeyStayIsolated(t *testing.T) {
	ctx, store, pool := testStore(t)
	characterKey, err := raiderio.NormalizeKey("eu", "gordunni", "Кипет")
	if err != nil {
		t.Fatal(err)
	}
	guildKey, err := raiderio.NormalizeGuildKey("eu", "gordunni", "Кипет")
	if err != nil {
		t.Fatal(err)
	}
	characterJob, err := store.AdmitRaider(ctx, characterKey)
	if err != nil {
		t.Fatal(err)
	}
	guildJob, err := store.AdmitRaider(ctx, guildKey)
	if err != nil || guildJob.ID == characterJob.ID {
		t.Fatal("guild and character must admit independent jobs", err)
	}
	duplicate, err := store.AdmitRaider(ctx, guildKey)
	if err != nil || duplicate.ID != guildJob.ID {
		t.Fatal("guild admission did not deduplicate", err)
	}
	leases := make(map[string]application.RaiderLease, 2)
	for range 2 {
		lease, err := store.ClaimRaider(ctx)
		if err != nil || lease == nil {
			t.Fatal("Raider.IO job was not claimed", err)
		}
		leases[lease.Job.ID] = *lease
	}
	if leases[characterJob.ID].Key != characterKey || leases[guildJob.ID].Key != guildKey {
		t.Fatal("claim did not preserve the resource identity")
	}
	now := time.Now().UTC()
	realm := raiderio.Realm{Name: "Gordunni", Slug: "gordunni"}
	snapshot := raiderio.Snapshot{FetchedAt: now, FreshUntil: now.Add(time.Minute), ValidUntil: now.Add(time.Hour)}
	snapshot.Profile = raiderio.Profile{Name: "Кипет", Region: "eu", Realm: realm, ProfileURL: "https://raider.io/characters/eu/gordunni/Кипет"}
	if err := store.SaveRaider(ctx, leases[characterJob.ID], snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Profile = raiderio.Profile{}
	snapshot.Guild = &raiderio.GuildProfile{Name: "Кипет", Region: "eu", Realm: realm, ProfileURL: "https://raider.io/guilds/eu/gordunni/Кипет"}
	if err := store.SaveRaider(ctx, leases[guildJob.ID], snapshot); err != nil {
		t.Fatal(err)
	}
	service := application.RaiderService{Repository: store}
	result, err := service.Read(ctx, guildKey, "ru-RU")
	guild, ok := result.Data.(raiderio.GuildProfile)
	if err != nil || !ok || guild.ProfileURL != "https://raider.io/guilds/eu/gordunni/%D0%9A%D0%B8%D0%BF%D0%B5%D1%82" {
		t.Fatal("guild snapshot had the wrong payload or canonical link", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE raiderio_jobs SET created_at=clock_timestamp()-interval '2 minutes' WHERE id=$1`, guildJob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AdmitRaider(ctx, guildKey); err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimRaider(ctx)
	if err != nil || lease == nil {
		t.Fatal("guild refresh was not claimed", err)
	}
	wrongKind := *lease
	wrongKind.Key = characterKey
	if err := store.PurgeRaider(ctx, wrongKind); !errors.Is(err, character.ErrLeaseLost) {
		t.Fatal("lease could purge a different kind", err)
	}
	if err := store.PurgeRaider(ctx, *lease); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupRaider(ctx, guildKey); character.AsFault(err).Code != "GUILD_UNAVAILABLE" {
		t.Fatal("purged guild must have a guild-specific negative lookup", err)
	}
	result, err = service.Read(ctx, characterKey, "ru-RU")
	if _, ok := result.Data.(raiderio.Profile); err != nil || !ok {
		t.Fatal("guild purge affected same-name character", err)
	}

	// Character lookup hashes generated before the kind discriminator still apply.
	legacy := hmac.New(sha256.New, store.options.HMACSecret)
	_, _ = legacy.Write([]byte("raiderio\x00eu\x00gordunni\x00" + characterKey.Name))
	if _, err := pool.Exec(ctx, `INSERT INTO raiderio_negative_lookups(key_hash,expires_at)
		VALUES($1,clock_timestamp()+interval '1 minute')`, legacy.Sum(nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupRaider(ctx, characterKey); character.AsFault(err).Code != "CHARACTER_UNAVAILABLE" {
		t.Fatal("legacy character negative lookup was lost", err)
	}
}
