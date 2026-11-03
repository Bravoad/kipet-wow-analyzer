package warcraftlogs

import "kipet-wow-analyzer/internal/domain/combat"

// CombatantInfo exposes the reported stat values. Preserve separate variants
// when provided and leave their units as supplied by the source.
var combatantStatNames = map[string]string{
	"strength": "strength", "agility": "agility", "intellect": "intellect", "stamina": "stamina",
	"criticalStrike": "critical_strike",
	"critMelee":      "critical_strike_melee", "critRanged": "critical_strike_ranged", "critSpell": "critical_strike_spell",
	"haste": "haste", "hasteMelee": "haste_melee", "hasteRanged": "haste_ranged", "hasteSpell": "haste_spell",
	"mastery": "mastery", "versatility": "versatility",
	"versatilityDamageDone": "versatility_damage_done", "versatilityHealingDone": "versatility_healing_done",
	"versatilityDamageReduction": "versatility_damage_reduction",
}

func normalizeCombatantStats(data any, fight combat.Fight, actorID int) *combat.CombatantStats {
	events, ok := data.([]any)
	if !ok || len(events) > 10 {
		return nil
	}
	var first *combat.CombatantStats
	for _, value := range events {
		raw := obj(value)
		actor, at := localID(raw["sourceID"]), integer(raw["timestamp"])
		if text(raw["type"]) != "combatantinfo" || actor == nil || *actor != actorID ||
			at == nil || *at < fight.StartMS || *at > fight.EndMS ||
			(first != nil && *at >= first.ObservedAtMS) {
			continue
		}
		if rawFight, exists := raw["fight"]; exists {
			id := localID(rawFight)
			if id == nil || *id != fight.ID {
				continue
			}
		}
		nested := obj(raw["stats"])
		values := make(map[string]float64)
		for source, name := range combatantStatNames {
			top, inside := amount(raw[source]), amount(nested[source])
			switch {
			case top != nil && inside != nil && *top != *inside:
				// An ambiguous field remains unknown.
			case inside != nil:
				values[name] = *inside
			case top != nil:
				values[name] = *top
			}
		}
		if len(values) > 0 {
			first = &combat.CombatantStats{ActorID: actorID, ObservedAtMS: *at, Values: values}
		}
	}
	return first
}
