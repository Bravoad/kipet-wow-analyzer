package analysis

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
)

type profileData struct {
	Class struct {
		ID string `json:"id"`
	} `json:"character_class"`
	Specialization *struct {
		ID string `json:"id"`
	} `json:"active_specialization"`
}
type equipmentData struct {
	Items []equipmentItem `json:"items"`
}
type equipmentItem struct {
	Slot         string `json:"slot"`
	ItemID       string `json:"item_id"`
	Enchantments []struct {
		ID string `json:"id"`
	} `json:"enchantments"`
	SetID *string `json:"set_id"`
}
type talentsData struct {
	Loadouts []loadout `json:"loadouts"`
}
type loadout struct {
	Specialization struct {
		ID string `json:"id"`
	} `json:"specialization"`
	Active   *bool        `json:"is_active"`
	TreeHash *string      `json:"tree_hash"`
	Nodes    []TalentNode `json:"nodes"`
}

// Analyze is deterministic for the explicit snapshot, clock, source and catalog.
// It never refreshes data, fetches references, or changes the input snapshot.
func (e *Engine) Analyze(input Input) Report {
	now := input.Now.UTC()
	report := Report{SnapshotID: input.Snapshot.ID, Source: input.Source, EvaluatedAt: now, Context: Context{Content: input.Content}, Results: []Result{}, Diagnostics: []Diagnostic{}}
	profile := input.Snapshot.Sections["profile"]
	var identity profileData
	usableProfile := !now.IsZero() && now.Before(input.Snapshot.ValidUntil) && profile.State == "complete" && profile.Publishable(now) && json.Unmarshal(profile.Payload, &identity) == nil
	if usableProfile {
		report.Context.Patch = input.Snapshot.Patch
		report.Context.Season = input.Snapshot.Season
		if positiveID(identity.Class.ID) {
			report.Context.ClassID = &identity.Class.ID
		}
		if identity.Specialization != nil && positiveID(identity.Specialization.ID) {
			report.Context.SpecializationID = &identity.Specialization.ID
		}
	} else {
		report.Diagnostics = append(report.Diagnostics, diagnostic("DATA_PROFILE_UNAVAILABLE", "profile", map[string]any{}))
	}
	for _, name := range character.Sections {
		section := input.Snapshot.Sections[name]
		if !usableProfile || !section.Publishable(now) {
			report.Diagnostics = append(report.Diagnostics, diagnostic("DATA_SECTION_UNAVAILABLE", name, map[string]any{}))
			continue
		}
		if section.State == "partial" {
			report.Diagnostics = append(report.Diagnostics, diagnostic("DATA_SECTION_PARTIAL", name, map[string]any{}))
		}
		if !section.Fresh(now) {
			report.Diagnostics = append(report.Diagnostics, diagnostic("DATA_STALE", name, map[string]any{}))
		}
	}
	contextKnown := report.Context.Patch != nil && *report.Context.Patch != "" && report.Context.ClassID != nil && report.Context.SpecializationID != nil && ValidContent(input.Content)
	if !contextKnown {
		report.Diagnostics = append(report.Diagnostics, diagnostic("DATA_CONTEXT_UNKNOWN", "", map[string]any{}))
	}
	var selected *catalogEntry
	if usableProfile && contextKnown && (input.Source == "fixture" || input.Source == "blizzard") {
		for i := range e.entries {
			entry := &e.entries[i]
			rule := entry.rules
			if !rule.Enabled || rule.ReviewedAt.After(now) || rule.Synthetic != (input.Source == "fixture") || rule.Patch != *report.Context.Patch || rule.ClassID != *report.Context.ClassID || rule.SpecializationID != *report.Context.SpecializationID || rule.Content != input.Content {
				continue
			}
			if rule.Season != nil && (report.Context.Season == nil || *rule.Season != *report.Context.Season) {
				continue
			}
			selected = entry
			break
		}
	}
	if selected == nil {
		report.Diagnostics = append(report.Diagnostics, diagnostic("DATA_RULESET_UNAVAILABLE", "", map[string]any{}))
		for _, id := range []string{"GEAR_MISSING_ENCHANT", "GEAR_SET_BONUS", "TALENT_BUILD_DIFF"} {
			report.Results = append(report.Results, unknown(id, EngineVersion, "unknown", "RULESET_UNAVAILABLE", map[string]any{}))
		}
	} else {
		rule := selected.rules
		report.Ruleset = &RulesetInfo{Version: rule.Version, Digest: selected.digest, Synthetic: rule.Synthetic, ReviewedAt: rule.ReviewedAt, ReviewedBy: rule.ReviewedBy, References: append([]Reference{}, rule.References...)}
		report.Results = append(report.Results, evaluateEnchants(input.Snapshot.Sections["equipment"], rule, now)...)
		report.Results = append(report.Results, evaluateSets(input.Snapshot.Sections["equipment"], rule, now)...)
		report.Results = append(report.Results, evaluateTalents(input.Snapshot.Sections["talents"], rule, now))
	}
	for _, result := range report.Results {
		if result.Outcome == nil {
			report.Summary.NotApplicable++
			continue
		}
		switch *result.Outcome {
		case "OK":
			report.Summary.OK++
		case "Info":
			report.Summary.Info++
		case "Warning":
			report.Summary.Warning++
		case "Critical":
			report.Summary.Critical++
		default:
			report.Summary.Unknown++
		}
	}
	return report
}

func evaluateEnchants(section character.Section, rules Ruleset, now time.Time) []Result {
	const id = "GEAR_MISSING_ENCHANT"
	rule := rules.Enchant
	if !rule.Enabled {
		return []Result{unknown(id, rule.Version, "unknown", "ENCHANT_RULE_DISABLED", map[string]any{})}
	}
	gear, ok := parseEquipment(section, now)
	if !ok {
		return []Result{unknown(id, rule.Version, "unknown", "ENCHANT_DATA_INCOMPLETE", map[string]any{})}
	}
	results := []Result{}
	for index, item := range gear.Items {
		params := map[string]any{"slot": item.Slot, "item_id": item.ItemID}
		var matrix *EnchantSlot
		for i := range rule.Slots {
			candidate := &rule.Slots[i]
			if candidate.Slot == item.Slot && contains(candidate.ItemIDs, item.ItemID) {
				matrix = candidate
				break
			}
		}
		var result Result
		switch {
		case matrix == nil:
			result = unknown(id, rule.Version, "unknown", "ENCHANT_APPLICABILITY_UNKNOWN", params)
		case !matrix.Applicable:
			result = notApplicable(id, rule.Version, "ENCHANT_NOT_APPLICABLE", params)
		case item.Enchantments == nil:
			result = unknown(id, rule.Version, "applicable", "ENCHANT_DATA_INCOMPLETE", params)
		default:
			present := false
			valid := true
			for _, enchant := range item.Enchantments {
				if !positiveID(enchant.ID) {
					valid = false
				}
				if contains(matrix.CompatibleEnchantmentIDs, enchant.ID) {
					present = true
				}
			}
			if !valid {
				result = unknown(id, rule.Version, "applicable", "ENCHANT_DATA_INCOMPLETE", params)
			} else if present {
				result = observed(id, rule.Version, "OK", "ENCHANT_PRESENT", params)
			} else {
				result = observed(id, rule.Version, "Warning", "ENCHANT_MISSING", params)
			}
		}
		if matrix != nil {
			result.Evidence = append(result.Evidence, evidence(section, "equipment", "/items/"+strconv.Itoa(index), map[string]any{"slot": item.Slot, "item_id": item.ItemID, "enchantments": item.Enchantments}, reference(rules, rule.Reference)))
		}
		results = append(results, result)
	}
	if len(results) == 0 {
		return []Result{notApplicable(id, rule.Version, "ENCHANT_NOT_APPLICABLE", map[string]any{})}
	}
	return results
}

func evaluateSets(section character.Section, rules Ruleset, now time.Time) []Result {
	const id = "GEAR_SET_BONUS"
	rule := rules.SetBonus
	if !rule.Enabled {
		return []Result{unknown(id, rule.Version, "unknown", "SET_RULE_DISABLED", map[string]any{})}
	}
	gear, ok := parseEquipment(section, now)
	if !ok {
		return []Result{unknown(id, rule.Version, "unknown", "SET_DATA_INCOMPLETE", map[string]any{})}
	}
	if len(rule.Sets) == 0 {
		return []Result{notApplicable(id, rule.Version, "SET_CONTEXT_EXCLUDED", map[string]any{})}
	}
	results := []Result{}
	for _, set := range rule.Sets {
		pieces := 0
		known := set.ItemCountOnly
		seenItems := map[string]bool{}
		for _, item := range gear.Items {
			if !contains(set.ItemIDs, item.ItemID) {
				if item.SetID != nil && *item.SetID == set.SetID {
					known = false
				}
				continue
			}
			if item.SetID == nil || *item.SetID != set.SetID || seenItems[item.ItemID] {
				known = false
				continue
			}
			pieces++
			seenItems[item.ItemID] = true
		}
		thresholds := []int{}
		for _, threshold := range set.Thresholds {
			if pieces >= threshold {
				thresholds = append(thresholds, threshold)
			}
		}
		var target any
		if set.TargetPieces != nil {
			target = *set.TargetPieces
		}
		params := map[string]any{"set_id": set.SetID, "pieces": pieces, "active_thresholds": thresholds, "target": target, "observed_only": section.State != "complete"}
		var result Result
		switch {
		case !known:
			result = unknown(id, rule.Version, "applicable", "SET_DATA_INCOMPLETE", params)
		case set.TargetPieces == nil:
			result = observed(id, rule.Version, "Info", "SET_BONUS_OBSERVED", params)
		case pieces >= *set.TargetPieces:
			result = observed(id, rule.Version, "OK", "SET_TARGET_MET", params)
		case section.State != "complete":
			result = unknown(id, rule.Version, "applicable", "SET_DATA_INCOMPLETE", params)
		default:
			result = observed(id, rule.Version, "Warning", "SET_TARGET_NOT_MET", params)
		}
		result.Evidence = append(result.Evidence, evidence(section, "equipment", "/items", map[string]any{"set_id": set.SetID, "observed_pieces": pieces}, reference(rules, rule.Reference)))
		results = append(results, result)
	}
	return results
}

func evaluateTalents(section character.Section, rules Ruleset, now time.Time) Result {
	const id = "TALENT_BUILD_DIFF"
	rule := rules.Talents
	if !rule.Enabled {
		return unknown(id, rule.Version, "unknown", "TALENT_RULE_DISABLED", map[string]any{})
	}
	if rule.TreeHash == "" || len(rule.Baseline) == 0 {
		return unknown(id, rule.Version, "unknown", "TALENT_BASELINE_UNAVAILABLE", map[string]any{})
	}
	var payload talentsData
	if section.State != "complete" || !section.Publishable(now) || json.Unmarshal(section.Payload, &payload) != nil || payload.Loadouts == nil {
		return unknown(id, rule.Version, "applicable", "TALENT_DATA_INCOMPLETE", map[string]any{})
	}
	var active *loadout
	for i := range payload.Loadouts {
		candidate := &payload.Loadouts[i]
		if candidate.Active == nil {
			return unknown(id, rule.Version, "applicable", "TALENT_DATA_INCOMPLETE", map[string]any{})
		}
		if *candidate.Active {
			if active != nil {
				return unknown(id, rule.Version, "applicable", "TALENT_DATA_INCOMPLETE", map[string]any{})
			}
			active = candidate
		}
	}
	if active == nil || active.Specialization.ID != rules.SpecializationID || active.TreeHash == nil || *active.TreeHash != rule.TreeHash {
		return unknown(id, rule.Version, "unknown", "TALENT_CONTEXT_UNKNOWN", map[string]any{})
	}
	if active.Nodes == nil || !validNodes(active.Nodes) {
		return unknown(id, rule.Version, "applicable", "TALENT_DATA_INCOMPLETE", map[string]any{})
	}
	selected := map[string]TalentNode{}
	for _, node := range active.Nodes {
		selected[nodeKey(node)] = node
	}
	expected := map[string]TalentNode{}
	for _, node := range rule.Baseline {
		expected[nodeKey(node)] = node
	}
	for _, alternative := range rule.Alternatives {
		baselineKey := nodeKey(alternative.Baseline)
		if _, exists := selected[baselineKey]; exists {
			continue
		}
		for _, allowed := range alternative.Allowed {
			key := nodeKey(allowed)
			if _, exists := selected[key]; exists {
				delete(expected, baselineKey)
				expected[key] = allowed
				break
			}
		}
	}
	missing := []string{}
	added := []string{}
	for key := range expected {
		if _, ok := selected[key]; !ok {
			missing = append(missing, key)
		}
	}
	for key := range selected {
		if _, ok := expected[key]; !ok {
			added = append(added, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(added)
	params := map[string]any{"missing_nodes": missing, "added_nodes": added, "changed_nodes": []string{}}
	result := observed(id, rule.Version, "Info", "TALENT_DIFFERENT", params)
	if len(missing) == 0 && len(added) == 0 {
		result = observed(id, rule.Version, "OK", "TALENT_MATCH", params)
	}
	for _, required := range rule.Required {
		if _, ok := selected[nodeKey(required)]; !ok {
			result = observed(id, rule.Version, "Warning", "TALENT_REQUIRED_MISSING", params)
			break
		}
	}
	result.Evidence = append(result.Evidence, evidence(section, "talents", "/loadouts", map[string]any{"tree_hash": active.TreeHash, "specialization_id": active.Specialization.ID, "nodes": active.Nodes}, reference(rules, rule.Reference)))
	return result
}

func parseEquipment(section character.Section, now time.Time) (equipmentData, bool) {
	var gear equipmentData
	if !section.Publishable(now) || json.Unmarshal(section.Payload, &gear) != nil || gear.Items == nil || len(gear.Items) > 32 {
		return gear, false
	}
	slots := map[string]bool{}
	for _, item := range gear.Items {
		if !bounded(item.Slot, 64) || !positiveID(item.ItemID) || slots[item.Slot] {
			return gear, false
		}
		slots[item.Slot] = true
	}
	return gear, true
}
func unknown(id, version, applicability, code string, params map[string]any) Result {
	outcome := "Unknown"
	return Result{RuleID: id, RuleVersion: version, Applicability: applicability, Outcome: &outcome, Confidence: "unknown", ConfidenceReason: "required_data_unavailable", ReasonCode: code, MessageKey: code, Params: params, Evidence: []Evidence{}}
}
func observed(id, version, outcome, code string, params map[string]any) Result {
	return Result{RuleID: id, RuleVersion: version, Applicability: "applicable", Outcome: &outcome, Confidence: "high", ConfidenceReason: "reviewed_ruleset_and_observed_data", ReasonCode: code, MessageKey: code, Params: params, Evidence: []Evidence{}}
}
func notApplicable(id, version, code string, params map[string]any) Result {
	return Result{RuleID: id, RuleVersion: version, Applicability: "not_applicable", Outcome: nil, Confidence: "high", ConfidenceReason: "scope_excludes_item", ReasonCode: code, MessageKey: code, Params: params, Evidence: []Evidence{}}
}
func diagnostic(code, section string, params map[string]any) Diagnostic {
	var selected *string
	if section != "" {
		selected = &section
		params["section"] = section
	}
	return Diagnostic{Code: code, Section: selected, MessageKey: code, Params: params, Evidence: []Evidence{}}
}
func evidence(section character.Section, name, path string, value any, ref *string) Evidence {
	return Evidence{Section: name, Path: path, Value: value, FetchedAt: section.FetchedAt, Reference: ref}
}
func reference(rules Ruleset, id string) *string {
	for _, ref := range rules.References {
		if ref.ID == id {
			value := ref.URL
			return &value
		}
	}
	return nil
}
func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
