// Package fixture is an explicitly selected synthetic provider for local use.
// It never replaces a failing real provider and never invents arbitrary users.
package fixture

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/progress"
)

type Provider struct{ now func() time.Time }

func New() *Provider { return &Provider{now: time.Now} }

func (p *Provider) Realms(ctx context.Context, region string) ([]character.Realm, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := p.now().UTC()
	switch region {
	case "eu":
		return []character.Realm{{ProviderID: "900000001", Region: "eu", Slug: "гордунни", Names: map[string]string{"ru-RU": "Гордунни", "en-US": "Gordunni"}, FetchedAt: now}}, nil
	case "us":
		return []character.Realm{{ProviderID: "900000002", Region: "us", Slug: "stormrage", Names: map[string]string{"ru-RU": "Stormrage", "en-US": "Stormrage"}, FetchedAt: now}}, nil
	default:
		return nil, character.NewFault("INVALID_ARGUMENT", false)
	}
}

func (p *Provider) Fetch(ctx context.Context, key character.Key, realm character.Realm) (character.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return character.Snapshot{}, err
	}
	var display, providerID string
	itemLevel := 278
	if key.Region == "eu" && key.Realm == "гордунни" && key.Name == "кипет" {
		display, providerID = "Кипет", "900000011"
	} else if key.Region == "eu" && key.Realm == "гордунни" && key.Name == "ледник" {
		display, providerID, itemLevel = "Ледник", "900000013", 270
	} else if key.Region == "us" && key.Realm == "stormrage" && key.Name == "arcanis" {
		display, providerID = "Arcanis", "900000012"
	} else {
		return character.Snapshot{}, character.NewFault("CHARACTER_UNAVAILABLE", false)
	}
	now := p.now().UTC()
	valid := now.Add(720 * time.Hour)
	profile := map[string]any{"id": "00000000-0000-4000-8000-000000000000", "provider_id": providerID, "region": key.Region, "realm": realm.DTO("ru-RU"), "name": display, "name_normalized": key.Name, "level": 80, "race": map[string]any{"id": "1", "name": "Human"}, "character_class": map[string]any{"id": "6", "name": "Death Knight"}, "active_specialization": map[string]any{"id": "251", "name": "Frost"}, "faction": "alliance", "average_item_level": 278.0, "equipped_item_level": 278.0, "source_last_login_at": now.Add(-2 * time.Hour)}
	profile["average_item_level"], profile["equipped_item_level"] = float64(itemLevel), float64(itemLevel)
	items := make([]any, 0, 16)
	for i, slot := range []string{"head", "neck", "shoulder", "back", "chest", "wrist", "hands", "waist", "legs", "feet", "finger_1", "finger_2", "trinket_1", "trinket_2", "main_hand", "off_hand"} {
		var ench any = []any{}
		if slot == "head" {
			ench = nil
		}
		if slot == "wrist" {
			ench = []any{map[string]any{"id": "900003001", "display": "Synthetic enchant"}}
		}
		var setID any
		if slot == "head" || slot == "shoulder" {
			setID = "900002000"
		}
		items = append(items, map[string]any{"slot": slot, "item_id": strconv.Itoa(900001000 + i), "name": "Synthetic · " + slot, "item_level": itemLevel + i%3, "quality": "epic", "inventory_type": slot, "bonus_ids": []string{}, "enchantments": ench, "sockets": []any{}, "set_id": setID})
	}
	stat := func(rating, percent float64) map[string]any {
		return map[string]any{"rating": rating, "percent": percent}
	}
	attack := func(rating, percent float64) map[string]any {
		return map[string]any{"melee": stat(rating, percent), "ranged": stat(rating, percent), "spell": stat(rating, percent)}
	}
	statistics := map[string]any{"character_id": "00000000-0000-4000-8000-000000000000", "health": 4582000, "primary": map[string]any{"strength": 18450, "agility": 1200, "intellect": 500, "stamina": 215000}, "secondary": map[string]any{"critical_strike": attack(4300, 22.4), "haste": attack(5100, 26.7), "mastery": stat(6200, 43.1), "versatility": map[string]any{"rating": 1400, "damage_done_percent": 7.2, "healing_done_percent": 7.2, "damage_taken_reduction_percent": 3.6}}}
	if display == "Ледник" {
		statistics["health"] = 4200000
		statistics["primary"].(map[string]any)["strength"] = 17200
	}
	talents := map[string]any{"character_id": "00000000-0000-4000-8000-000000000000", "loadouts": []any{map[string]any{"provider_id": nil, "specialization": map[string]any{"id": "251", "name": "Frost"}, "is_active": true, "export_code": nil, "tree_hash": nil, "nodes": nil}}}
	makeSection := func(data any, ttl time.Duration, partial bool) character.Section {
		payload, _ := json.Marshal(data)
		fresh := now.Add(ttl)
		state := "complete"
		var reason *string
		if partial {
			state = "partial"
			r := "not_provided"
			reason = &r
		}
		return character.Section{State: state, Reason: reason, FetchedAt: &now, CheckedAt: now, FreshUntil: &fresh, ValidUntil: &valid, Payload: payload}
	}
	patch := "synthetic-demo-v1"
	textPointer := func(value string) *string { return &value }
	intPointer := func(value int64) *int64 { return &value }
	floatPointer := func(value float64) *float64 { return &value }
	boolPointer := func(value bool) *bool { return &value }
	timePointer := func(value time.Time) *time.Time { return &value }
	season := progress.Season{ID: "900005001", Name: textPointer("Synthetic season 1"), IsCurrent: boolPointer(true)}
	mythic := progress.MythicPlus{CharacterID: "00000000-0000-4000-8000-000000000000", Season: &season, Rating: floatPointer(1420.5), RunScope: "season_best", AvailableSeasons: []progress.Season{season}, BestRuns: []progress.Run{
		{Dungeon: progress.NamedID{ID: "900006001", Name: textPointer("Synthetic · Frost Hall")}, KeystoneLevel: 8, DurationMS: intPointer(1534000), CompletedAt: timePointer(now.Add(-6 * time.Hour)), WithinTime: boolPointer(true), Rating: floatPointer(181.5)},
		{Dungeon: progress.NamedID{ID: "900006002", Name: textPointer("Synthetic · Ember Vault")}, KeystoneLevel: 9, DurationMS: intPointer(2190000), CompletedAt: timePointer(now.Add(-25 * time.Hour)), WithinTime: boolPointer(false), Rating: floatPointer(174)},
	}}
	if display == "Ледник" {
		mythic.Rating = floatPointer(1300)
	}
	raids := progress.Raids{CharacterID: "00000000-0000-4000-8000-000000000000", Expansions: []progress.Expansion{{ID: "900007001", Name: textPointer("Synthetic expansion"), Instances: []progress.RaidInstance{{ID: "900008001", Name: textPointer("Synthetic · Ice Citadel"), Modes: []progress.RaidMode{
		{Difficulty: "normal", Status: "in_progress", Completed: intPointer(1), Total: intPointer(3), Encounters: []progress.Encounter{
			{ID: "900009001", Name: textPointer("Synthetic · Gatekeeper"), CompletedCount: intPointer(2), LastKillAt: timePointer(now.Add(-48 * time.Hour))},
			{ID: "900009002", Name: textPointer("Synthetic · Frost Warden"), CompletedCount: intPointer(0)},
			{ID: "900009003", Name: textPointer("Synthetic · Ice Sovereign")},
		}},
		{Difficulty: "heroic", Status: "unknown", Encounters: nil},
	}}}}}}
	return character.Snapshot{ProviderID: providerID, DisplayName: display, FetchedAt: now, ProfileVerifiedAt: now, ValidUntil: valid, Patch: &patch, Sections: map[string]character.Section{"profile": makeSection(profile, 15*time.Minute, false), "equipment": makeSection(map[string]any{"character_id": "00000000-0000-4000-8000-000000000000", "items": items}, 15*time.Minute, true), "statistics": makeSection(statistics, 15*time.Minute, false), "talents": makeSection(talents, 30*time.Minute, true), "mythic_plus": makeSection(mythic, 15*time.Minute, false), "raids": makeSection(raids, 15*time.Minute, true)}}, nil
}
