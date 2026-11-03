package analysis

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
)

var testNow = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func ptr[T any](value T) *T { return &value }
func validRule() Ruleset {
	meta := RuleMetadata{Version: "1.0.0", Enabled: true, Reference: "synthetic"}
	return Ruleset{Version: "test-v1", GameFlavor: "retail", Patch: "synthetic-test", ClassID: "6", SpecializationID: "251", Content: GeneralPVE, Synthetic: true, Enabled: true, ReviewStatus: "approved", ReviewedAt: testNow.Add(-time.Hour), ReviewedBy: "Synthetic test specification", References: []Reference{{ID: "synthetic", Title: "Synthetic", URL: "urn:kipet:synthetic:test"}}, Enchant: EnchantRule{RuleMetadata: meta, Slots: []EnchantSlot{{Slot: "head", ItemIDs: []string{"100"}, Applicable: true, CompatibleEnchantmentIDs: []string{"200"}}, {Slot: "neck", ItemIDs: []string{"101"}, Applicable: false, CompatibleEnchantmentIDs: []string{}}}}, SetBonus: SetRule{RuleMetadata: meta, Sets: []SetDefinition{{SetID: "300", ItemIDs: []string{"100", "102"}, Thresholds: []int{1, 2}, ItemCountOnly: true}}}, Talents: TalentRule{RuleMetadata: meta, TreeHash: "synthetic-tree", Baseline: []TalentNode{{NodeID: "400", Rank: 1, Tree: "class"}}, Alternatives: []TalentAlternative{{Baseline: TalentNode{NodeID: "400", Rank: 1, Tree: "class"}, Allowed: []TalentNode{{NodeID: "401", Rank: 1, Tree: "class"}}}}}}
}
func section(value any) character.Section {
	payload, _ := json.Marshal(value)
	return character.Section{State: "complete", FetchedAt: ptr(testNow.Add(-time.Minute)), CheckedAt: testNow, FreshUntil: ptr(testNow.Add(time.Minute)), ValidUntil: ptr(testNow.Add(time.Hour)), Payload: payload}
}
func input() Input {
	return Input{Source: "fixture", Content: GeneralPVE, Now: testNow, Snapshot: character.Snapshot{ID: "snapshot-synthetic", ValidUntil: testNow.Add(time.Hour), Patch: ptr("synthetic-test"), Sections: map[string]character.Section{"profile": section(map[string]any{"character_class": map[string]any{"id": "6"}, "active_specialization": map[string]any{"id": "251"}}), "equipment": section(map[string]any{"items": []any{map[string]any{"slot": "head", "item_id": "100", "enchantments": []any{}, "set_id": "300"}}}), "talents": section(map[string]any{"loadouts": []any{map[string]any{"specialization": map[string]any{"id": "251"}, "is_active": true, "tree_hash": "synthetic-tree", "nodes": []any{map[string]any{"node_id": "400", "entry_id": nil, "rank": 1, "tree": "class"}}}}}), "statistics": section(map[string]any{})}}}
}
func engine(t *testing.T, rule Ruleset) *Engine {
	t.Helper()
	e, err := New([]Ruleset{rule})
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func ruleResults(report Report, id string) []Result {
	results := []Result{}
	for _, result := range report.Results {
		if result.RuleID == id {
			results = append(results, result)
		}
	}
	return results
}
func single(t *testing.T, report Report, id, code string) Result {
	t.Helper()
	results := ruleResults(report, id)
	if len(results) != 1 || results[0].ReasonCode != code {
		t.Fatalf("%s wanted %s, got %+v", id, code, results)
	}
	return results[0]
}
func withGear(in Input, items any) Input {
	in.Snapshot.Sections["equipment"] = section(map[string]any{"items": items})
	return in
}
func withTalents(in Input, loadouts any) Input {
	in.Snapshot.Sections["talents"] = section(map[string]any{"loadouts": loadouts})
	return in
}
func talentNodes(nodes any, active any, hash any, spec string) map[string]any {
	return map[string]any{"specialization": map[string]any{"id": spec}, "is_active": active, "tree_hash": hash, "nodes": nodes}
}
func node(id string, rank int) map[string]any {
	return map[string]any{"node_id": id, "entry_id": nil, "rank": rank, "tree": "class"}
}

func TestScopeAndUnknownAreFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Input, *Ruleset)
	}{
		{"missing patch", func(i *Input, r *Ruleset) { i.Snapshot.Patch = nil }},
		{"different patch", func(i *Input, r *Ruleset) { i.Snapshot.Patch = ptr("synthetic-next") }},
		{"different specialization", func(i *Input, r *Ruleset) { r.SpecializationID = "252" }},
		{"different class", func(i *Input, r *Ruleset) { r.ClassID = "7" }},
		{"different content", func(i *Input, r *Ruleset) { i.Content = Raid }},
		{"invalid content", func(i *Input, r *Ruleset) { i.Content = "unsupported" }},
		{"synthetic forbidden for Blizzard", func(i *Input, r *Ruleset) { i.Source = "blizzard" }},
		{"unknown source", func(i *Input, r *Ruleset) { i.Source = "invalid" }},
		{"disabled ruleset", func(i *Input, r *Ruleset) { r.Enabled = false }},
		{"missing season", func(i *Input, r *Ruleset) { r.Season = ptr("10") }},
		{"different season", func(i *Input, r *Ruleset) { r.Season = ptr("10"); i.Snapshot.Season = ptr("11") }},
		{"expired snapshot", func(i *Input, r *Ruleset) { i.Snapshot.ValidUntil = testNow }},
		{"expired profile", func(i *Input, r *Ruleset) {
			p := i.Snapshot.Sections["profile"]
			p.ValidUntil = ptr(testNow)
			i.Snapshot.Sections["profile"] = p
		}},
		{"invalid profile JSON", func(i *Input, r *Ruleset) {
			p := i.Snapshot.Sections["profile"]
			p.Payload = json.RawMessage(`{"broken"`)
			i.Snapshot.Sections["profile"] = p
		}},
		{"missing specialization", func(i *Input, r *Ruleset) {
			i.Snapshot.Sections["profile"] = section(map[string]any{"character_class": map[string]any{"id": "6"}, "active_specialization": nil})
		}},
		{"invalid class ID", func(i *Input, r *Ruleset) {
			i.Snapshot.Sections["profile"] = section(map[string]any{"character_class": map[string]any{"id": "0"}, "active_specialization": map[string]any{"id": "251"}})
		}},
		{"no clock", func(i *Input, r *Ruleset) { i.Now = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			in := input()
			rule := validRule()
			test.change(&in, &rule)
			report := engine(t, rule).Analyze(in)
			if report.Ruleset != nil || report.Summary.Unknown != 3 || report.Summary.Warning != 0 {
				t.Fatalf("unsafe scope result %+v", report)
			}
			for _, result := range report.Results {
				if len(result.Evidence) > 0 {
					t.Fatal("unavailable context leaked evidence")
				}
			}
		})
	}
	empty, _ := New(nil)
	report := empty.Analyze(input())
	if report.Summary.Unknown != 3 {
		t.Fatal(report)
	}
	rule := validRule()
	rule.Season = ptr("10")
	in := input()
	in.Snapshot.Season = ptr("10")
	if engine(t, rule).Analyze(in).Ruleset == nil {
		t.Fatal("matching season rejected")
	}
	rule = validRule()
	rule.Synthetic = false
	rule.Patch = "actual-test-context"
	rule.References[0].URL = "https://example.org/reviewed-test"
	in = input()
	in.Source = "blizzard"
	in.Snapshot.Patch = ptr(rule.Patch)
	if engine(t, rule).Analyze(in).Ruleset == nil {
		t.Fatal("non-synthetic reviewed context rejected")
	}
}

func TestEnchantEvidenceAndTriState(t *testing.T) {
	tests := []struct {
		name    string
		items   any
		code    string
		outcome string
	}{
		{"confirmed missing", []any{map[string]any{"slot": "head", "item_id": "100", "enchantments": []any{}}}, "ENCHANT_MISSING", "Warning"},
		{"unknown field", []any{map[string]any{"slot": "head", "item_id": "100", "enchantments": nil}}, "ENCHANT_DATA_INCOMPLETE", "Unknown"},
		{"compatible present", []any{map[string]any{"slot": "head", "item_id": "100", "enchantments": []any{map[string]any{"id": "200"}}}}, "ENCHANT_PRESENT", "OK"},
		{"incompatible present", []any{map[string]any{"slot": "head", "item_id": "100", "enchantments": []any{map[string]any{"id": "201"}}}}, "ENCHANT_MISSING", "Warning"},
		{"invalid enchant", []any{map[string]any{"slot": "head", "item_id": "100", "enchantments": []any{map[string]any{"id": "0"}}}}, "ENCHANT_DATA_INCOMPLETE", "Unknown"},
		{"explicitly excluded", []any{map[string]any{"slot": "neck", "item_id": "101", "enchantments": nil}}, "ENCHANT_NOT_APPLICABLE", ""},
		{"unknown item", []any{map[string]any{"slot": "head", "item_id": "999", "enchantments": []any{}}}, "ENCHANT_APPLICABILITY_UNKNOWN", "Unknown"},
		{"empty equipment", []any{}, "ENCHANT_NOT_APPLICABLE", ""},
		{"unknown equipment", nil, "ENCHANT_DATA_INCOMPLETE", "Unknown"},
		{"invalid item", []any{map[string]any{"slot": "head", "item_id": "0"}}, "ENCHANT_DATA_INCOMPLETE", "Unknown"},
		{"duplicate slot", []any{map[string]any{"slot": "head", "item_id": "100"}, map[string]any{"slot": "head", "item_id": "101"}}, "ENCHANT_DATA_INCOMPLETE", "Unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := engine(t, validRule()).Analyze(withGear(input(), test.items))
			result := single(t, report, "GEAR_MISSING_ENCHANT", test.code)
			if test.outcome == "" {
				if result.Outcome != nil || result.Applicability != "not_applicable" {
					t.Fatal(result)
				}
			} else if result.Outcome == nil || *result.Outcome != test.outcome {
				t.Fatal(result)
			}
			if test.code == "ENCHANT_MISSING" {
				if len(result.Evidence) != 1 || result.Evidence[0].Path != "/items/0" || result.Evidence[0].Reference == nil || result.Evidence[0].FetchedAt == nil {
					t.Fatal("missing proof")
				}
			}
		})
	}
	rule := validRule()
	rule.Enchant.Enabled = false
	single(t, engine(t, rule).Analyze(input()), "GEAR_MISSING_ENCHANT", "ENCHANT_RULE_DISABLED")
	in := input()
	in.Snapshot.Sections["equipment"] = character.Unavailable("provider_unavailable", testNow)
	single(t, engine(t, validRule()).Analyze(in), "GEAR_MISSING_ENCHANT", "ENCHANT_DATA_INCOMPLETE")
	in = input()
	section := in.Snapshot.Sections["equipment"]
	section.Payload = json.RawMessage(`{`)
	in.Snapshot.Sections["equipment"] = section
	single(t, engine(t, validRule()).Analyze(in), "GEAR_MISSING_ENCHANT", "ENCHANT_DATA_INCOMPLETE")
}

func TestSetBonusRequiresReviewedActivationAndCompleteGoal(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Input, *Ruleset)
		code   string
	}{
		{"observed only", func(i *Input, r *Ruleset) {}, "SET_BONUS_OBSERVED"},
		{"rule disabled", func(i *Input, r *Ruleset) { r.SetBonus.Enabled = false }, "SET_RULE_DISABLED"},
		{"scope excluded", func(i *Input, r *Ruleset) { r.SetBonus.Sets = nil }, "SET_CONTEXT_EXCLUDED"},
		{"activation unknown", func(i *Input, r *Ruleset) { r.SetBonus.Sets[0].ItemCountOnly = false }, "SET_DATA_INCOMPLETE"},
		{"set membership unknown", func(i *Input, r *Ruleset) {
			*i = withGear(*i, []any{map[string]any{"slot": "head", "item_id": "100", "set_id": nil}})
		}, "SET_DATA_INCOMPLETE"},
		{"set membership wrong", func(i *Input, r *Ruleset) {
			*i = withGear(*i, []any{map[string]any{"slot": "head", "item_id": "100", "set_id": "301"}})
		}, "SET_DATA_INCOMPLETE"},
		{"duplicate piece", func(i *Input, r *Ruleset) {
			*i = withGear(*i, []any{map[string]any{"slot": "head", "item_id": "100", "set_id": "300"}, map[string]any{"slot": "back", "item_id": "100", "set_id": "300"}})
		}, "SET_DATA_INCOMPLETE"},
		{"explicit goal met", func(i *Input, r *Ruleset) { r.SetBonus.Sets[0].TargetPieces = ptr(1) }, "SET_TARGET_MET"},
		{"explicit goal missed", func(i *Input, r *Ruleset) { r.SetBonus.Sets[0].TargetPieces = ptr(2) }, "SET_TARGET_NOT_MET"},
		{"partial cannot prove missed goal", func(i *Input, r *Ruleset) {
			r.SetBonus.Sets[0].TargetPieces = ptr(2)
			s := i.Snapshot.Sections["equipment"]
			s.State = "partial"
			s.Reason = ptr("not_provided")
			i.Snapshot.Sections["equipment"] = s
		}, "SET_DATA_INCOMPLETE"},
		{"expired equipment", func(i *Input, r *Ruleset) {
			s := i.Snapshot.Sections["equipment"]
			s.ValidUntil = ptr(testNow)
			i.Snapshot.Sections["equipment"] = s
		}, "SET_DATA_INCOMPLETE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			in := input()
			rule := validRule()
			test.change(&in, &rule)
			single(t, engine(t, rule).Analyze(in), "GEAR_SET_BONUS", test.code)
		})
	}
}

func TestTalentBaselineAlternativesAndMissingData(t *testing.T) {
	tests := []struct {
		name     string
		loadouts any
		code     string
	}{
		{"match", []any{talentNodes([]any{node("400", 1)}, true, "synthetic-tree", "251")}, "TALENT_MATCH"},
		{"allowed alternative", []any{talentNodes([]any{node("401", 1)}, true, "synthetic-tree", "251")}, "TALENT_MATCH"},
		{"difference only", []any{talentNodes([]any{node("402", 1)}, true, "synthetic-tree", "251")}, "TALENT_DIFFERENT"},
		{"confirmed empty build", []any{talentNodes([]any{}, true, "synthetic-tree", "251")}, "TALENT_DIFFERENT"},
		{"nodes unknown", []any{talentNodes(nil, true, "synthetic-tree", "251")}, "TALENT_DATA_INCOMPLETE"},
		{"activity unknown", []any{talentNodes([]any{node("400", 1)}, nil, "synthetic-tree", "251")}, "TALENT_DATA_INCOMPLETE"},
		{"inactive only", []any{talentNodes([]any{node("400", 1)}, false, "synthetic-tree", "251")}, "TALENT_CONTEXT_UNKNOWN"},
		{"tree unknown", []any{talentNodes([]any{node("400", 1)}, true, nil, "251")}, "TALENT_CONTEXT_UNKNOWN"},
		{"tree changed", []any{talentNodes([]any{node("400", 1)}, true, "synthetic-next", "251")}, "TALENT_CONTEXT_UNKNOWN"},
		{"specialization changed", []any{talentNodes([]any{node("400", 1)}, true, "synthetic-tree", "252")}, "TALENT_CONTEXT_UNKNOWN"},
		{"two active loadouts", []any{talentNodes([]any{node("400", 1)}, true, "synthetic-tree", "251"), talentNodes([]any{node("400", 1)}, true, "synthetic-tree", "251")}, "TALENT_DATA_INCOMPLETE"},
		{"invalid rank", []any{talentNodes([]any{node("400", 0)}, true, "synthetic-tree", "251")}, "TALENT_DATA_INCOMPLETE"},
		{"unknown loadouts", nil, "TALENT_DATA_INCOMPLETE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			single(t, engine(t, validRule()).Analyze(withTalents(input(), test.loadouts)), "TALENT_BUILD_DIFF", test.code)
		})
	}
	rule := validRule()
	rule.Talents.Required = []TalentNode{{NodeID: "400", Rank: 1, Tree: "class"}}
	report := engine(t, rule).Analyze(withTalents(input(), []any{talentNodes([]any{node("402", 1)}, true, "synthetic-tree", "251")}))
	single(t, report, "TALENT_BUILD_DIFF", "TALENT_REQUIRED_MISSING")
	rule = validRule()
	rule.Talents.Enabled = false
	single(t, engine(t, rule).Analyze(input()), "TALENT_BUILD_DIFF", "TALENT_RULE_DISABLED")
	// This guard also protects callers of the evaluator from incomplete manually assembled catalogs.
	rule = validRule()
	rule.Talents.Baseline = nil
	result := evaluateTalents(input().Snapshot.Sections["talents"], rule, testNow)
	if result.ReasonCode != "TALENT_BASELINE_UNAVAILABLE" {
		t.Fatal(result)
	}
}

func TestDiagnosticsDigestAndImmutability(t *testing.T) {
	rule := validRule()
	e := engine(t, rule)
	in := input()
	copyBefore, _ := json.Marshal(in.Snapshot)
	p := in.Snapshot.Sections["equipment"]
	p.State = "partial"
	p.Reason = ptr("not_provided")
	p.FreshUntil = ptr(testNow.Add(-time.Second))
	in.Snapshot.Sections["equipment"] = p
	report := e.Analyze(in)
	again := e.Analyze(in)
	if !reflect.DeepEqual(report, again) {
		t.Fatal("identical explicit inputs changed output")
	}
	codes := map[string]bool{}
	for _, d := range report.Diagnostics {
		codes[d.Code] = true
	}
	if !codes["DATA_STALE"] || !codes["DATA_SECTION_PARTIAL"] {
		t.Fatal(report.Diagnostics)
	}
	baseline := input()
	_ = e.Analyze(baseline)
	copyAfter, _ := json.Marshal(baseline.Snapshot)
	if !bytes.Equal(copyBefore, copyAfter) {
		t.Fatal("analysis mutated snapshot")
	}
	originalDigest := e.CatalogDigest()
	rule.Enchant.Slots[0].CompatibleEnchantmentIDs[0] = "999"
	if e.CatalogDigest() != originalDigest || single(t, e.Analyze(input()), "GEAR_MISSING_ENCHANT", "ENCHANT_MISSING").Evidence[0].Reference == nil {
		t.Fatal("catalog mutated through caller")
	}
	changed := engine(t, rule)
	if changed.CatalogDigest() == originalDigest {
		t.Fatal("catalog content absent from digest")
	}
	a, b := validRule(), validRule()
	b.Patch = "synthetic-other"
	left, _ := New([]Ruleset{a, b})
	right, _ := New([]Ruleset{b, a})
	if left.CatalogDigest() != right.CatalogDigest() {
		t.Fatal("catalog ordering affects digest")
	}
	encoded, _ := json.Marshal(report)
	if bytes.Contains(encoded, []byte(`"score"`)) {
		t.Fatal("universal player score introduced")
	}
	if reference(validRule(), "unknown") != nil {
		t.Fatal("unknown provenance linked")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("synthetic read failure") }
func TestStrictRulesetLoading(t *testing.T) {
	valid, _ := json.Marshal(validRule())
	decoded, err := DecodeRuleset(bytes.NewReader(valid))
	if err != nil || decoded.Version != "test-v1" {
		t.Fatal(decoded, err)
	}
	invalid := [][]byte{append(append([]byte{}, valid...), []byte(` {}`)...), []byte(`{"version":"a","version":"b"}`), []byte(`{"unknown":true}`), []byte(`{"enchant":{"unknown":true}}`), []byte(`{`), []byte(`[]`), []byte(`null`), []byte(strings.Repeat(" ", maxRulesetBytes+1)), []byte(strings.Repeat("[", 66) + strings.Repeat("]", 66))}
	for _, data := range invalid {
		if _, err := DecodeRuleset(bytes.NewReader(data)); err == nil {
			t.Fatalf("accepted invalid config %.80s", data)
		}
	}
	if _, err := DecodeRuleset(errorReader{}); err == nil {
		t.Fatal("I/O failure ignored")
	}
	if err := uniqueJSONKeys(json.NewDecoder(strings.NewReader(`}`)), 0); err == nil {
		t.Fatal("invalid delimiter accepted")
	}
	if _, err := DecodeRuleset(io.LimitReader(strings.NewReader(`{"version":`), 11)); err == nil {
		t.Fatal("truncated input accepted")
	}
}

func TestRulesetValidationAndAmbiguity(t *testing.T) {
	tests := []func(*Ruleset){func(r *Ruleset) { r.Version = "" }, func(r *Ruleset) { r.Season = ptr("0") }, func(r *Ruleset) { r.Patch = "real-looking" }, func(r *Ruleset) { r.ReviewStatus = "unreviewed" }, func(r *Ruleset) { r.ReviewStatus = "draft" }, func(r *Ruleset) { r.ReviewedAt = time.Time{} }, func(r *Ruleset) { r.ReviewedBy = "" }, func(r *Ruleset) { r.References = nil }, func(r *Ruleset) { r.References = append(r.References, r.References[0]) }, func(r *Ruleset) { r.References[0].URL = "http://example.org/insecure" }, func(r *Ruleset) { r.References[0].URL = "https://user:password@example.org/" }, func(r *Ruleset) { r.Enchant.Version = "" }, func(r *Ruleset) { r.Enchant.Reference = "missing" }, func(r *Ruleset) { r.Enchant.Slots = append(r.Enchant.Slots, r.Enchant.Slots[0]) }, func(r *Ruleset) { r.Enchant.Slots[0].ItemIDs = nil }, func(r *Ruleset) { r.Enchant.Slots[0].ItemIDs = []string{"100", "100"} }, func(r *Ruleset) { r.Enchant.Slots[0].CompatibleEnchantmentIDs = nil }, func(r *Ruleset) { r.Enchant.Slots[1].CompatibleEnchantmentIDs = []string{"200"} }, func(r *Ruleset) { r.SetBonus.Sets[0].SetID = "0" }, func(r *Ruleset) { r.SetBonus.Sets[0].Thresholds = nil }, func(r *Ruleset) { r.SetBonus.Sets[0].Thresholds = []int{2, 1} }, func(r *Ruleset) { r.SetBonus.Sets[0].Thresholds = []int{3} }, func(r *Ruleset) { r.SetBonus.Sets[0].TargetPieces = ptr(3) }, func(r *Ruleset) { r.Talents.TreeHash = "" }, func(r *Ruleset) { r.Talents.Baseline = nil }, func(r *Ruleset) { r.Talents.Baseline[0].NodeID = "0" }, func(r *Ruleset) {
		r.Talents.Required = []TalentNode{{NodeID: "1", EntryID: ptr("0"), Rank: 1, Tree: "class"}}
	}, func(r *Ruleset) { r.Talents.Alternatives[0].Baseline.NodeID = "999" }, func(r *Ruleset) { r.Talents.Alternatives[0].Allowed = nil }, func(r *Ruleset) { r.Talents.Alternatives = append(r.Talents.Alternatives, r.Talents.Alternatives[0]) }}
	for i, change := range tests {
		rule := validRule()
		change(&rule)
		if _, err := New([]Ruleset{rule}); err == nil {
			t.Fatalf("invalid rule variant %d accepted", i)
		}
	}
	rule := validRule()
	if _, err := New([]Ruleset{rule, rule}); err == nil {
		t.Fatal("duplicate context accepted")
	}
	other := validRule()
	other.Version = "new-version"
	other.Season = ptr("10")
	if _, err := New([]Ruleset{rule, other}); err == nil {
		t.Fatal("season wildcard overlap accepted")
	}
	other = validRule()
	other.Enabled = false
	other.Version = "archive"
	if _, err := New([]Ruleset{rule, other}); err != nil {
		t.Fatal("disabled historical version rejected", err)
	}
	rule = validRule()
	rule.Enabled = false
	rule.ReviewStatus = "draft"
	rule.References = nil
	rule.Enchant.Enabled = false
	rule.SetBonus.Enabled = false
	rule.Talents.Enabled = false
	if _, err := New([]Ruleset{rule}); err != nil {
		t.Fatal("inactive draft rejected", err)
	}
	for _, id := range []string{"", "0", "01", "-1", "+1", "1.0", "9223372036854775808"} {
		if positiveID(id) {
			t.Fatalf("invalid ID %q accepted", id)
		}
	}
	if !positiveID("9223372036854775807") {
		t.Fatal("int64 boundary lost")
	}
}

func TestPartialTalentsCannotProveMatchOrMissingConstraint(t *testing.T) {
	rule := validRule()
	hero := TalentNode{NodeID: "500", Rank: 1, Tree: "hero"}
	rule.Talents.Baseline = append(rule.Talents.Baseline, hero)
	rule.Talents.Required = []TalentNode{hero}
	in := input()
	talents := in.Snapshot.Sections["talents"]
	talents.State = "partial"
	talents.Reason = ptr("not_provided")
	in.Snapshot.Sections["talents"] = talents
	result := single(t, engine(t, rule).Analyze(in), "TALENT_BUILD_DIFF", "TALENT_DATA_INCOMPLETE")
	if result.Outcome == nil || *result.Outcome != "Unknown" {
		t.Fatal("partial trees produced a player judgment")
	}
	rule = validRule()
	single(t, engine(t, rule).Analyze(in), "TALENT_BUILD_DIFF", "TALENT_DATA_INCOMPLETE")
}

func TestAlternativeGroupsCannotCollapseTwoBaselineRequirements(t *testing.T) {
	rule := validRule()
	second := TalentNode{NodeID: "500", Rank: 1, Tree: "class"}
	rule.Talents.Baseline = append(rule.Talents.Baseline, second)
	rule.Talents.Alternatives = append(rule.Talents.Alternatives, TalentAlternative{Baseline: second, Allowed: []TalentNode{{NodeID: "401", Rank: 1, Tree: "class"}}})
	if _, err := New([]Ruleset{rule}); err == nil {
		t.Fatal("shared replacement can incorrectly satisfy two baseline nodes")
	}
	rule = validRule()
	rule.Talents.Baseline = append(rule.Talents.Baseline, second)
	rule.Talents.Alternatives[0].Allowed = []TalentNode{second}
	if _, err := New([]Ruleset{rule}); err == nil {
		t.Fatal("another baseline node cannot replace an independent requirement")
	}
	// Different choices of the same node are valid alternatives within one group.
	rule = validRule()
	rule.Talents.Alternatives[0].Allowed = []TalentNode{{NodeID: "400", EntryID: ptr("1"), Rank: 1, Tree: "class"}, {NodeID: "400", EntryID: ptr("2"), Rank: 1, Tree: "class"}}
	e := engine(t, rule)
	choice := node("400", 1)
	choice["entry_id"] = "2"
	single(t, e.Analyze(withTalents(input(), []any{talentNodes([]any{choice}, true, "synthetic-tree", "251")})), "TALENT_BUILD_DIFF", "TALENT_MATCH")
	rule.Talents.Alternatives[0].Allowed = append(rule.Talents.Alternatives[0].Allowed, rule.Talents.Alternatives[0].Allowed[0])
	if _, err := New([]Ruleset{rule}); err == nil {
		t.Fatal("duplicate replacement choice accepted")
	}
	rule = validRule()
	rule.Talents.Alternatives[0].Allowed[0].Rank = 0
	if _, err := New([]Ruleset{rule}); err == nil {
		t.Fatal("invalid replacement node accepted")
	}
}

func TestUnknownSetCatalogMemberCannotCreateFalseMissingGoal(t *testing.T) {
	rule := validRule()
	rule.SetBonus.Sets[0].TargetPieces = ptr(2)
	in := withGear(input(), []any{map[string]any{"slot": "head", "item_id": "100", "set_id": "300"}, map[string]any{"slot": "back", "item_id": "999", "set_id": "300"}})
	single(t, engine(t, rule).Analyze(in), "GEAR_SET_BONUS", "SET_DATA_INCOMPLETE")
}

func TestFutureReviewIsNotActiveAndReturnedTargetCannotMutateCatalog(t *testing.T) {
	rule := validRule()
	rule.ReviewedAt = testNow.Add(time.Hour)
	if engine(t, rule).Analyze(input()).Ruleset != nil {
		t.Fatal("future review treated as already approved")
	}
	rule = validRule()
	rule.SetBonus.Sets[0].TargetPieces = ptr(2)
	e := engine(t, rule)
	report := e.Analyze(input())
	result := single(t, report, "GEAR_SET_BONUS", "SET_TARGET_NOT_MET")
	if _, unsafe := result.Params["target"].(*int); unsafe {
		t.Fatal("report exposes mutable catalog pointer")
	}
	result.Params["target"] = 1
	single(t, e.Analyze(input()), "GEAR_SET_BONUS", "SET_TARGET_NOT_MET")
}
