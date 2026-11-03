package blizzard

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
)

// Fetch collects a mandatory profile and five independent optional sections.
// Unknown game context is deliberately left nil; no live season is guessed.
func (c *Client) Fetch(ctx context.Context, key character.Key, realm character.Realm) (character.Snapshot, error) {
	lookupName := key.LookupName
	if lookupName == "" {
		lookupName = key.Name
	}
	path := "/profile/wow/character/" + url.PathEscape(realm.Slug) + "/" + url.PathEscape(strings.ToLower(lookupName))
	body, _, err := c.get(ctx, key.Region, "profile", path, "profile")
	if err != nil {
		return character.Snapshot{}, fault(err)
	}
	var raw object
	if decode(body, &raw) != nil {
		return character.Snapshot{}, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	now := c.cfg.Now().UTC()
	profile, providerID, displayName, err := normalizeProfile(raw, key, realm)
	if err != nil {
		return character.Snapshot{}, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	snapshot := character.Snapshot{ProviderID: providerID, DisplayName: displayName, FetchedAt: now, ProfileVerifiedAt: now, ValidUntil: now.Add(720 * time.Hour), Sections: map[string]character.Section{}}
	snapshot.Sections["profile"] = section(profile, "complete", nil, now, 15*time.Minute, updated(raw))
	for _, entry := range []struct {
		name, path string
		ttl        time.Duration
	}{
		{"equipment", "equipment", 15 * time.Minute}, {"statistics", "statistics", 15 * time.Minute}, {"talents", "specializations", 30 * time.Minute},
	} {
		body, _, err = c.get(ctx, key.Region, "profile", path+"/"+entry.path, "section")
		checked := c.cfg.Now().UTC()
		if err != nil {
			var pe *Error
			if errors.As(err, &pe) {
				if pe.Code == "PROVIDER_AUTH_FAILURE" || pe.Code == "PROVIDER_RATE_LIMITED" {
					return character.Snapshot{}, fault(err)
				}
				reason := "provider_unavailable"
				if pe.Code == "SECTION_UNAVAILABLE" || pe.Code == "PROVIDER_ACCESS_DENIED" {
					reason = "not_provided"
				}
				if pe.Code == "PROVIDER_INVALID_RESPONSE" {
					reason = "invalid_response"
				}
				snapshot.Sections[entry.name] = character.Unavailable(reason, checked)
				continue
			}
			return character.Snapshot{}, fault(err)
		}
		var raw object
		if decode(body, &raw) != nil {
			snapshot.Sections[entry.name] = character.Unavailable("invalid_response", checked)
			continue
		}
		var payload object
		var partial bool
		switch entry.name {
		case "equipment":
			payload, partial = normalizeEquipment(raw)
		case "statistics":
			payload, partial = normalizeStatistics(raw)
		case "talents":
			payload, partial = normalizeTalents(raw, profile["active_specialization"])
		}
		if payload == nil {
			snapshot.Sections[entry.name] = character.Unavailable("not_provided", checked)
			continue
		}
		state := "complete"
		var reason *string
		if partial {
			state = "partial"
			r := "not_provided"
			reason = &r
		}
		snapshot.Sections[entry.name] = section(payload, state, reason, checked, entry.ttl, updated(raw))
	}
	progress, err := c.fetchProgress(ctx, key, realm, providerID, path)
	if err != nil {
		return character.Snapshot{}, err
	}
	for name, section := range progress {
		snapshot.Sections[name] = section
	}
	return snapshot, nil
}

// Realms reads only the allowlisted regional index and keeps the provider's
// canonical slug. UI translations never alter this identifier.
func (c *Client) Realms(ctx context.Context, region string) ([]character.Realm, error) {
	body, _, err := c.get(ctx, region, "dynamic", "/data/wow/realm/index", "catalog")
	if err != nil {
		return nil, fault(err)
	}
	var raw object
	if decode(body, &raw) != nil {
		return nil, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	list, ok := raw["realms"].([]any)
	if !ok || len(list) == 0 || len(list) > 5000 {
		return nil, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	result := make([]character.Realm, 0, len(list))
	seen := map[string]bool{}
	for _, entry := range list {
		o, ok := entry.(map[string]any)
		if !ok {
			return nil, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
		}
		id := positiveID(o["id"])
		slug := text(o["slug"])
		name := label(o["name"])
		if id == "" || slug == "" || name == nil || seen[slug] {
			return nil, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
		}
		if _, err := character.NormalizeKey(region, slug, "Example"); err != nil {
			return nil, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
		}
		seen[slug] = true
		names := map[string]string{"en-US": *name}
		if localized, ok := o["name"].(map[string]any); ok {
			for from, to := range map[string]string{"ru_RU": "ru-RU", "en_US": "en-US", "en_GB": "en-US"} {
				if n := text(localized[from]); n != "" {
					names[to] = n
				}
			}
		}
		var connected *string
		if conn := asObject(o["connected_realm"]); conn != nil {
			connected = nullableID(conn["id"])
		}
		result = append(result, character.Realm{ProviderID: id, Region: region, Slug: slug, Names: names, ConnectedID: connected, FetchedAt: c.cfg.Now().UTC()})
	}
	return result, nil
}

func fault(err error) error {
	var e *Error
	if !errors.As(err, &e) {
		var f *character.Fault
		if errors.As(err, &f) {
			return f
		}
		return character.NewFault("PROVIDER_UNAVAILABLE", true)
	}
	f := character.NewFault(e.Code, e.Retryable)
	if e.RetryAt != nil {
		f.RetryAt = *e.RetryAt
	}
	return f
}

func section(payload object, state string, reason *string, now time.Time, ttl time.Duration, sourceUpdated *time.Time) character.Section {
	data, _ := json.Marshal(payload)
	fresh, valid := now.Add(ttl), now.Add(720*time.Hour)
	return character.Section{State: state, Reason: reason, FetchedAt: &now, CheckedAt: now, FreshUntil: &fresh, ValidUntil: &valid, SourceUpdatedAt: sourceUpdated, Payload: data}
}

type object = map[string]any

func asObject(v any) object { o, _ := v.(map[string]any); return o }
func text(v any) string     { s, _ := v.(string); return s }
func number(v any) *float64 {
	n, ok := v.(json.Number)
	if !ok {
		return nil
	}
	f, err := n.Float64()
	if err != nil || f < 0 || f > 9007199254740991 {
		return nil
	}
	return &f
}
func positiveID(v any) string {
	n, ok := v.(json.Number)
	if !ok {
		return ""
	}
	i, err := n.Int64()
	if err != nil || i <= 0 {
		return ""
	}
	return strconv.FormatInt(i, 10)
}
func nullableID(v any) *string {
	id := positiveID(v)
	if id == "" {
		return nil
	}
	return &id
}
func label(v any) *string {
	if s := text(v); s != "" && len([]rune(s)) <= 200 {
		return &s
	}
	if m := asObject(v); m != nil {
		for _, key := range []string{"en_US", "en_GB", "ru_RU"} {
			if s := text(m[key]); s != "" && len([]rune(s)) <= 200 {
				return &s
			}
		}
	}
	return nil
}
func named(v any) object {
	o := asObject(v)
	if o == nil || positiveID(o["id"]) == "" {
		return nil
	}
	return object{"id": positiveID(o["id"]), "name": label(o["name"])}
}
func updated(raw object) *time.Time {
	n, ok := raw["last_modified"].(json.Number)
	if !ok {
		return nil
	}
	v, err := n.Int64()
	if err != nil || v <= 0 {
		return nil
	}
	t := time.UnixMilli(v).UTC()
	return &t
}

func normalizeProfile(raw object, key character.Key, realm character.Realm) (object, string, string, error) {
	id := positiveID(raw["id"])
	name := text(raw["name"])
	sourceRealm := asObject(raw["realm"])
	actual, err := character.NormalizeKey(key.Region, realm.Slug, name)
	level := number(raw["level"])
	race, class := named(raw["race"]), named(raw["character_class"])
	if race == nil {
		race = named(raw["playable_race"])
	}
	if class == nil {
		class = named(raw["playable_class"])
	}
	if id == "" || err != nil || actual.Name != key.Name || sourceRealm == nil || positiveID(sourceRealm["id"]) != realm.ProviderID || level == nil || *level < 1 || *level > 1000 || float64(int(*level)) != *level || race == nil || class == nil {
		return nil, "", "", errors.New("invalid mandatory profile")
	}
	var lastLogin *time.Time
	if n, ok := raw["last_login_timestamp"].(json.Number); ok {
		if v, e := n.Int64(); e == nil && v > 0 {
			t := time.UnixMilli(v).UTC()
			lastLogin = &t
		}
	}
	faction := strings.ToLower(text(asObject(raw["faction"])["type"]))
	if faction != "alliance" && faction != "horde" && faction != "neutral" {
		faction = "unknown"
	}
	itemLevel := func(v any) *float64 {
		n := number(v)
		if n != nil && *n > 100000 {
			return nil
		}
		return n
	}
	return object{"id": "00000000-0000-4000-8000-000000000000", "provider_id": id, "region": key.Region, "realm": realm.DTO("en-US"), "name": name, "name_normalized": key.Name, "level": int(*level), "race": race, "character_class": class, "active_specialization": named(raw["active_spec"]), "faction": faction, "average_item_level": itemLevel(raw["average_item_level"]), "equipped_item_level": itemLevel(raw["equipped_item_level"]), "source_last_login_at": lastLogin}, id, name, nil
}
