package raiderio

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/raiderio"
)

const guildIdentity = `"name":"Тестовая гильдия","region":"eu","realm":"Gordunni","profile_url":"https://raider.io/guilds/eu/gordunni/Тестовая%20гильдия"`

func TestFetchGuildWithValidatedRosterAndRankings(t *testing.T) {
	key, err := raiderio.NormalizeGuildKey("EU", "Гордунни", "Тестовая гильдия")
	if err != nil {
		t.Fatal(err)
	}
	c := fixtureClient(t, Config{}, func(req *http.Request) (*http.Response, error) {
		query := req.URL.Query()
		if req.Method != http.MethodGet || req.URL.Scheme != "https" || req.URL.Host != "raider.io" ||
			req.URL.Path != "/api/v1/guilds/profile" || query.Get("region") != "eu" || query.Get("realm") != "гордунни" ||
			query.Get("name") != "тестовая гильдия" || query.Get("fields") != "members,raid_progression,raid_rankings" || len(query) != 4 {
			t.Fatalf("unexpected guild request: %s %s", req.Method, req.URL)
		}
		body := `{` + guildIdentity + `,
			"members":[
				{"rank":0,"character":{` + identity + `,"class":"Hunter","active_spec_name":"Beast Mastery","active_spec_role":"DPS"}},
				{"character":{` + strings.Replace(identity, "raider.io/", "example.org/", 1) + `}},
				{"character":{` + identity + `}}
			],
			"raid_progression":{"tier-example":{"summary":"1/1 H","heroic_bosses_killed":1}},
			"raid_rankings":{"tier-example":{"heroic":{"world":42,"region":0}}}
		}`
		return response(req, http.StatusOK, body), nil
	})
	snapshot, err := c.Fetch(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if err := raiderio.ValidateSnapshot(key, snapshot); err != nil {
		t.Fatalf("invalid guild snapshot: %v", err)
	}
	guild := snapshot.Guild
	if guild == nil || guild.Realm.Slug != "gordunni" || len(guild.Members) != 1 || guild.Coverage["members"].State != "partial" {
		t.Fatalf("incorrect roster identity or coverage: %+v", guild)
	}
	member := guild.Members[0]
	if member.Key != (raiderio.Key{Region: "eu", Realm: "gordunni", Name: "Кипет"}) || member.Rank == nil || *member.Rank != 0 ||
		member.Specialization == nil || *member.Specialization != "Beast Mastery" {
		t.Fatalf("incorrect normalized member: %+v", member)
	}
	rank := guild.Rankings["tier-example"]["heroic"]
	if len(guild.Raids) != 1 || rank.World == nil || *rank.World != 42 || rank.Region == nil || *rank.Region != 0 || rank.Realm != nil {
		t.Fatalf("raid progress, ranking, or null/zero changed: %+v %+v", guild.Raids, rank)
	}
}

func TestGuildEmptyAndMissingCollectionsRemainDistinct(t *testing.T) {
	key, err := raiderio.NormalizeGuildKey("eu", "gordunni", "Тестовая гильдия")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		fields string
		state  string
	}{
		{fields: `,"members":[],"raid_progression":{},"raid_rankings":{}`, state: "complete"},
		{fields: `,"members":null,"raid_rankings":"malformed"`, state: "unavailable"},
	} {
		guild, err := normalizeGuildProfile(key, []byte("{"+guildIdentity+test.fields+"}"))
		if err != nil {
			t.Fatal(err)
		}
		present := test.state == "complete"
		if (guild.Members != nil) != present || (guild.Raids != nil) != present || (guild.Rankings != nil) != present ||
			guild.Coverage["members"].State != test.state || guild.Coverage["rankings"].State != test.state {
			t.Fatalf("collection absence and empty values conflated: %+v", guild)
		}
	}
	if _, err := normalizeGuildProfile(key, []byte("{"+strings.Replace(guildIdentity, "/guilds/", "/characters/", 1)+"}")); character.AsFault(err).Code != "PROVIDER_INVALID_RESPONSE" {
		t.Fatalf("invalid guild identity accepted: %v", err)
	}
	c := fixtureClient(t, Config{}, func(req *http.Request) (*http.Response, error) {
		return response(req, http.StatusNotFound, ""), nil
	})
	if _, err := c.Fetch(context.Background(), key); character.AsFault(err).Code != "GUILD_UNAVAILABLE" {
		t.Fatalf("404 did not report unavailable guild: %v", err)
	}
}
