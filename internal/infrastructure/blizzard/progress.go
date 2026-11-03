package blizzard

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/progress"
)

type seasonCatalog struct {
	Current   *progress.Season
	ExpiresAt time.Time
	Loading   chan struct{}
}

// currentSeason consults a regional catalog, never the calendar or greatest ID.
// A single-flight cache limits catalog traffic across character workers.
func (c *Client) currentSeason(ctx context.Context, region string) (*progress.Season, error) {
	for {
		c.seasonMu.Lock()
		if c.seasons == nil {
			c.seasons = map[string]*seasonCatalog{}
		}
		cached := c.seasons[region]
		if cached != nil && cached.Loading == nil && c.cfg.Now().Before(cached.ExpiresAt) {
			current := cloneSeason(cached.Current)
			c.seasonMu.Unlock()
			return current, nil
		}
		if cached != nil && cached.Loading != nil {
			pending := cached.Loading
			c.seasonMu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-pending:
				continue
			}
		}
		pending := make(chan struct{})
		c.seasons[region] = &seasonCatalog{Loading: pending}
		c.seasonMu.Unlock()
		body, _, err := c.get(ctx, region, "dynamic", "/data/wow/mythic-keystone/season/index", "catalog")
		var raw object
		var current *progress.Season
		if err == nil {
			if decode(body, &raw) != nil {
				err = &Error{Code: "PROVIDER_INVALID_RESPONSE", Scope: "catalog"}
			} else {
				current = seasonValue(raw["current_season"])
				if current != nil {
					yes := true
					current.IsCurrent = &yes
				}
			}
		}
		c.seasonMu.Lock()
		if err == nil {
			c.seasons[region] = &seasonCatalog{Current: cloneSeason(current), ExpiresAt: c.cfg.Now().Add(time.Hour)}
		} else {
			delete(c.seasons, region)
		}
		close(pending)
		c.seasonMu.Unlock()
		return current, err
	}
}

func cloneSeason(in *progress.Season) *progress.Season {
	if in == nil {
		return nil
	}
	out := *in
	if in.Name != nil {
		name := *in.Name
		out.Name = &name
	}
	if in.IsCurrent != nil {
		current := *in.IsCurrent
		out.IsCurrent = &current
	}
	return &out
}
func seasonValue(v any) *progress.Season {
	raw := asObject(v)
	id := positiveID(raw["id"])
	if id == "" {
		return nil
	}
	return &progress.Season{ID: id, Name: label(raw["name"])}
}

func (c *Client) fetchProgress(ctx context.Context, key character.Key, realm character.Realm, providerID, path string) (map[string]character.Section, error) {
	out := map[string]character.Section{}
	mythic, err := c.fetchMythic(ctx, key, realm, providerID, path)
	if err != nil {
		return nil, err
	}
	out["mythic_plus"] = mythic
	body, _, err := c.get(ctx, key.Region, "profile", path+"/encounters/raids", "section")
	checked := c.cfg.Now().UTC()
	if err != nil {
		unavailable, fatal := optionalProgressError(err, checked)
		if fatal != nil {
			return nil, fatal
		}
		out["raids"] = unavailable
		return out, nil
	}
	var raw object
	if decode(body, &raw) != nil || !progressIdentity(raw, key, realm, providerID) {
		out["raids"] = character.Unavailable("invalid_response", checked)
		return out, nil
	}
	payload, partial := normalizeRaids(raw)
	if payload == nil {
		out["raids"] = character.Unavailable("not_provided", checked)
	} else {
		out["raids"] = progressSection(payload, partial, checked, updated(raw))
	}
	return out, nil
}

func (c *Client) fetchMythic(ctx context.Context, key character.Key, realm character.Realm, providerID, path string) (character.Section, error) {
	body, _, err := c.get(ctx, key.Region, "profile", path+"/mythic-keystone-profile", "section")
	checked := c.cfg.Now().UTC()
	if err != nil {
		return optionalProgressError(err, checked)
	}
	var index object
	if decode(body, &index) != nil || !progressIdentity(index, key, realm, providerID) {
		return character.Unavailable("invalid_response", checked), nil
	}
	current := seasonValue(index["current_season"])
	if current != nil {
		yes := true
		current.IsCurrent = &yes
	} else {
		current, err = c.currentSeason(ctx, key.Region)
		if err != nil {
			var e *Error
			if errors.As(err, &e) && (e.Code == "PROVIDER_AUTH_FAILURE" || e.Code == "PROVIDER_RATE_LIMITED") {
				return character.Section{}, fault(err)
			}
			current = nil
		}
	}
	var details object
	if current != nil {
		body, _, err = c.get(ctx, key.Region, "profile", path+"/mythic-keystone-profile/season/"+url.PathEscape(current.ID), "section")
		checked = c.cfg.Now().UTC()
		if err != nil {
			unavailable, fatal := optionalProgressError(err, checked)
			if fatal != nil {
				return character.Section{}, fatal
			}
			if unavailable.Reason == nil || *unavailable.Reason != "not_provided" {
				return unavailable, nil
			}
		} else if decode(body, &details) != nil || !progressIdentity(details, key, realm, providerID) || positiveID(asObject(details["season"])["id"]) != current.ID {
			return character.Unavailable("invalid_response", checked), nil
		}
	}
	payload, partial := normalizeMythic(index, details, current)
	if payload == nil {
		return character.Unavailable("not_provided", checked), nil
	}
	sourceUpdated := updated(details)
	if sourceUpdated == nil {
		sourceUpdated = updated(index)
	}
	return progressSection(payload, partial, checked, sourceUpdated), nil
}

func optionalProgressError(err error, checked time.Time) (character.Section, error) {
	var e *Error
	if errors.As(err, &e) {
		if e.Code == "PROVIDER_AUTH_FAILURE" || e.Code == "PROVIDER_RATE_LIMITED" {
			return character.Section{}, fault(err)
		}
		reason := "provider_unavailable"
		if e.Code == "SECTION_UNAVAILABLE" || e.Code == "PROVIDER_ACCESS_DENIED" {
			reason = "not_provided"
		}
		if e.Code == "PROVIDER_INVALID_RESPONSE" {
			reason = "invalid_response"
		}
		return character.Unavailable(reason, checked), nil
	}
	return character.Section{}, fault(err)
}

func progressSection(payload any, partial bool, now time.Time, sourceUpdated *time.Time) character.Section {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return character.Unavailable("invalid_response", now)
	}
	fresh, valid := now.Add(15*time.Minute), now.Add(720*time.Hour)
	state := "complete"
	var reason *string
	if partial {
		state = "partial"
		value := "not_provided"
		reason = &value
	}
	return character.Section{State: state, Reason: reason, FetchedAt: &now, CheckedAt: now, FreshUntil: &fresh, ValidUntil: &valid, SourceUpdatedAt: sourceUpdated, Payload: encoded}
}

func progressIdentity(raw object, key character.Key, realm character.Realm, providerID string) bool {
	value, present := raw["character"]
	if !present {
		return true
	}
	actual := asObject(value)
	if actual == nil || positiveID(actual["id"]) != providerID {
		return false
	}
	if name, present := actual["name"]; present {
		normalized, err := character.NormalizeKey(key.Region, realm.Slug, text(name))
		if err != nil || normalized.Name != key.Name {
			return false
		}
	}
	if value, present := actual["realm"]; present {
		source := asObject(value)
		if source == nil || positiveID(source["id"]) != realm.ProviderID {
			return false
		}
		if slug, present := source["slug"]; present && strings.TrimSpace(text(slug)) != realm.Slug {
			return false
		}
	}
	return true
}

// Nullable collections preserve the distinction between an absent observation
// and an explicitly returned empty list. Neither means a full run history.
func normalizeMythic(index, details object, current *progress.Season) (*progress.MythicPlus, bool) {
	if index == nil {
		return nil, false
	}
	out := &progress.MythicPlus{CharacterID: "00000000-0000-4000-8000-000000000000", Season: cloneSeason(current), RunScope: "unknown"}
	partial := false
	if list, ok := index["seasons"].([]any); ok {
		out.AvailableSeasons = make([]progress.Season, 0, len(list))
		seen := map[string]bool{}
		for _, value := range list {
			season := seasonValue(value)
			if season == nil || seen[season.ID] {
				partial = true
				continue
			}
			seen[season.ID] = true
			if current != nil {
				isCurrent := season.ID == current.ID
				season.IsCurrent = &isCurrent
			}
			out.AvailableSeasons = append(out.AvailableSeasons, *season)
		}
	} else {
		partial = true
	}
	var runs any
	if details != nil {
		runs = details["best_runs"]
		out.RunScope = "season_best"
		out.Rating = number(asObject(details["mythic_rating"])["rating"])
		// The cached catalog may straddle a season transition. An index rating
		// belongs to this detail only when the index explicitly identifies it.
		indexSeason := seasonValue(index["current_season"])
		if out.Rating == nil && current != nil && indexSeason != nil && indexSeason.ID == current.ID {
			out.Rating = number(asObject(index["current_mythic_rating"])["rating"])
		}
	} else {
		// Period observations identify their season only through their own index,
		// never through a possibly stale regional catalog used for the detail URL.
		out.Season = seasonValue(index["current_season"])
		if out.Season != nil {
			yes := true
			out.Season.IsCurrent = &yes
		}
		for i := range out.AvailableSeasons {
			out.AvailableSeasons[i].IsCurrent = nil
			if out.Season != nil {
				isCurrent := out.AvailableSeasons[i].ID == out.Season.ID
				out.AvailableSeasons[i].IsCurrent = &isCurrent
			}
		}
		out.Rating = number(asObject(index["current_mythic_rating"])["rating"])
		if period := asObject(index["current_period"]); period != nil {
			runs = period["best_runs"]
			out.RunScope = "current_period_best"
		}
	}
	if list, ok := runs.([]any); ok {
		out.BestRuns = make([]progress.Run, 0, len(list))
		for _, value := range list {
			raw := asObject(value)
			dungeon := asObject(raw["dungeon"])
			level := progressInteger(raw["keystone_level"])
			if raw == nil || positiveID(dungeon["id"]) == "" || level == nil || *level < 1 || *level > 1000 {
				partial = true
				continue
			}
			run := progress.Run{Dungeon: progress.NamedID{ID: positiveID(dungeon["id"]), Name: label(dungeon["name"])}, KeystoneLevel: int(*level), DurationMS: progressInteger(raw["duration"]), CompletedAt: progressTimestamp(raw["completed_timestamp"]), WithinTime: progressBool(raw["is_completed_within_time"]), Rating: number(asObject(raw["mythic_rating"])["rating"])}
			if run.Dungeon.Name == nil || run.DurationMS == nil || run.CompletedAt == nil || run.WithinTime == nil || run.Rating == nil {
				partial = true
			}
			out.BestRuns = append(out.BestRuns, run)
		}
	} else {
		partial = true
	}
	if out.Season == nil || out.Rating == nil {
		partial = true
	}
	if out.BestRuns == nil && out.AvailableSeasons == nil && out.Rating == nil {
		return nil, false
	}
	return out, partial
}

func normalizeRaids(raw object) (*progress.Raids, bool) {
	list, ok := raw["expansions"].([]any)
	if !ok {
		return nil, false
	}
	out := &progress.Raids{CharacterID: "00000000-0000-4000-8000-000000000000", Expansions: make([]progress.Expansion, 0, len(list))}
	partial := false
	seenExpansions := map[string]bool{}
	for _, value := range list {
		entry := asObject(value)
		expansion := asObject(entry["expansion"])
		id := positiveID(expansion["id"])
		if id == "" || seenExpansions[id] {
			partial = true
			continue
		}
		seenExpansions[id] = true
		next := progress.Expansion{ID: id, Name: label(expansion["name"])}
		if next.Name == nil {
			partial = true
		}
		if instances, ok := entry["instances"].([]any); ok {
			next.Instances = make([]progress.RaidInstance, 0, len(instances))
			seen := map[string]bool{}
			for _, value := range instances {
				entry := asObject(value)
				instance := asObject(entry["instance"])
				id := positiveID(instance["id"])
				if id == "" || seen[id] {
					partial = true
					continue
				}
				seen[id] = true
				raid := progress.RaidInstance{ID: id, Name: label(instance["name"])}
				if raid.Name == nil {
					partial = true
				}
				if modes, ok := entry["modes"].([]any); ok {
					raid.Modes = make([]progress.RaidMode, 0, len(modes))
					seenModes := map[string]bool{}
					for _, value := range modes {
						entry := asObject(value)
						if entry == nil {
							partial = true
							continue
						}
						difficulty := progressEnum(asObject(entry["difficulty"])["type"])
						status := progressEnum(asObject(entry["status"])["type"])
						if seenModes[difficulty] {
							partial = true
							continue
						}
						seenModes[difficulty] = true
						stats := asObject(entry["progress"])
						mode := progress.RaidMode{Difficulty: difficulty, Status: status, Completed: progressInteger(stats["completed_count"]), Total: progressInteger(stats["total_count"])}
						if difficulty == "unknown" || status == "unknown" || mode.Completed == nil || mode.Total == nil {
							partial = true
						}
						if mode.Completed != nil && mode.Total != nil && *mode.Completed > *mode.Total {
							mode.Completed = nil
							partial = true
						}
						if encounters, ok := stats["encounters"].([]any); ok {
							mode.Encounters = make([]progress.Encounter, 0, len(encounters))
							seenEncounters := map[string]bool{}
							for _, value := range encounters {
								entry := asObject(value)
								encounter := asObject(entry["encounter"])
								id := positiveID(encounter["id"])
								if id == "" || seenEncounters[id] {
									partial = true
									continue
								}
								seenEncounters[id] = true
								boss := progress.Encounter{ID: id, Name: label(encounter["name"]), CompletedCount: progressInteger(entry["completed_count"]), LastKillAt: progressTimestamp(entry["last_kill_timestamp"])}
								// No last kill is expected when the source confirms zero kills.
								if boss.Name == nil || boss.CompletedCount == nil || (boss.LastKillAt == nil && *boss.CompletedCount > 0) {
									partial = true
								}
								mode.Encounters = append(mode.Encounters, boss)
							}
						} else {
							partial = true
						}
						raid.Modes = append(raid.Modes, mode)
					}
				} else {
					partial = true
				}
				next.Instances = append(next.Instances, raid)
			}
		} else {
			partial = true
		}
		out.Expansions = append(out.Expansions, next)
	}
	return out, partial
}

func progressInteger(value any) *int64 {
	n, ok := value.(json.Number)
	if !ok {
		return nil
	}
	v, err := n.Int64()
	if err != nil || v < 0 || v > 9007199254740991 {
		return nil
	}
	return &v
}
func progressTimestamp(value any) *time.Time {
	n := progressInteger(value)
	if n == nil || *n <= 0 || *n > 253402300799999 {
		return nil
	}
	at := time.UnixMilli(*n).UTC()
	return &at
}
func progressBool(value any) *bool {
	b, ok := value.(bool)
	if !ok {
		return nil
	}
	return &b
}
func progressEnum(value any) string {
	v := text(value)
	if len(v) == 0 || len(v) > 80 {
		return "unknown"
	}
	for _, r := range v {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return "unknown"
		}
	}
	return strings.ToLower(v)
}
