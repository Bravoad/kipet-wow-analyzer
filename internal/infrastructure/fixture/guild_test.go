package fixture

import (
	"context"
	"encoding/json"
	"testing"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/guild"
	"kipet-wow-analyzer/internal/domain/progress"
)

func TestSyntheticGuildUsesExplicitRosterAndDistinctCharacterFacts(t *testing.T) {
	ctx := context.Background()
	p := New()
	realms, _ := p.Realms(ctx, "eu")
	key, _ := guild.NormalizeKey("eu", "гордунни", "kipet-lab")
	bundle, err := p.FetchGuild(ctx, key, realms[0])
	if err != nil || bundle.Roster.Members == nil || len(bundle.Roster.Members) != 3 {
		t.Fatal(bundle, err)
	}
	observations := []guild.Observation{}
	for _, member := range bundle.Roster.Members {
		characterKey, _ := character.NormalizeKey(member.Key.Region, member.Key.Realm, member.Key.Name)
		snapshot, err := p.Fetch(ctx, characterKey, realms[0])
		if member.Key.Name == "Пропавший" {
			if character.AsFault(err).Code != "CHARACTER_UNAVAILABLE" {
				t.Fatal(err)
			}
			observations = append(observations, guild.Observation{Member: member, Status: "unavailable"})
			continue
		}
		if err != nil || character.ValidateSnapshot(snapshot) != nil || snapshot.ProviderID != member.ProviderID {
			t.Fatal(snapshot, err)
		}
		id := "synthetic-snapshot-" + snapshot.ProviderID
		obs := guild.Observation{Member: member, SnapshotID: &id, Status: "fresh", Profile: snapshot.Sections["profile"].Payload, Sections: map[string]json.RawMessage{}}
		for name, section := range snapshot.Sections {
			obs.Sections[name] = section.Payload
		}
		observations = append(observations, obs)
		if member.Key.Name == "Ледник" {
			var mythic progress.MythicPlus
			if json.Unmarshal(snapshot.Sections["mythic_plus"].Payload, &mythic) != nil || mythic.Rating == nil || *mythic.Rating != 1300 {
				t.Fatal("second fixture facts not distinct")
			}
		}
	}
	result := guild.Analyze(bundle, observations)
	if result.Coverage.Profiles != 2 || result.Coverage.Roster != 3 || result.Coverage.Unavailable != 1 || *result.ItemLevel.Mean != 274 || *result.ItemLevel.Median != 274 {
		t.Fatal(result)
	}
	key.Slug = "unknown-guild"
	if _, err := p.FetchGuild(ctx, key, realms[0]); character.AsFault(err).Code != "GUILD_UNAVAILABLE" {
		t.Fatal(err)
	}
}
