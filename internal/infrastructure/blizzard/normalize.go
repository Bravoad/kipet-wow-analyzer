package blizzard

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"
)

func normalizeEquipment(raw object) (object, bool) {
	list, ok := raw["equipped_items"].([]any)
	if !ok || len(list) > 32 {
		return nil, true
	}
	items := make([]any, 0, len(list))
	partial := false
	slots := map[string]bool{}
	for _, entry := range list {
		o := asObject(entry)
		if o == nil {
			partial = true
			continue
		}
		id := positiveID(asObject(o["item"])["id"])
		slot := strings.ToLower(text(asObject(o["slot"])["type"]))
		if id == "" || slot == "" || utf8.RuneCountInString(slot) > 64 || slots[slot] {
			partial = true
			continue
		}
		slots[slot] = true
		quality := strings.ToLower(text(asObject(o["quality"])["type"]))
		switch quality {
		case "poor", "common", "uncommon", "rare", "epic", "legendary", "artifact", "heirloom":
		default:
			quality = "unknown"
			partial = true
		}
		var itemLevel *int
		if v := number(asObject(o["level"])["value"]); v != nil && *v <= 100000 && float64(int(*v)) == *v {
			n := int(*v)
			itemLevel = &n
		} else {
			partial = true
		}
		var inventory *string
		if v := text(asObject(o["inventory_type"])["type"]); v != "" && len(v) <= 80 {
			inventory = &v
		} else {
			partial = true
		}
		bonus := make([]string, 0)
		if values, ok := o["bonus_list"].([]any); ok && len(values) <= 100 {
			for _, v := range values {
				if id := positiveID(v); id != "" {
					found := false
					for _, existing := range bonus {
						if id == existing {
							found = true
						}
					}
					if !found {
						bonus = append(bonus, id)
					}
				} else {
					partial = true
				}
			}
		} else {
			partial = true
		}
		var ench []any
		if values, ok := o["enchantments"].([]any); ok && len(values) <= 20 {
			ench = make([]any, 0, len(values))
			for _, v := range values {
				e := asObject(v)
				eid := positiveID(e["enchantment_id"])
				if eid == "" {
					partial = true
					ench = nil
					break
				}
				ench = append(ench, object{"id": eid, "display": label(e["display_string"])})
			}
		} else {
			partial = true
		}
		var sockets []any
		if values, ok := o["sockets"].([]any); ok && len(values) <= 10 {
			sockets = make([]any, 0, len(values))
			for _, v := range values {
				s := asObject(v)
				kind := text(asObject(s["socket_type"])["type"])
				if kind == "" {
					partial = true
					sockets = nil
					break
				}
				sockets = append(sockets, object{"type": kind, "item_id": nullableID(asObject(s["item"])["id"])})
			}
		} else {
			partial = true
		}
		set := nullableID(asObject(asObject(o["set"])["item_set"])["id"])
		items = append(items, object{"slot": slot, "item_id": id, "name": label(o["name"]), "item_level": itemLevel, "quality": quality, "inventory_type": inventory, "bonus_ids": bonus, "enchantments": ench, "sockets": sockets, "set_id": set})
	}
	return object{"character_id": "00000000-0000-4000-8000-000000000000", "items": items}, partial
}

func normalizeStatistics(raw object) (object, bool) {
	partial := false
	get := func(v any) *float64 {
		n := number(v)
		if n == nil {
			partial = true
		}
		return n
	}
	getPercent := func(v any) *float64 {
		n, ok := v.(json.Number)
		if !ok {
			partial = true
			return nil
		}
		f, err := n.Float64()
		if err != nil || f < -100 || f > 100000 {
			partial = true
			return nil
		}
		return &f
	}
	primary := object{}
	for _, name := range []string{"strength", "agility", "intellect", "stamina"} {
		primary[name] = get(asObject(raw[name])["effective"])
	}
	attack := func(prefix string) object {
		o := asObject(raw[prefix])
		return object{"rating": get(o["rating"]), "percent": getPercent(o["value"])}
	}
	crit := object{"melee": attack("melee_crit"), "ranged": attack("ranged_crit"), "spell": attack("spell_crit")}
	haste := object{"melee": attack("melee_haste"), "ranged": attack("ranged_haste"), "spell": attack("spell_haste")}
	mastery := attack("mastery")
	versatility := object{"rating": get(raw["versatility"]), "damage_done_percent": getPercent(raw["versatility_damage_done_bonus"]), "healing_done_percent": getPercent(raw["versatility_healing_done_bonus"]), "damage_taken_reduction_percent": getPercent(raw["versatility_damage_taken_bonus"])}
	health := get(raw["health"])
	if health != nil && float64(int64(*health)) != *health {
		health = nil
		partial = true
	}
	return object{"character_id": "00000000-0000-4000-8000-000000000000", "health": health, "primary": primary, "secondary": object{"critical_strike": crit, "haste": haste, "mastery": mastery, "versatility": versatility}}, partial
}

func normalizeTalents(raw object, activeSpec any) (object, bool) {
	specializations, ok := raw["specializations"].([]any)
	if !ok || len(specializations) > 100 {
		return nil, true
	}
	result := make([]any, 0)
	partial := false
	for _, entry := range specializations {
		spec := asObject(entry)
		specialization := named(spec["specialization"])
		if specialization == nil {
			partial = true
			continue
		}
		loadouts, ok := spec["loadouts"].([]any)
		if !ok {
			// Older or incomplete schemas can identify a specialization without
			// exposing a modern loadout/tree. Preserve it without inventing nodes.
			result = append(result, object{"provider_id": nil, "specialization": specialization, "is_active": nil, "export_code": nil, "tree_hash": nil, "nodes": nil})
			partial = true
			continue
		}
		for _, entry := range loadouts {
			if len(result) >= 100 {
				return nil, true
			}
			l := asObject(entry)
			if l == nil {
				partial = true
				continue
			}
			var id *string
			if n, ok := l["id"].(json.Number); ok {
				if v, err := n.Int64(); err == nil && v >= 0 {
					s := strconv.FormatInt(v, 10)
					id = &s
				}
			}
			var active *bool
			if v, ok := l["is_active"].(bool); ok {
				active = &v
			} else {
				partial = true
			}
			var export *string
			for _, field := range []string{"talent_loadout_code", "export_code"} {
				if v := text(l[field]); v != "" && len(v) <= 4096 {
					export = &v
					break
				}
			}
			var treeHash *string
			if v := text(l["tree_hash"]); v != "" && len(v) <= 200 {
				treeHash = &v
			}
			// Different class, specialization and hero nodes are retained only
			// when node IDs/ranks are explicitly provided. Legacy talent IDs are
			// not silently reinterpreted as node IDs.
			var nodes []any
			seenNodes := map[string]bool{}
			known := false
			valid := true
			knownTrees := 0
			for _, tree := range []struct{ field, kind string }{{"selected_class_talents", "class"}, {"selected_spec_talents", "specialization"}, {"selected_hero_talents", "hero"}} {
				values, exists := l[tree.field]
				if !exists {
					continue
				}
				list, ok := values.([]any)
				if !ok {
					valid = false
					break
				}
				known = true
				knownTrees++
				if nodes == nil {
					nodes = make([]any, 0)
				}
				for _, entry := range list {
					n := asObject(entry)
					nid := positiveID(n["node_id"])
					rank := number(n["rank"])
					if nid == "" || rank == nil || *rank < 1 || *rank > 100 || float64(int(*rank)) != *rank || seenNodes[nid] || len(nodes) >= 300 {
						valid = false
						break
					}
					seenNodes[nid] = true
					// The common modern shape puts the spell under tooltip.spell_tooltip.spell.id.
					spell := nullableID(asObject(asObject(asObject(n["tooltip"])["spell_tooltip"])["spell"])["id"])
					if spell == nil {
						spell = nullableID(n["spell_id"])
					}
					nodes = append(nodes, object{"node_id": nid, "entry_id": nullableID(n["entry_id"]), "spell_id": spell, "rank": int(*rank), "tree": tree.kind})
				}
				if !valid {
					break
				}
			}
			if !known || !valid {
				nodes = nil
				partial = true
			}
			// Without a verified tree catalog an omitted tree cannot be inferred
			// empty. Keep observed nodes, but do not claim full talent coverage.
			if knownTrees < 3 {
				partial = true
			}
			result = append(result, object{"provider_id": id, "specialization": specialization, "is_active": active, "export_code": export, "tree_hash": treeHash, "nodes": nodes})
		}
	}
	// An explicitly empty specialization list is an observed empty response, not
	// a missing field. It remains partial because an active build is unconfirmed.
	if len(result) == 0 {
		partial = true
	}
	_ = activeSpec // Summary cannot prove loadout activity by itself.
	return object{"character_id": "00000000-0000-4000-8000-000000000000", "loadouts": result}, partial
}
