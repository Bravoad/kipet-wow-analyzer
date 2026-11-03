package raiderio

import (
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"kipet-wow-analyzer/internal/domain/raiderio"
)

type wireProfile struct {
	Name           string          `json:"name"`
	Region         string          `json:"region"`
	Realm          string          `json:"realm"`
	ProfileURL     string          `json:"profile_url"`
	Race           *string         `json:"race"`
	Class          *string         `json:"class"`
	Specialization *string         `json:"active_spec_name"`
	Role           *string         `json:"active_spec_role"`
	Faction        *string         `json:"faction"`
	LastCrawledAt  *string         `json:"last_crawled_at"`
	Gear           json.RawMessage `json:"gear"`
	Talents        json.RawMessage `json:"talentLoadout"`
	Scores         json.RawMessage `json:"mythic_plus_scores_by_season"`
	Runs           json.RawMessage `json:"mythic_plus_best_runs"`
	Raids          json.RawMessage `json:"raid_progression"`
	Guild          json.RawMessage `json:"guild"`
}

func normalizeProfile(key raiderio.Key, body []byte) (raiderio.Profile, error) {
	var raw wireProfile
	if err := json.Unmarshal(body, &raw); err != nil {
		return raiderio.Profile{}, invalid()
	}
	realm, err := raiderio.ProfileRealm(key, raw.Region, raw.Name, raw.Realm, raw.ProfileURL)
	if err != nil {
		return raiderio.Profile{}, err
	}
	profile := raiderio.Profile{
		Name: strings.TrimSpace(raw.Name), Region: raw.Region, Realm: realm, ProfileURL: raw.ProfileURL,
		Race: textValue(raw.Race, 80), Class: textValue(raw.Class, 80),
		Specialization: textValue(raw.Specialization, 80), Role: textValue(raw.Role, 32),
		Faction: textValue(raw.Faction, 32), LastCrawledAt: timestamp(raw.LastCrawledAt),
		Coverage: make(map[string]raiderio.Coverage, 5),
	}
	if profile.Faction != nil {
		faction := strings.ToLower(*profile.Faction)
		profile.Faction = &faction
	}
	profile.Coverage["profile"] = coverage("complete", "", profile.LastCrawledAt)
	profile.Gear, profile.Coverage["gear"] = normalizeGear(raw.Gear)
	profile.Talents, profile.Coverage["talents"] = normalizeTalents(raw.Talents)
	profile.MythicPlus, profile.Coverage["mythic_plus"] = normalizeMythicPlus(raw.Scores, raw.Runs)
	profile.Raids, profile.Coverage["raids"] = normalizeRaids(raw.Raids)
	if guild, _ := decodeOptional[raiderio.GuildLink](raw.Guild); guild != nil {
		if _, err := raiderio.NormalizeGuildKey(raw.Region, guild.Realm, guild.Name); err == nil {
			profile.Guild = &raiderio.GuildLink{Name: strings.TrimSpace(guild.Name), Realm: strings.TrimSpace(guild.Realm)}
		}
	}
	return profile, nil
}

func coverage(state, reason string, updatedAt *time.Time) raiderio.Coverage {
	value := raiderio.Coverage{State: state, SourceUpdatedAt: updatedAt}
	if reason != "" {
		value.Reason = &reason
	}
	return value
}

func decodeOptional[T any](body json.RawMessage) (*T, raiderio.Coverage) {
	var value *T
	if len(body) > 0 && json.Unmarshal(body, &value) != nil {
		return nil, coverage("unavailable", "invalid_response", nil)
	}
	if value == nil {
		return nil, coverage("unavailable", "not_provided", nil)
	}
	return value, coverage("complete", "", nil)
}

type wireGear struct {
	Equipped *float64             `json:"item_level_equipped"`
	Total    *float64             `json:"item_level_total"`
	Updated  *string              `json:"updated_at"`
	Items    map[string]*wireItem `json:"items"`
}

type wireItem struct {
	ItemID      int64    `json:"item_id"`
	Name        *string  `json:"name"`
	ItemLevel   *float64 `json:"item_level"`
	Quality     *int     `json:"item_quality"`
	Enchant     *int64   `json:"enchant"`
	Enchants    []int64  `json:"enchants"`
	EnchantInfo []struct {
		ID   int64   `json:"id"`
		Name *string `json:"name"`
	} `json:"enchants_detail"`
	Gems    []int64 `json:"gems"`
	Bonuses []int64 `json:"bonuses"`
}

func normalizeGear(body json.RawMessage) (*raiderio.Gear, raiderio.Coverage) {
	raw, status := decodeOptional[wireGear](body)
	if raw == nil {
		return nil, status
	}
	gear := &raiderio.Gear{
		EquippedItemLevel: metric(raw.Equipped), TotalItemLevel: metric(raw.Total),
		UpdatedAt: timestamp(raw.Updated),
	}
	partial := raw.Items == nil
	if raw.Items != nil {
		keys := sortedKeys(raw.Items)
		limit := min(len(keys), 64)
		partial = len(keys) > limit
		gear.Items = make([]raiderio.Item, 0, limit)
		for _, slot := range keys[:limit] {
			item := raw.Items[slot]
			if item == nil || item.ItemID <= 0 || !identifier(slot, 32, true) {
				partial = true
				continue
			}
			gear.Items = append(gear.Items, normalizeItem(slot, item))
		}
		if len(keys) > 0 && len(gear.Items) == 0 {
			gear.Items = nil
		}
	}
	status.SourceUpdatedAt = gear.UpdatedAt
	if partial {
		status = coverage("partial", "incomplete", gear.UpdatedAt)
	}
	return gear, status
}

func normalizeItem(slot string, item *wireItem) raiderio.Item {
	enchants := item.Enchants
	if enchants == nil && item.Enchant != nil && *item.Enchant > 0 {
		enchants = []int64{*item.Enchant}
	}
	out := raiderio.Item{
		Slot: slot, ItemID: strconv.FormatInt(item.ItemID, 10), Name: textValue(item.Name, 200),
		ItemLevel: metric(item.ItemLevel), Quality: count(item.Quality, 10),
		EnchantIDs: ids(enchants), GemIDs: ids(item.Gems), BonusIDs: ids(item.Bonuses),
	}
	// These IDs are the source's enchant details, in a different namespace from
	// the raw enchant IDs. Preserve each collection independently.
	if item.EnchantInfo != nil {
		limit := min(len(item.EnchantInfo), 128)
		out.Enchantments = make([]raiderio.Detail, 0, limit)
		for _, detail := range item.EnchantInfo[:limit] {
			if detail.ID > 0 {
				out.Enchantments = append(out.Enchantments, raiderio.Detail{
					ID: strconv.FormatInt(detail.ID, 10), Name: textValue(detail.Name, 200),
				})
			}
		}
		if len(item.EnchantInfo) > 0 && len(out.Enchantments) == 0 {
			out.Enchantments = nil
		}
	}
	return out
}

func normalizeTalents(body json.RawMessage) (*raiderio.Talents, raiderio.Coverage) {
	type wireTalents struct {
		SpecID int64   `json:"loadout_spec_id"`
		Text   *string `json:"loadout_text"`
	}
	raw, status := decodeOptional[wireTalents](body)
	if raw == nil {
		return nil, status
	}
	talents := &raiderio.Talents{SpecializationID: positiveID(raw.SpecID), ExportCode: textValue(raw.Text, 4096)}
	if talents.SpecializationID == nil || talents.ExportCode == nil {
		status = coverage("partial", "incomplete", nil)
	}
	return talents, status
}

type wireScore struct {
	Season string              `json:"season"`
	Scores map[string]*float64 `json:"scores"`
}

type wireRun struct {
	Dungeon   string   `json:"dungeon"`
	Level     int      `json:"mythic_level"`
	Duration  *int64   `json:"clear_time_ms"`
	ParTime   *int64   `json:"par_time_ms"`
	Score     *float64 `json:"score"`
	Completed *string  `json:"completed_at"`
	URL       *string  `json:"url"`
}

func normalizeMythicPlus(scoresBody, runsBody json.RawMessage) (*raiderio.MythicPlus, raiderio.Coverage) {
	scores, scoresStatus := decodeOptional[[]wireScore](scoresBody)
	runs, runsStatus := decodeOptional[[]wireRun](runsBody)
	if scores == nil && runs == nil {
		if runsStatus.Reason != nil && *runsStatus.Reason == "invalid_response" {
			return nil, runsStatus
		}
		return nil, scoresStatus
	}
	out := &raiderio.MythicPlus{}
	if scores != nil && len(*scores) == 1 {
		source := (*scores)[0]
		if identifier(source.Season, 80, false) && strings.HasPrefix(source.Season, "season-") {
			out.Season = &source.Season
			out.Scores = roleScores(source.Scores)
			out.Rating = out.Scores["all"]
		}
	}
	partial := out.Season == nil || out.Rating == nil || runs == nil
	if runs != nil {
		limit := min(len(*runs), 100)
		out.BestRuns = make([]raiderio.Run, 0, limit)
		partial = partial || len(*runs) > limit
		for _, item := range (*runs)[:limit] {
			run, ok := normalizeRun(item)
			if !ok {
				partial = true
				continue
			}
			out.BestRuns = append(out.BestRuns, run)
		}
		if len(*runs) > 0 && len(out.BestRuns) == 0 {
			out.BestRuns = nil
		}
	}
	if partial {
		return out, coverage("partial", "incomplete", nil)
	}
	return out, coverage("complete", "", nil)
}

func roleScores(source map[string]*float64) map[string]*float64 {
	if source == nil {
		return nil
	}
	out := make(map[string]*float64)
	for _, key := range []string{"all", "dps", "healer", "tank", "spec_0", "spec_1", "spec_2", "spec_3"} {
		if value, ok := source[key]; ok {
			out[key] = metric(value)
		}
	}
	return out
}

func normalizeRun(item wireRun) (raiderio.Run, bool) {
	dungeon := textValue(&item.Dungeon, 200)
	if dungeon == nil || item.Level < 1 || item.Level > 1000 {
		return raiderio.Run{}, false
	}
	out := raiderio.Run{
		Dungeon: *dungeon, Level: item.Level, DurationMS: duration(item.Duration),
		ParTimeMS: duration(item.ParTime), Score: metric(item.Score),
		CompletedAt: timestamp(item.Completed), URL: runLink(item.URL),
	}
	if out.DurationMS != nil && out.ParTimeMS != nil {
		timed := *out.DurationMS <= *out.ParTimeMS
		out.Timed = &timed
	}
	return out, true
}

func runLink(raw *string) *string {
	if raw == nil || len(*raw) > 1024 {
		return nil
	}
	link, err := url.Parse(*raw)
	if err != nil || link.Scheme != "https" || link.Host != "raider.io" ||
		link.User != nil || link.RawQuery != "" || link.ForceQuery || link.Fragment != "" {
		return nil
	}
	path := strings.Split(link.EscapedPath(), "/")
	if len(path) != 4 || path[1] != "mythic-plus-runs" ||
		!identifier(path[2], 80, false) || !identifier(path[3], 200, false) {
		return nil
	}
	return raw
}

type wireRaid struct {
	ExpansionID *int64  `json:"expansion_id"`
	Summary     *string `json:"summary"`
	Total       *int    `json:"total_bosses"`
	Normal      *int    `json:"normal_bosses_killed"`
	Heroic      *int    `json:"heroic_bosses_killed"`
	Mythic      *int    `json:"mythic_bosses_killed"`
}

func normalizeRaids(body json.RawMessage) ([]raiderio.Raid, raiderio.Coverage) {
	raw, status := decodeOptional[map[string]*wireRaid](body)
	if raw == nil {
		return nil, status
	}
	keys := sortedKeys(*raw)
	limit := min(len(keys), 100)
	out := make([]raiderio.Raid, 0, limit)
	partial := len(keys) > limit
	for _, slug := range keys[:limit] {
		raid := (*raw)[slug]
		if raid == nil || !identifier(slug, 100, false) {
			partial = true
			continue
		}
		out = append(out, normalizeRaid(slug, raid))
	}
	if partial {
		status = coverage("partial", "incomplete", nil)
	}
	if len(keys) > 0 && len(out) == 0 {
		out = nil
	}
	return out, status
}

func normalizeRaid(slug string, raid *wireRaid) raiderio.Raid {
	total := count(raid.Total, 1000)
	maxKills := 1000
	if total != nil {
		maxKills = *total
	}
	out := raiderio.Raid{
		Slug: slug, Summary: textValue(raid.Summary, 100), TotalBosses: total,
		Normal: count(raid.Normal, maxKills), Heroic: count(raid.Heroic, maxKills),
		Mythic: count(raid.Mythic, maxKills),
	}
	if raid.ExpansionID != nil && *raid.ExpansionID >= 0 {
		id := strconv.FormatInt(*raid.ExpansionID, 10)
		out.ExpansionID = &id
	}
	return out
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func identifier(s string, maxBytes int, underscores bool) bool {
	if len(s) == 0 || len(s) > maxBytes {
		return false
	}
	for _, ch := range s {
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' || underscores && ch == '_') {
			return false
		}
	}
	return true
}

func textValue(value *string, maxRunes int) *string {
	if value == nil {
		return nil
	}
	s := strings.TrimSpace(*value)
	if s == "" || utf8.RuneCountInString(s) > maxRunes || strings.ContainsFunc(s, unicode.IsControl) {
		return nil
	}
	return &s
}

func timestamp(value *string) *time.Time {
	if value == nil {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, *value)
	if err != nil || parsed.Year() < 1970 || parsed.Year() > 9999 {
		return nil
	}
	utc := parsed.UTC()
	return &utc
}

func metric(value *float64) *float64 {
	if value != nil && *value >= 0 && *value <= 1_000_000 {
		return value
	}
	return nil
}

func duration(value *int64) *int64 {
	if value != nil && *value > 0 && *value <= (24*time.Hour).Milliseconds() {
		return value
	}
	return nil
}

func count(value *int, maxValue int) *int {
	if value != nil && *value >= 0 && *value <= maxValue {
		return value
	}
	return nil
}

func positiveID(value int64) *string {
	if value <= 0 {
		return nil
	}
	s := strconv.FormatInt(value, 10)
	return &s
}

func ids(values []int64) []string {
	if values == nil {
		return nil
	}
	limit := min(len(values), 128)
	out := make([]string, 0, limit)
	for _, value := range values[:limit] {
		if value > 0 {
			out = append(out, strconv.FormatInt(value, 10))
		}
	}
	if len(values) > 0 && len(out) == 0 {
		return nil
	}
	return out
}
