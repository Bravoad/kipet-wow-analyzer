package blizzard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/guild"
)

const syntheticGuildProfile = `{"id":900007001,"name":"Synthetic Guild","slug":"kipet-lab","realm":{"id":1,"slug":"synthetic"},"faction":{"type":"ALLIANCE"},"member_count":3,"created_timestamp":1790000000000}`

func syntheticGuildKey() (guild.Key, character.Realm) {
	return guild.Key{Region: "eu", Realm: "synthetic", Slug: "kipet-lab"}, character.Realm{Region: "eu", Slug: "synthetic", ProviderID: "1", Names: map[string]string{"en-US": "Synthetic Realm"}}
}
func rawGuild(t *testing.T, value string) object {
	t.Helper()
	var raw object
	if err := decode([]byte(value), &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestGuildNormalizationPreservesIDsAndUnknownVersusZero(t *testing.T) {
	key, realm := syntheticGuildKey()
	profile, err := normalizeGuildProfile(rawGuild(t, syntheticGuildProfile), key, realm)
	if err != nil {
		t.Fatal(err)
	}
	if profile.ProviderID != "900007001" || profile.MemberCount == nil || *profile.MemberCount != 3 || profile.CreatedAt == nil || profile.Faction != "alliance" {
		t.Fatalf("profile=%+v", profile)
	}
	for _, tc := range []struct {
		body  string
		known bool
	}{{`{}`, false}, {`{"members":null}`, false}, {`{"members":[]}`, true}} {
		r, err := normalizeGuildRoster(rawGuild(t, tc.body), key, realm, profile.ProviderID)
		if err != nil || ((r.Members != nil) != tc.known) {
			t.Fatalf("%s: %+v %v", tc.body, r, err)
		}
	}
	roster, err := normalizeGuildRoster(rawGuild(t, `{"guild":{"id":900007001,"realm":{"id":1,"slug":"synthetic"}},"member_count":1,"members":[{"character":{"id":9007199254740993,"name":"Пример","realm":{"id":1,"slug":"synthetic"},"playable_class":{"id":6},"level":80},"rank":0}]}`), key, realm, profile.ProviderID)
	if err != nil || len(roster.Members) != 1 {
		t.Fatal(roster, err)
	}
	member := roster.Members[0]
	if member.ProviderID != "9007199254740993" || member.Key.Name != "Пример" || member.Rank == nil || *member.Rank != 0 || member.Class == nil || member.Class.ID != "6" || member.Class.Name != nil {
		t.Fatal("IDs/null/zero changed", member)
	}
}
func TestGuildRosterRejectsIdentityConflictsAndDuplicates(t *testing.T) {
	key, realm := syntheticGuildKey()
	for _, body := range []string{
		`{"guild":{"id":999},"members":[]}`,
		`{"guild":{"id":900007001,"slug":"wrong-guild"},"members":[]}`,
		`{"guild":{"id":900007001,"realm":{"id":2}},"members":[]}`,
		`{"members":"not-a-list"}`,
		`{"members":[{"character":{"id":1,"name":"Example","realm":{"id":1,"slug":"other"}}}]}`,
		`{"members":[{"character":{"id":1,"name":"Example","realm":{"id":2,"slug":"synthetic"}}}]}`,
		`{"members":[{"character":{"id":1,"name":"bad/name","realm":{"id":1,"slug":"synthetic"}}}]}`,
		`{"members":[{"character":{"id":1,"name":"Example","realm":{"id":1,"slug":"synthetic"}}},{"character":{"id":2,"name":"EXAMPLE","realm":{"id":1,"slug":"synthetic"}}}]}`,
		`{"members":[{"character":{"id":1,"name":"Example","realm":{"id":1,"slug":"synthetic"}}},{"character":{"id":1,"name":"Other","realm":{"id":1,"slug":"synthetic"}}}]}`,
	} {
		if _, err := normalizeGuildRoster(rawGuild(t, body), key, realm, "900007001"); character.AsFault(err).Code != "PROVIDER_INVALID_RESPONSE" {
			t.Fatalf("accepted %s: %v", body, err)
		}
	}
	oversize := `{"members":[` + strings.Repeat(`{},`, 1000) + `{}]}`
	if _, err := normalizeGuildRoster(rawGuild(t, oversize), key, realm, "900007001"); err == nil {
		t.Fatal("oversize roster accepted")
	}
	for _, body := range []string{`{"id":1,"name":"Wrong","realm":{"id":2}}`, `{"id":1,"name":"Wrong","realm":{"id":1},"slug":"other-guild"}`, `{"id":1,"name":"Wrong","realm":{"id":1},"member_count":1001}`} {
		if _, err := normalizeGuildProfile(rawGuild(t, body), key, realm); err == nil {
			t.Fatalf("accepted identity %s", body)
		}
	}
}
func TestFetchGuildUsesProfileNamespaceAndPreservesOptionalRoster(t *testing.T) {
	for _, body := range []string{`{"guild":{"id":900007001},"members":[]}`, `{}`} {
		t.Run(body, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					tokenResponse(w)
					return
				}
				if r.URL.Query().Get("namespace") != "profile-eu" {
					t.Error("wrong namespace")
				}
				switch r.URL.Path {
				case "/data/wow/guild/synthetic/kipet-lab":
					io.WriteString(w, syntheticGuildProfile)
				case "/data/wow/guild/synthetic/kipet-lab/roster":
					io.WriteString(w, body)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			})
			key, realm := syntheticGuildKey()
			result, err := c.FetchGuild(context.Background(), key, realm)
			if err != nil {
				t.Fatal(err)
			}
			if result.Profile.ProviderID != "900007001" || result.Roster.ReportedCount == nil || *result.Roster.ReportedCount != 3 {
				t.Fatal(result)
			}
			if strings.Contains(body, `[]`) && result.Roster.Members == nil {
				t.Fatal("explicit [] lost")
			}
			if body == `{}` && result.Roster.Members != nil {
				t.Fatal("missing roster invented")
			}
		})
	}
}
func TestFetchGuildMapsMissingAndDeniedAndRateLimitedSafely(t *testing.T) {
	for _, tc := range []struct {
		summary, roster int
		code            string
		optional        bool
	}{{404, 200, "GUILD_UNAVAILABLE", false}, {403, 200, "PROVIDER_ACCESS_DENIED", false}, {200, 404, "", true}, {200, 403, "PROVIDER_ACCESS_DENIED", false}, {200, 429, "PROVIDER_RATE_LIMITED", false}, {200, 500, "PROVIDER_UNAVAILABLE", false}} {
		t.Run(fmt.Sprintf("%d-%d", tc.summary, tc.roster), func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					tokenResponse(w)
					return
				}
				status := tc.summary
				if strings.HasSuffix(r.URL.Path, "/roster") {
					status = tc.roster
				}
				if status != 200 {
					w.Header().Set("Retry-After", "120")
					w.WriteHeader(status)
					io.WriteString(w, `sensitive-provider-detail`)
					return
				}
				io.WriteString(w, syntheticGuildProfile)
			})
			key, realm := syntheticGuildKey()
			result, err := c.FetchGuild(context.Background(), key, realm)
			if tc.optional {
				if err != nil || result.Roster.Members != nil || result.Profile.ProviderID == "" {
					t.Fatal(result, err)
				}
				return
			}
			if err == nil || character.AsFault(err).Code != tc.code || strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("%v", err)
			}
			if tc.roster == 429 && time.Until(character.AsFault(err).RetryAt) < 119*time.Second {
				t.Fatal("Retry-After shortened")
			}
		})
	}
}
func TestFetchGuildRejectsUnsafeInputBeforeNetwork(t *testing.T) {
	var requests atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) })
	key, realm := syntheticGuildKey()
	key.Slug = "https://elsewhere.invalid/secret"
	if _, err := c.FetchGuild(context.Background(), key, realm); character.AsFault(err).Code != "INVALID_ARGUMENT" || requests.Load() != 0 {
		t.Fatal("unsafe input reached network", err)
	}
	_, _ = json.Marshal(key) // Key serialization must not introduce a URL-based transport option.
}
