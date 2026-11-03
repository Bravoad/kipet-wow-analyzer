package blizzard

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/progress"
)

func progressRaw(t *testing.T, body string) object {
	t.Helper()
	var raw object
	if err := decode([]byte(body), &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestNormalizeMythicPreservesZeroFalseAndUnknownCollections(t *testing.T) {
	index := progressRaw(t, `{"seasons":[{"id":42,"name":"Synthetic season"}],"current_mythic_rating":{"rating":0}}`)
	details := progressRaw(t, `{"season":{"id":42},"mythic_rating":{"rating":0},"best_runs":[{"dungeon":{"id":9007199254740993,"name":"Synthetic dungeon"},"keystone_level":2,"duration":0,"completed_timestamp":1790000000000,"is_completed_within_time":false,"mythic_rating":{"rating":0}}]}`)
	yes := true
	current := &progress.Season{ID: "42", IsCurrent: &yes}
	actual, partial := normalizeMythic(index, details, current)
	if partial || actual.RunScope != "season_best" || actual.Rating == nil || *actual.Rating != 0 || len(actual.BestRuns) != 1 {
		t.Fatalf("known observation lost: %#v partial=%v", actual, partial)
	}
	run := actual.BestRuns[0]
	if run.Dungeon.ID != "9007199254740993" || run.WithinTime == nil || *run.WithinTime || run.DurationMS == nil || *run.DurationMS != 0 || run.Rating == nil || *run.Rating != 0 {
		t.Fatalf("zero/false/id observation lost: %#v", run)
	}
	actual, partial = normalizeMythic(progressRaw(t, `{"current_mythic_rating":{"rating":0}}`), nil, nil)
	if !partial || actual == nil || actual.BestRuns != nil || actual.AvailableSeasons != nil || actual.Season != nil || actual.RunScope != "unknown" {
		t.Fatalf("unknown collection invented: %#v", actual)
	}
	actual, partial = normalizeMythic(progressRaw(t, `{"seasons":[],"current_period":{"best_runs":[]}}`), nil, nil)
	if !partial || actual.BestRuns == nil || len(actual.BestRuns) != 0 || actual.AvailableSeasons == nil || actual.RunScope != "current_period_best" {
		t.Fatalf("confirmed empty observation lost: %#v", actual)
	}
	if actual, _ = normalizeMythic(progressRaw(t, `{}`), nil, nil); actual != nil {
		t.Fatal("empty source became a progress observation")
	}
}

func TestNormalizeMythicMalformedRunsDoNotInventValues(t *testing.T) {
	index := progressRaw(t, `{"seasons":[{"id":42},{"id":42},{"id":-1}],"current_period":{"best_runs":[{"dungeon":{"id":1},"keystone_level":2,"duration":-1,"completed_timestamp":999999999999999999,"is_completed_within_time":"false","mythic_rating":{"rating":-3}},{"dungeon":{"id":2},"keystone_level":2.5},{"dungeon":{"id":3},"keystone_level":1001}]}}`)
	actual, partial := normalizeMythic(index, nil, nil)
	if !partial || len(actual.BestRuns) != 1 || len(actual.AvailableSeasons) != 1 {
		t.Fatalf("invalid rows accepted %#v", actual)
	}
	run := actual.BestRuns[0]
	if run.DurationMS != nil || run.CompletedAt != nil || run.WithinTime != nil || run.Rating != nil {
		t.Fatalf("unknown values invented %#v", run)
	}
	section := progressSection(actual, partial, time.Now().UTC(), nil)
	if section.State != "partial" || len(section.Payload) == 0 {
		t.Fatal("normalized malformed values failed safe serialization")
	}
}

func TestNormalizeMythicDoesNotMixCachedSeasonAndCurrentIndexRating(t *testing.T) {
	yes := true
	cached := &progress.Season{ID: "42", IsCurrent: &yes}
	details := progressRaw(t, `{"season":{"id":42},"best_runs":[]}`)
	for _, body := range []string{
		`{"seasons":[{"id":43}],"current_mythic_rating":{"rating":2400}}`,
		`{"current_season":{"id":43},"seasons":[{"id":43}],"current_mythic_rating":{"rating":2400}}`,
	} {
		actual, partial := normalizeMythic(progressRaw(t, body), details, cached)
		if !partial || actual.Season == nil || actual.Season.ID != "42" || actual.Rating != nil || actual.RunScope != "season_best" {
			t.Fatalf("current index rating mixed into cached-season detail: %#v", actual)
		}
	}
	matching := progressRaw(t, `{"current_season":{"id":42},"seasons":[],"current_mythic_rating":{"rating":0}}`)
	actual, partial := normalizeMythic(matching, details, cached)
	if partial || actual.Rating == nil || *actual.Rating != 0 {
		t.Fatal("explicit matching-season rating was not reused")
	}
}

func TestMythicDetail404DoesNotLabelCurrentPeriodWithCachedSeason(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			tokenResponse(w)
		case strings.HasSuffix(r.URL.Path, "/season/42"):
			w.WriteHeader(http.StatusNotFound)
		default:
			io.WriteString(w, `{"seasons":[{"id":43}],"current_mythic_rating":{"rating":2400},"current_period":{"best_runs":[]}}`)
		}
	})
	yes := true
	c.seasons = map[string]*seasonCatalog{"eu": {Current: &progress.Season{ID: "42", IsCurrent: &yes}, ExpiresAt: time.Now().Add(time.Hour)}}
	actualSection, err := c.fetchMythic(context.Background(), character.Key{Region: "eu", Name: "synthetic", Realm: "synthetic"}, character.Realm{ProviderID: "1", Slug: "synthetic"}, "2", "/profile/wow/character/synthetic/synthetic")
	if err != nil {
		t.Fatal(err)
	}
	var actual progress.MythicPlus
	if err := decode(actualSection.Payload, &actual); err != nil {
		t.Fatal(err)
	}
	if actualSection.State != "partial" || actual.Season != nil || actual.Rating == nil || *actual.Rating != 2400 || actual.RunScope != "current_period_best" || actual.AvailableSeasons[0].IsCurrent != nil {
		t.Fatalf("current period mislabeled using stale catalog: %#v", actual)
	}
}

func TestNormalizeRaidsPreservesMissingVersusZero(t *testing.T) {
	raw := progressRaw(t, `{"expansions":[{"expansion":{"id":1,"name":"Synthetic expansion"},"instances":[{"instance":{"id":2,"name":"Synthetic raid"},"modes":[{"difficulty":{"type":"NORMAL"},"status":{"type":"IN_PROGRESS"},"progress":{"completed_count":1,"total_count":3,"encounters":[{"encounter":{"id":3,"name":"Known kill"},"completed_count":2,"last_kill_timestamp":1790000000000},{"encounter":{"id":4,"name":"Known zero"},"completed_count":0},{"encounter":{"id":5,"name":"Unknown count"}}]}},{"difficulty":{"type":"HEROIC"}}]}]}]}`)
	actual, partial := normalizeRaids(raw)
	if !partial || len(actual.Expansions) != 1 {
		t.Fatalf("expected partial raid: %#v", actual)
	}
	modes := actual.Expansions[0].Instances[0].Modes
	if modes[0].Difficulty != "normal" || modes[0].Status != "in_progress" || modes[0].Completed == nil || *modes[0].Completed != 1 {
		t.Fatalf("mode observation lost %#v", modes[0])
	}
	bosses := modes[0].Encounters
	if bosses[0].CompletedCount == nil || *bosses[0].CompletedCount != 2 || bosses[0].LastKillAt == nil || bosses[1].CompletedCount == nil || *bosses[1].CompletedCount != 0 || bosses[1].LastKillAt != nil || bosses[2].CompletedCount != nil {
		t.Fatalf("unknown and zero conflated: %#v", bosses)
	}
	if modes[1].Encounters != nil || modes[1].Completed != nil || modes[1].Status != "unknown" {
		t.Fatalf("missing progress invented: %#v", modes[1])
	}
	actual, partial = normalizeRaids(progressRaw(t, `{"expansions":[]}`))
	if partial || actual.Expansions == nil || len(actual.Expansions) != 0 {
		t.Fatal("explicit empty raid list lost")
	}
	if actual, _ = normalizeRaids(progressRaw(t, `{}`)); actual != nil {
		t.Fatal("missing raid list invented")
	}
}

func TestNormalizeRaidsRejectsMalformedAndDuplicateIdentities(t *testing.T) {
	raw := progressRaw(t, `{"expansions":[{"expansion":{"id":1},"instances":[{"instance":{"id":2},"modes":[{"difficulty":{"type":"NORMAL"},"progress":{"completed_count":4,"total_count":2,"encounters":[{"encounter":{"id":3},"completed_count":-1},{"encounter":{"id":3}},{"encounter":{"id":0}}]}},{"difficulty":{"type":"NORMAL"}},{"difficulty":{"type":"<script>"}}]},{"instance":{"id":2}},{"instance":{"id":0}}]},{"expansion":{"id":1}},{"expansion":{"id":0}}]}`)
	actual, partial := normalizeRaids(raw)
	if !partial || len(actual.Expansions) != 1 || len(actual.Expansions[0].Instances) != 1 {
		t.Fatalf("duplicate/invalid IDs accepted: %#v", actual)
	}
	modes := actual.Expansions[0].Instances[0].Modes
	if len(modes) != 2 || modes[0].Completed != nil || len(modes[0].Encounters) != 1 || modes[0].Encounters[0].CompletedCount != nil || modes[1].Difficulty != "unknown" {
		t.Fatalf("malformed values accepted %#v", modes)
	}
}

func TestMythicDoesNotGuessCurrentSeasonFromHighestID(t *testing.T) {
	var detailRequests atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenResponse(w)
			return
		}
		if r.URL.Path == "/data/wow/mythic-keystone/season/index" {
			if r.URL.Query().Get("namespace") != "dynamic-eu" {
				t.Error("wrong catalog namespace")
			}
			io.WriteString(w, `{"seasons":[{"id":9999},{"id":42}]}`)
			return
		}
		if strings.Contains(r.URL.Path, "/season/") {
			detailRequests.Add(1)
			t.Error("guessed season detail request")
			w.WriteHeader(404)
			return
		}
		io.WriteString(w, `{"seasons":[{"id":9999},{"id":42}],"current_mythic_rating":{"rating":1400},"current_period":{"best_runs":[]}}`)
	})
	key := character.Key{Region: "eu", Name: "synthetic", Realm: "synthetic"}
	realm := character.Realm{ProviderID: "1", Slug: "synthetic"}
	section, err := c.fetchMythic(context.Background(), key, realm, "2", "/profile/wow/character/synthetic/synthetic")
	if err != nil {
		t.Fatal(err)
	}
	var actual progress.MythicPlus
	if err := decode(section.Payload, &actual); err != nil {
		t.Fatal(err)
	}
	if actual.Season != nil || actual.AvailableSeasons[0].IsCurrent != nil || actual.RunScope != "current_period_best" || detailRequests.Load() != 0 {
		t.Fatalf("season inferred: %#v", actual)
	}
}

func TestCurrentSeasonCatalogIsCoalescedCachedAndRegional(t *testing.T) {
	var catalogs atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenResponse(w)
			return
		}
		catalogs.Add(1)
		io.WriteString(w, `{"current_season":{"id":42,"name":"Synthetic season"},"seasons":[{"id":9999}]}`)
	})
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	c.cfg.Now = func() time.Time { return now }
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			actual, err := c.currentSeason(context.Background(), "eu")
			if err != nil || actual == nil || actual.ID != "42" || actual.IsCurrent == nil || !*actual.IsCurrent {
				t.Errorf("current season lost %#v %v", actual, err)
			}
		}()
	}
	wg.Wait()
	if catalogs.Load() != 1 {
		t.Fatalf("catalog not coalesced: %d", catalogs.Load())
	}
	actual, _ := c.currentSeason(context.Background(), "eu")
	actual.ID = "mutated"
	again, _ := c.currentSeason(context.Background(), "eu")
	if again.ID != "42" {
		t.Fatal("caller mutated cached season")
	}
	c.currentSeason(context.Background(), "us")
	if catalogs.Load() != 2 {
		t.Fatal("regions shared catalog")
	}
	now = now.Add(61 * time.Minute)
	c.currentSeason(context.Background(), "eu")
	if catalogs.Load() != 3 {
		t.Fatal("expired catalog reused")
	}
}

func TestProgressOptionalFailureAndCurrentSeasonIdentity(t *testing.T) {
	for _, scenario := range []string{"not-provided", "season-mismatch", "rate-limited", "partial-raids"} {
		t.Run(scenario, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					tokenResponse(w)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/encounters/raids") {
					if scenario == "partial-raids" {
						w.WriteHeader(503)
					} else {
						io.WriteString(w, `{"expansions":[]}`)
					}
					return
				}
				if strings.HasSuffix(r.URL.Path, "/season/42") {
					switch scenario {
					case "season-mismatch":
						io.WriteString(w, `{"season":{"id":43},"best_runs":[]}`)
					case "rate-limited":
						w.Header().Set("Retry-After", "45")
						w.WriteHeader(429)
					default:
						w.WriteHeader(404)
					}
					return
				}
				io.WriteString(w, `{"current_season":{"id":42},"seasons":[],"current_mythic_rating":{"rating":0},"current_period":{"best_runs":[]}}`)
			})
			actual, err := c.fetchProgress(context.Background(), character.Key{Region: "eu", Name: "synthetic", Realm: "synthetic"}, character.Realm{ProviderID: "1", Slug: "synthetic"}, "2", "/profile/wow/character/synthetic/synthetic")
			if scenario == "rate-limited" {
				var fault *character.Fault
				if !errors.As(err, &fault) || fault.Code != "PROVIDER_RATE_LIMITED" || fault.RetryAt.IsZero() {
					t.Fatalf("rate limit lost %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "season-mismatch" {
				if actual["mythic_plus"].State != "unavailable" || *actual["mythic_plus"].Reason != "invalid_response" {
					t.Fatal("mismatched season published")
				}
				return
			}
			if actual["mythic_plus"].State != "complete" {
				t.Fatalf("current-period data lost on optional detail404: %#v", actual["mythic_plus"])
			}
			if scenario == "partial-raids" && actual["raids"].State != "unavailable" {
				t.Fatal("optional raid error not isolated")
			}
		})
	}
}

func TestProgressIdentityRejectsMismatchedOptionalCharacter(t *testing.T) {
	key := character.Key{Region: "eu", Realm: "synthetic", Name: "synthetic"}
	realm := character.Realm{ProviderID: "1", Slug: "synthetic"}
	for _, body := range []string{`{"character":{"id":3}}`, `{"character":{"id":2,"name":"Other"}}`, `{"character":{"id":2,"realm":{"id":5}}}`, `{"character":{"id":2,"realm":{"id":1,"slug":"other"}}}`, `{"character":null}`} {
		if progressIdentity(progressRaw(t, body), key, realm, "2") {
			t.Fatalf("identity accepted %s", body)
		}
	}
	if !progressIdentity(progressRaw(t, `{"character":{"id":2,"name":"Synthetic","realm":{"id":1,"slug":"synthetic"}}}`), key, realm, "2") {
		t.Fatal("valid optional identity rejected")
	}
}
