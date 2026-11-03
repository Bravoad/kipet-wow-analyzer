package raiderio

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/raiderio"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (fn roundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func response(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Body: io.NopCloser(strings.NewReader(body)),
		Header: make(http.Header), Request: req, ContentLength: -1,
	}
}

func fixtureClient(t *testing.T, cfg Config, transport roundTrip) *Client {
	t.Helper()
	cfg.HTTPClient = &http.Client{Transport: transport}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var aliasKey = raiderio.Key{Region: "eu", Realm: "Гордунни", Name: "Кипет"}

const identity = `"name":"Кипет","region":"eu","realm":"Gordunni","profile_url":"https://raider.io/characters/eu/gordunni/%D0%9A%D0%B8%D0%BF%D0%B5%D1%82"`

func TestFetchNormalizesPublicProfileWithoutInventingValues(t *testing.T) {
	now := time.Date(2026, 11, 3, 20, 0, 0, 0, time.UTC)
	permitted := false
	cfg := Config{
		Now: func() time.Time { return now },
		Permit: func(ctx context.Context) error {
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > requestDeadline {
				t.Fatal("unbounded provider permit")
			}
			permitted = true
			return nil
		},
	}
	c := fixtureClient(t, cfg, func(req *http.Request) (*http.Response, error) {
		if !permitted || req.Method != http.MethodGet || req.URL.Scheme != "https" ||
			req.URL.Host != "raider.io" || req.URL.Path != "/api/v1/characters/profile" {
			t.Fatalf("unexpected request %s %s", req.Method, req.URL)
		}
		query := req.URL.Query()
		if query.Get("region") != "eu" || query.Get("realm") != "гордунни" ||
			query.Get("name") != "кипет" || query.Get("fields") != "gear,talents,guild,mythic_plus_scores_by_season:current,mythic_plus_best_runs,raid_progression" || len(query) != 4 {
			t.Fatalf("unexpected query %v", query)
		}
		body := `{` + identity + `,
			"class":"Hunter","active_spec_name":"Beast Mastery","active_spec_role":"DPS","faction":"Alliance",
			"guild":{"name":"Тестовая гильдия","realm":"Gordunni"},
			"last_crawled_at":"2026-11-03T19:55:42.000Z",
			"gear":{"item_level_equipped":330.812,"item_level_total":0,"updated_at":"2026-11-03T19:55:43.000Z","items":{
				"head":{"item_id":271492,"item_level":321,"item_quality":4,"enchants":[8017],"enchants_detail":[{"id":244007,"name":"Synthetic enchant"}],"gems":[],"bonuses":[6652]},
				"mainhand":{"item_id":251187}}},
			"talentLoadout":{"loadout_spec_id":253,"loadout_text":"SyntheticCode"},
			"mythic_plus_scores_by_season":[{"season":"season-mn-2","scores":{"all":3002.7,"healer":0}}],
			"mythic_plus_best_runs":[
				{"dungeon":"Example","mythic_level":13,"clear_time_ms":1000,"par_time_ms":2000,"score":386,"url":"https://raider.io/mythic-plus-runs/season-mn-2/1-example"},
				{"dungeon":"Other","mythic_level":12,"clear_time_ms":1000,"url":"https://raider.io.evil.example/mythic-plus-runs/season-mn-2/2-other"}
			],
			"raid_progression":{"example":{"expansion_id":11,"summary":"1/1 H","total_bosses":1,"heroic_bosses_killed":1,"mythic_bosses_killed":0}}
		}`
		return response(req, 200, body), nil
	})
	snapshot, err := c.Fetch(context.Background(), aliasKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = raiderio.ValidateSnapshot(aliasKey, snapshot); err != nil ||
		!snapshot.FreshUntil.Equal(now.Add(15*time.Minute)) || !snapshot.ValidUntil.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("bad snapshot TTL or identity: %v", err)
	}
	p := snapshot.Profile
	if p.Guild == nil || p.Guild.Name != "Тестовая гильдия" || p.Guild.Realm != "Gordunni" {
		t.Fatalf("guild reference lost: %+v", p.Guild)
	}
	if p.Realm.Slug != "gordunni" || p.Realm.Name != "Gordunni" || p.Race != nil ||
		p.Gear == nil || *p.Gear.EquippedItemLevel != 330.812 || *p.Gear.TotalItemLevel != 0 ||
		p.Coverage["gear"].SourceUpdatedAt == nil || len(p.Gear.Items) != 2 {
		t.Fatalf("bad profile or gear: %+v", p)
	}
	head, weapon := p.Gear.Items[0], p.Gear.Items[1]
	if head.EnchantIDs[0] != "8017" || head.Enchantments[0].ID != "244007" ||
		head.GemIDs == nil || len(head.GemIDs) != 0 || weapon.EnchantIDs != nil ||
		weapon.ItemLevel != nil || weapon.GemIDs != nil {
		t.Fatalf("raw IDs, detail IDs, or unknown collections changed: %+v", p.Gear.Items)
	}
	if p.Talents == nil || *p.Talents.SpecializationID != "253" || *p.Talents.ExportCode != "SyntheticCode" ||
		p.MythicPlus == nil || *p.MythicPlus.Season != "season-mn-2" || *p.MythicPlus.Rating != 3002.7 ||
		*p.MythicPlus.Scores["healer"] != 0 || len(p.MythicPlus.BestRuns) != 2 ||
		!*p.MythicPlus.BestRuns[0].Timed || p.MythicPlus.BestRuns[0].URL == nil ||
		p.MythicPlus.BestRuns[1].Timed != nil || p.MythicPlus.BestRuns[1].URL != nil {
		t.Fatalf("bad talents or Mythic+ data: %+v", p)
	}
	if len(p.Raids) != 1 || p.Raids[0].Normal != nil || *p.Raids[0].Mythic != 0 ||
		*p.Raids[0].ExpansionID != "11" {
		t.Fatalf("bad raid data: %+v", p.Raids)
	}
}

func TestProviderIdentityAndBodyValidation(t *testing.T) {
	tests := map[string]string{
		"wrong region": strings.Replace(identity, `"region":"eu"`, `"region":"us"`, 1),
		"wrong name":   strings.Replace(identity, `"name":"Кипет"`, `"name":"Иное"`, 1),
		"wrong link":   strings.Replace(identity, "characters/eu/", "characters/us/", 1),
		"wrong host":   strings.Replace(identity, "raider.io/characters", "raider.io.evil.example/characters", 1),
		"unsafe path":  strings.Replace(identity, "/gordunni/", "/gordunni%2Fother/", 1),
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			c := fixtureClient(t, Config{}, func(req *http.Request) (*http.Response, error) {
				return response(req, 200, "{"+payload+"}"), nil
			})
			_, err := c.Fetch(context.Background(), aliasKey)
			if fault := character.AsFault(err); fault.Code != "PROVIDER_INVALID_RESPONSE" || fault.Retryable {
				t.Fatalf("wrong fault: %v", err)
			}
		})
	}
	for _, body := range []string{"{" + identity + "}{}", strings.Repeat(" ", maxResponseBytes+1)} {
		c := fixtureClient(t, Config{}, func(req *http.Request) (*http.Response, error) {
			return response(req, 200, body), nil
		})
		if _, err := c.Fetch(context.Background(), aliasKey); character.AsFault(err).Code != "PROVIDER_INVALID_RESPONSE" {
			t.Fatalf("invalid payload accepted: %v", err)
		}
	}
}

func TestOptionalCoverageDistinguishesUnknownFromEmpty(t *testing.T) {
	p, err := normalizeProfile(aliasKey, []byte(`{`+identity+`,
		"gear":{"items":{}},"talentLoadout":null,"mythic_plus_best_runs":[],"raid_progression":"broken"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Gear.Items == nil || len(p.Gear.Items) != 0 || p.Talents != nil ||
		p.MythicPlus.BestRuns == nil || len(p.MythicPlus.BestRuns) != 0 || p.MythicPlus.Rating != nil ||
		p.MythicPlus.Season != nil || p.Raids != nil {
		t.Fatalf("absence and empty arrays conflated: %+v", p)
	}
	if p.Coverage["gear"].State != "complete" || p.Coverage["talents"].State != "unavailable" ||
		p.Coverage["mythic_plus"].State != "partial" || p.Coverage["raids"].Reason == nil ||
		*p.Coverage["raids"].Reason != "invalid_response" {
		t.Fatalf("coverage does not reflect observations: %+v", p.Coverage)
	}
}

func TestProviderRateLimitPausesAndRedirectsStayBlocked(t *testing.T) {
	now := time.Date(2026, 11, 3, 20, 0, 0, 0, time.UTC)
	var pause time.Time
	c := fixtureClient(t, Config{
		Now:        func() time.Time { return now },
		PauseUntil: func(_ context.Context, until time.Time) error { pause = until; return nil },
	}, func(req *http.Request) (*http.Response, error) {
		r := response(req, http.StatusTooManyRequests, "provider details")
		r.Header.Set("Retry-After", "73")
		return r, nil
	})
	_, err := c.Fetch(context.Background(), aliasKey)
	fault := character.AsFault(err)
	if fault.Code != "PROVIDER_RATE_LIMITED" || !fault.Retryable ||
		!fault.RetryAt.Equal(now.Add(73*time.Second)) || !pause.Equal(fault.RetryAt) {
		t.Fatalf("Retry-After was lost: %+v", fault)
	}
	calls := 0
	c = fixtureClient(t, Config{}, func(req *http.Request) (*http.Response, error) {
		calls++
		r := response(req, http.StatusFound, "")
		r.Header.Set("Location", "https://example.org")
		return r, nil
	})
	_, err = c.Fetch(context.Background(), aliasKey)
	if character.AsFault(err).Code != "PROVIDER_INVALID_RESPONSE" || calls != 1 {
		t.Fatalf("redirect was followed or accepted: calls=%d error=%v", calls, err)
	}
}

func TestNotFoundAndPrivateAreDifferentOutcomes(t *testing.T) {
	for code, want := range map[int]string{404: "CHARACTER_UNAVAILABLE", 403: "PROVIDER_ACCESS_DENIED"} {
		c := fixtureClient(t, Config{}, func(req *http.Request) (*http.Response, error) {
			return response(req, code, "provider details"), nil
		})
		_, err := c.Fetch(context.Background(), aliasKey)
		if fault := character.AsFault(err); fault.Code != want || fault.Retryable {
			t.Fatalf("status %d gave %v", code, err)
		}
	}
}
