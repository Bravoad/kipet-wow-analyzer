package guild

import (
	"encoding/json"
	"reflect"
	"testing"
)

func ptr[T any](v T) *T { return &v }
func testMember(name, id, class string) Member {
	return Member{Key: CharacterKey{Region: "eu", Realm: "гордунни", Name: name}, ProviderID: id, Class: &NamedID{ID: class}}
}
func testObservation(member Member, level float64, status string) Observation {
	profile, _ := json.Marshal(map[string]any{"provider_id": member.ProviderID, "region": "eu", "realm": map[string]any{"slug": "гордунни"}, "name": member.Key.Name, "character_class": map[string]any{"id": "6"}, "active_specialization": map[string]any{"id": "251", "name": "Synthetic spec"}, "equipped_item_level": level})
	return Observation{Member: member, SnapshotID: ptr("snapshot-" + member.ProviderID), Status: status, Profile: profile, Sections: map[string]json.RawMessage{}}
}
func TestCanonicalGuildSlugPreservesProviderUnicode(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"  STRAẞE-GILDE  ", "straße-gilde"}, {"ЛЕДЯНОИ\u0306-ЗАЛ", "ледяной-зал"}, {"kipet-lab", "kipet-lab"}} {
		got, err := NormalizeKey("eu", "гордунни", tc.input)
		if err != nil || got.Slug != tc.want {
			t.Fatalf("%q: %+v %v", tc.input, got, err)
		}
	}
	for _, bad := range []string{"a", "-guild", "guild-", "guild name", "guild/name", "https://example.com", "g%20uild", "g?uild", "g\\uild", "\u0301abc"} {
		if _, err := NormalizeKey("eu", "гордунни", bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, err := NormalizeKey("kr", "realm", "guild"); err == nil {
		t.Fatal("unsupported region")
	}
}
func TestAnalyzeCountsOnlyAuthorizedMatchingProfiles(t *testing.T) {
	a, b, c := testMember("Кипет", "11", "6"), testMember("Ледник", "13", "6"), testMember("Пропавший", "99", "8")
	bundle := Bundle{Profile: Profile{ID: "guild"}, Roster: Roster{Members: []Member{c, b, a}, ReportedCount: ptr(3)}}
	one, two := testObservation(a, 278, "fresh"), testObservation(b, 270, "stale")
	one.Notices = ptr(1)
	two.Notices = ptr(0)
	one.Sections["equipment"] = json.RawMessage(`{"items":[{"enchantments":null,"set_id":null},{"enchantments":[],"set_id":"9001"},{"enchantments":[{"id":"42"}],"set_id":null}]}`)
	two.Sections["equipment"] = json.RawMessage(`{"items":[]}`)
	one.Sections["mythic_plus"] = json.RawMessage(`{"rating":0,"season":{"id":"42"}}`)
	two.Sections["mythic_plus"] = json.RawMessage(`{"rating":1300,"season":null}`)
	one.Sections["raids"] = json.RawMessage(`{"expansions":[]}`)
	missing := Observation{Member: c, Status: "unavailable"}
	before, _ := json.Marshal(bundle)
	got := Analyze(bundle, []Observation{two, missing, one})
	if !got.Coverage.RosterKnown || got.Coverage.Roster != 3 || got.Coverage.Profiles != 2 || got.Coverage.Fresh != 1 || got.Coverage.Stale != 1 || got.Coverage.Unavailable != 1 || got.Coverage.Pending != 0 {
		t.Fatalf("wrong coverage %+v", got.Coverage)
	}
	if got.ItemLevel.Count != 2 || *got.ItemLevel.Mean != 274 || *got.ItemLevel.Median != 274 {
		t.Fatalf("wrong aggregate %+v", got.ItemLevel)
	}
	if got.Gear.KnownEnchantments != 2 || got.Gear.Enchanted != 1 || got.Gear.EmptyEnchantments != 1 || got.Gear.UnknownEnchantments != 1 || got.Gear.KnownSets != 1 {
		t.Fatalf("wrong gear %+v", got.Gear)
	}
	if got.Progress.MythicProfiles != 2 || got.Progress.RaidProfiles != 1 || len(got.Roles) != 1 || got.Roles[0].ID != "unknown" || got.Roles[0].Count != 2 {
		t.Fatalf("invented roles/progress %+v %+v", got.Roles, got.Progress)
	}
	if got.Members[0].Member.Key.Name != "Кипет" || got.Members[0].MythicRating == nil || *got.Members[0].MythicRating != 0 || *got.Members[0].MythicSeason != "42" || got.Members[1].MythicSeason != nil {
		t.Fatal("lost knownzero/season/member order", got.Members)
	}
	if got.Members[0].Notices == nil || *got.Members[0].Notices != 1 || got.Members[1].Notices == nil || *got.Members[1].Notices != 0 || got.Members[2].Notices != nil {
		t.Fatal("unknown checks conflated with zero")
	}
	after, _ := json.Marshal(bundle)
	if string(before) != string(after) {
		t.Fatal("mutated roster")
	}
	if !reflect.DeepEqual(got, Analyze(bundle, []Observation{one, two, missing})) {
		t.Fatal("depends on input observation ordering")
	}
}
func TestAnalyzeIdentityMismatchAndDuplicatesDoNotLeakFacts(t *testing.T) {
	member := testMember("Кипет", "11", "6")
	bundle := Bundle{Roster: Roster{Members: []Member{member}}}
	for _, payload := range []json.RawMessage{json.RawMessage(`{"provider_id":"99","region":"eu","realm":{"slug":"гордунни"},"name":"Кипет","equipped_item_level":999}`), json.RawMessage(`{"provider_id":"11","region":"us","realm":{"slug":"гордунни"},"name":"Кипет","equipped_item_level":999}`), json.RawMessage(`{"provider_id":"11","region":"eu","realm":{"slug":"гордунни"},"name":"Ледник","equipped_item_level":999}`)} {
		obs := testObservation(member, 278, "fresh")
		obs.Profile = payload
		obs.Sections["mythic_plus"] = json.RawMessage(`{"rating":9000}`)
		got := Analyze(bundle, []Observation{obs})
		if got.Coverage.Profiles != 0 || got.Coverage.Unavailable != 1 || got.ItemLevel.Mean != nil || got.Members[0].SnapshotID != nil || got.Members[0].MythicRating != nil {
			t.Fatal("mismatched identity leaked", got)
		}
	}
	obs := testObservation(member, 278, "fresh")
	got := Analyze(bundle, []Observation{obs, obs})
	if got.Coverage.Profiles != 0 || got.Coverage.Unavailable != 1 {
		t.Fatal("duplicate observation selected arbitrarily")
	}
}
func TestAnalyzeUnknownRosterDiffersFromConfirmedEmpty(t *testing.T) {
	bundle := Bundle{Profile: Profile{MemberCount: ptr(3)}}
	unknown := Analyze(bundle, nil)
	if unknown.Coverage.RosterKnown || unknown.Coverage.Reported == nil || *unknown.Coverage.Reported != 3 || unknown.ItemLevel.Median != nil {
		t.Fatal("unknown roster invented", unknown)
	}
	bundle.Roster.Members = []Member{}
	bundle.Roster.ReportedCount = ptr(0)
	empty := Analyze(bundle, nil)
	if !empty.Coverage.RosterKnown || empty.Coverage.Roster != 0 || empty.Coverage.Profiles != 0 || empty.ItemLevel.Mean != nil {
		t.Fatal("known empty lost", empty)
	}
	bundle.Roster.Members = []Member{testMember("Кипет", "11", "6")}
	missing := Analyze(bundle, nil)
	if missing.Coverage.Pending != 1 || missing.Coverage.Unavailable != 0 {
		t.Fatal("missing observation is not pending")
	}
}
