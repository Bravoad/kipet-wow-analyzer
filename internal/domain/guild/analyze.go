package guild

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"

	"kipet-wow-analyzer/internal/domain/character"
)

// Analyze summarizes authorized observations; it makes no network requests and
// deliberately does not treat missing enchants as player mistakes.
func Analyze(bundle Bundle, observations []Observation) Analysis {
	out := Analysis{GuildID: bundle.Profile.ID, Classes: []Distribution{}, Specializations: []Distribution{}, Roles: []Distribution{}, Members: []MemberSummary{}, Limitations: []string{"ROSTER_BOUND_OBSERVATIONS", "ROLES_REQUIRE_SOURCE", "ENCHANT_COUNTS_ARE_OBSERVATIONS", "GUILD_LOGS_NOT_LINKED", "NO_CROSS_SPECIALIZATION_RANKING"}}
	out.Coverage.Sections = map[string]int{"profile": 0, "equipment": 0, "statistics": 0, "talents": 0, "mythic_plus": 0, "raids": 0}
	out.Coverage.RosterKnown = bundle.Roster.Members != nil
	out.Coverage.Reported = bundle.Roster.ReportedCount
	if out.Coverage.Reported == nil {
		out.Coverage.Reported = bundle.Profile.MemberCount
	}
	if bundle.Roster.Members == nil {
		out.Limitations = append(out.Limitations, "ROSTER_UNAVAILABLE")
		return out
	}
	members := append([]Member(nil), bundle.Roster.Members...)
	sort.SliceStable(members, func(i, j int) bool { return memberKey(members[i]) < memberKey(members[j]) })
	byKey := map[string][]Observation{}
	for _, o := range observations {
		k := memberKey(o.Member)
		byKey[k] = append(byKey[k], o)
	}
	classes, specs, roles := map[string]Distribution{}, map[string]Distribution{}, map[string]Distribution{}
	levels := []float64{}
	out.Coverage.Roster = len(members)
	if out.Coverage.Reported != nil && *out.Coverage.Reported != len(members) {
		out.Limitations = append(out.Limitations, "ROSTER_COUNT_MISMATCH")
	}
	for _, member := range members {
		rosterClass := member.Class
		if rosterClass != nil && !positiveID(rosterClass.ID) {
			rosterClass = nil
		}
		addDistribution(classes, rosterClass)
		summary := MemberSummary{Member: member, Status: "pending"}
		candidates := byKey[memberKey(member)]
		if len(candidates) != 1 {
			if len(candidates) > 1 {
				summary.Status = "unavailable"
			}
			countUnavailable(&out, summary)
			continue
		}
		observation := candidates[0]
		if observation.Status != "fresh" && observation.Status != "stale" {
			if observation.Status == "unavailable" {
				summary.Status = "unavailable"
			}
			countUnavailable(&out, summary)
			continue
		}
		var profile struct {
			ProviderID string `json:"provider_id"`
			Region     string `json:"region"`
			Name       string `json:"name"`
			Realm      struct {
				Slug string `json:"slug"`
			} `json:"realm"`
			Class          *NamedID `json:"character_class"`
			Specialization *NamedID `json:"active_specialization"`
			Role           *string  `json:"role"`
			ItemLevel      *float64 `json:"equipped_item_level"`
		}
		key, err := character.NormalizeKey(member.Key.Region, member.Key.Realm, member.Key.Name)
		if json.Unmarshal(observation.Profile, &profile) != nil || observation.SnapshotID == nil || *observation.SnapshotID == "" || err != nil || profile.ProviderID != member.ProviderID || !positiveID(profile.ProviderID) {
			summary.Status = "unavailable"
			countUnavailable(&out, summary)
			continue
		}
		actual, actualErr := character.NormalizeKey(profile.Region, profile.Realm.Slug, profile.Name)
		if actualErr != nil || actual.Region != key.Region || actual.Realm != key.Realm || actual.Name != key.Name {
			summary.Status = "unavailable"
			countUnavailable(&out, summary)
			continue
		}
		summary.Status = observation.Status
		summary.SnapshotID = observation.SnapshotID
		if observation.Notices != nil && *observation.Notices >= 0 {
			summary.Notices = observation.Notices
		}
		out.Coverage.Profiles++
		out.Coverage.Sections["profile"]++
		if summary.Status == "fresh" {
			out.Coverage.Fresh++
		} else {
			out.Coverage.Stale++
		}
		if profile.Specialization != nil && positiveID(profile.Specialization.ID) {
			summary.Specialization = profile.Specialization
		}
		addDistribution(specs, summary.Specialization)
		if profile.Role != nil && (*profile.Role == "tank" || *profile.Role == "healer" || *profile.Role == "damage") {
			summary.Role = profile.Role
			addDistribution(roles, &NamedID{ID: *profile.Role, Name: profile.Role})
		} else {
			addDistribution(roles, nil)
		}
		if validNumber(profile.ItemLevel, 100000) {
			summary.ItemLevel = profile.ItemLevel
			levels = append(levels, *profile.ItemLevel)
		}
		for _, section := range []string{"equipment", "statistics", "talents", "mythic_plus", "raids"} {
			if validObject(observation.Sections[section]) {
				out.Coverage.Sections[section]++
			}
		}
		analyzeGear(&out, observation.Sections["equipment"])
		if validObject(observation.Sections["mythic_plus"]) {
			out.Progress.MythicProfiles++
			var mythic struct {
				Rating *float64 `json:"rating"`
				Season *struct {
					ID string `json:"id"`
				} `json:"season"`
			}
			if json.Unmarshal(observation.Sections["mythic_plus"], &mythic) == nil {
				if validNumber(mythic.Rating, 9007199254740991) {
					summary.MythicRating = mythic.Rating
				}
				if mythic.Season != nil && positiveID(mythic.Season.ID) {
					summary.MythicSeason = &mythic.Season.ID
				}
			}
		}
		if validObject(observation.Sections["raids"]) {
			out.Progress.RaidProfiles++
		}
		out.Members = append(out.Members, summary)
	}
	if len(levels) > 0 {
		sort.Float64s(levels)
		mean := 0.0
		for _, level := range levels {
			mean += level / float64(len(levels))
		}
		median := levels[len(levels)/2]
		if len(levels)%2 == 0 {
			median = (levels[len(levels)/2-1] + median) / 2
		}
		out.ItemLevel.Count = len(levels)
		out.ItemLevel.Mean = &mean
		out.ItemLevel.Median = &median
	}
	out.Classes = distributions(classes)
	out.Specializations = distributions(specs)
	out.Roles = distributions(roles)
	for _, member := range out.Members {
		if member.Notices == nil {
			out.Limitations = append(out.Limitations, "RULE_CHECKS_UNAVAILABLE")
			break
		}
	}
	return out
}
func countUnavailable(out *Analysis, summary MemberSummary) {
	if summary.Status == "pending" {
		out.Coverage.Pending++
	} else {
		out.Coverage.Unavailable++
	}
	out.Members = append(out.Members, summary)
}
func memberKey(member Member) string {
	key, err := character.NormalizeKey(member.Key.Region, member.Key.Realm, member.Key.Name)
	if err != nil {
		return member.Key.Region + "\x00" + member.Key.Realm + "\x00" + member.Key.Name
	}
	return key.Region + "\x00" + key.Realm + "\x00" + key.Name
}
func positiveID(value string) bool {
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == value
}
func validNumber(value *float64, max float64) bool {
	return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0 && *value <= max
}
func validObject(raw json.RawMessage) bool {
	var v map[string]json.RawMessage
	return json.Unmarshal(raw, &v) == nil && v != nil
}
func addDistribution(values map[string]Distribution, named *NamedID) {
	id := "unknown"
	var name *string
	if named != nil && named.ID != "" {
		id = named.ID
		name = named.Name
	}
	v := values[id]
	v.ID = id
	if v.Name == nil {
		v.Name = name
	}
	v.Count++
	values[id] = v
}
func distributions(values map[string]Distribution) []Distribution {
	keys := make([]string, 0, len(values))
	for id := range values {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	out := make([]Distribution, 0, len(keys))
	for _, id := range keys {
		out = append(out, values[id])
	}
	return out
}
func analyzeGear(out *Analysis, raw json.RawMessage) {
	var equipment struct {
		Items []struct {
			Enchantments []json.RawMessage `json:"enchantments"`
			SetID        *string           `json:"set_id"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &equipment) != nil {
		return
	}
	for _, item := range equipment.Items {
		if item.Enchantments == nil {
			out.Gear.UnknownEnchantments++
		} else {
			out.Gear.KnownEnchantments++
			if len(item.Enchantments) == 0 {
				out.Gear.EmptyEnchantments++
			} else {
				out.Gear.Enchanted++
			}
		}
		if item.SetID != nil && positiveID(*item.SetID) {
			out.Gear.KnownSets++
		}
	}
}
