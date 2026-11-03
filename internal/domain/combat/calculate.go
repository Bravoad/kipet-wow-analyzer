package combat

import (
	"math"
	"sort"
	"strings"

	"kipet-wow-analyzer/internal/domain/character"
)

const Method = "event-amounts/whole-fight-v1"
const maxSafe = 9007199254740991
const MaxTimeline = 500

// ValidCode accepts an opaque report code, never a URL or GraphQL expression.
func ValidCode(code string) bool {
	if len(code) != 16 {
		return false
	}
	for _, r := range code {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func Select(report Report, fightID, actorID int) (Fight, Actor, error) {
	if !ValidCode(report.Code) || report.Visibility != "public" {
		return Fight{}, Actor{}, character.NewFault("REPORT_UNAVAILABLE", false)
	}
	var selected Fight
	var actor Actor
	for _, f := range report.Fights {
		if f.ID == fightID {
			selected = f
			break
		}
	}
	for _, a := range report.Actors {
		if a.ID == actorID {
			actor = a
			break
		}
	}
	if selected.ID == 0 || actor.ID == 0 || strings.ToLower(actor.Type) != "player" || selected.StartMS < 0 || selected.EndMS <= selected.StartMS || selected.EndMS > maxSafe {
		return Fight{}, Actor{}, character.NewFault("INVALID_ARGUMENT", false)
	}
	if selected.ParticipantIDs != nil {
		member := false
		for _, id := range selected.ParticipantIDs {
			if id == actorID {
				member = true
				break
			}
		}
		if !member {
			return Fight{}, Actor{}, character.NewFault("INVALID_ARGUMENT", false)
		}
	}
	return selected, actor, nil
}

// Calculate uses report-relative milliseconds and the entire fight duration.
// amount is the normalized source amount: effective healing, excluding the
// separately supplied overheal. Damage/healing from pets or other actors is not
// attributed to this player. These are our calculations, not WCL rankings.
func Calculate(c Collection, source string) (Analysis, error) {
	fight, _, err := Select(c.Report, c.FightID, c.ActorID)
	if err != nil {
		return Analysis{}, err
	}
	if source != "fixture" && source != "warcraftlogs" {
		return Analysis{}, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	out := Analysis{ReportCode: c.Report.Code, FightID: c.FightID, ActorID: c.ActorID, FightStartMS: fight.StartMS, FightEndMS: fight.EndMS, DurationMS: fight.EndMS - fight.StartMS, Method: Method, Source: source, EventsComplete: c.Complete, Abilities: []Ability{}, Timeline: []Event{}, Findings: []Finding{}}
	out.CombatantStats = validateCombatantStats(c.CombatantStats, fight, c.ActorID)
	events := append([]Event(nil), c.Events...)
	sort.SliceStable(events, func(i, j int) bool { return events[i].TimestampMS < events[j].TimestampMS })
	damage, healing, overheal := 0.0, 0.0, 0.0
	casts, interrupts, deaths := 0, 0, 0
	damageKnown, healKnown, overhealKnown := true, true, true
	amountsUnavailable := false
	type observedAbility struct {
		value                  Ability
		damageKnown, healKnown bool
	}
	abilities := map[string]*observedAbility{}
	selectedEvents := []Event{}
	actorObserved := false
	for _, event := range events {
		knownKind := false
		switch event.Kind {
		case "damage", "heal", "cast", "interrupt", "death", "applybuff", "refreshbuff", "removebuff", "other":
			knownKind = true
		}
		if event.TimestampMS < fight.StartMS || event.TimestampMS > fight.EndMS || !knownKind {
			out.EventsComplete = false
			continue
		}
		if event.Kind == "death" && event.TargetID == nil || (event.Kind == "damage" || event.Kind == "heal" || event.Kind == "cast" || event.Kind == "interrupt") && event.SourceID == nil {
			out.EventsComplete = false
		}
		if event.Amount != nil && !validAmount(*event.Amount) {
			event.Amount = nil
		}
		if event.Overheal != nil && !validAmount(*event.Overheal) {
			event.Overheal = nil
		}
		outgoing := event.SourceID != nil && *event.SourceID == c.ActorID
		incoming := event.TargetID != nil && *event.TargetID == c.ActorID
		if !outgoing && !incoming {
			continue
		}
		actorObserved = true
		selectedEvents = append(selectedEvents, event)
		if event.Kind == "death" && incoming {
			deaths++
		}
		if !outgoing {
			continue
		}
		var ability *observedAbility
		if event.AbilityID != nil && (event.Kind == "damage" || event.Kind == "heal" || event.Kind == "cast") {
			ability = abilities[*event.AbilityID]
			if ability == nil {
				ability = &observedAbility{value: Ability{ID: *event.AbilityID}, damageKnown: true, healKnown: true}
				abilities[*event.AbilityID] = ability
			}
		}
		switch event.Kind {
		case "damage":
			if !add(&damage, event.Amount) {
				damageKnown = false
				if ability != nil {
					ability.damageKnown = false
				}
			}
		case "heal":
			if !add(&healing, event.Amount) {
				healKnown = false
				if ability != nil {
					ability.healKnown = false
				}
			}
			if !add(&overheal, event.Overheal) {
				overhealKnown = false
			}
		case "cast":
			casts++
			if ability != nil {
				ability.value.Casts++
			}
		case "interrupt":
			interrupts++
		}
		if ability != nil && event.Kind == "damage" {
			ability.value.Damage = sumPointer(ability.value.Damage, event.Amount, &ability.damageKnown)
		}
		if ability != nil && event.Kind == "heal" {
			ability.value.Healing = sumPointer(ability.value.Healing, event.Amount, &ability.healKnown)
		}
	}
	if fight.ParticipantIDs == nil && !actorObserved {
		out.EventsComplete = false
	}
	if out.EventsComplete {
		if damageKnown {
			out.Metrics.DamageTotal = &damage
			out.Metrics.DPS = rate(damage, out.DurationMS)
		}
		if healKnown {
			out.Metrics.HealingTotal = &healing
			out.Metrics.HPS = rate(healing, out.DurationMS)
		}
		if overhealKnown {
			out.Metrics.OverhealTotal = &overheal
		}
		if healKnown && overhealKnown && healing+overheal > 0 && healing+overheal <= maxSafe {
			value := 100 * overheal / (healing + overheal)
			out.Metrics.OverhealPercent = &value
		}
		out.Metrics.Casts = &casts
		out.Metrics.Interrupts = &interrupts
		out.Metrics.Deaths = &deaths
		amountsUnavailable = !damageKnown || !healKnown || !overhealKnown || out.Metrics.DPS == nil || out.Metrics.HPS == nil
	}
	ids := make([]string, 0, len(abilities))
	for id := range abilities {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		observed := abilities[id]
		if !observed.damageKnown {
			observed.value.Damage = nil
		}
		if !observed.healKnown {
			observed.value.Healing = nil
		}
		out.Abilities = append(out.Abilities, observed.value)
	}
	out.Timeline, out.TimelineTruncated = selectTimeline(selectedEvents, c.ActorID)
	indexes := []int{}
	for i, event := range out.Timeline {
		if event.Kind == "death" && event.TargetID != nil && *event.TargetID == c.ActorID {
			indexes = append(indexes, i)
		}
	}
	if len(indexes) > 0 {
		out.Findings = append(out.Findings, Finding{Code: "DEATHS_OBSERVED", Outcome: "Info", EventIndexes: indexes})
	}
	if !out.EventsComplete {
		out.Findings = append(out.Findings, Finding{Code: "INCOMPLETE_EVENTS", Outcome: "Unknown", EventIndexes: []int{}})
	}
	if amountsUnavailable {
		out.Findings = append(out.Findings, Finding{Code: "AMOUNTS_UNAVAILABLE", Outcome: "Unknown", EventIndexes: []int{}})
	}
	for _, code := range []string{"ROTATION_RULES_UNAVAILABLE", "COOLDOWN_RULES_UNAVAILABLE", "BUFF_UPTIME_UNAVAILABLE"} {
		out.Findings = append(out.Findings, Finding{Code: code, Outcome: "Unknown", EventIndexes: []int{}})
	}
	return out, nil
}

func validateCombatantStats(stats *CombatantStats, fight Fight, actorID int) *CombatantStats {
	if stats == nil || stats.ActorID != actorID || stats.ObservedAtMS < fight.StartMS || stats.ObservedAtMS > fight.EndMS {
		return nil
	}
	values := make(map[string]float64)
	for key, value := range stats.Values {
		if !validAmount(value) {
			continue
		}
		switch key {
		case "strength", "agility", "intellect", "stamina",
			"critical_strike", "critical_strike_melee", "critical_strike_ranged", "critical_strike_spell",
			"haste", "haste_melee", "haste_ranged", "haste_spell", "mastery",
			"versatility", "versatility_damage_done", "versatility_healing_done", "versatility_damage_reduction":
			values[key] = value
		}
	}
	if len(values) == 0 {
		return nil
	}
	return &CombatantStats{ActorID: actorID, ObservedAtMS: stats.ObservedAtMS, Values: values}
}

func rate(total float64, durationMS int64) *float64 {
	value := total / (float64(durationMS) / 1000)
	if !validAmount(value) {
		return nil
	}
	return &value
}

func selectTimeline(events []Event, actorID int) ([]Event, bool) {
	if len(events) <= MaxTimeline {
		return events, false
	}
	selected := map[int]bool{}
	deaths := 0
	// Retain the latest ten deaths with up to twenty preceding observations
	// from ten seconds before each death, then fill with the earliest events.
	// This keeps evidence available even when an early cast sequence is long.
	for i := len(events) - 1; i >= 0 && deaths < 10; i-- {
		event := events[i]
		if event.Kind != "death" || event.TargetID == nil || *event.TargetID != actorID {
			continue
		}
		deaths++
		selected[i] = true
		for j := i - 1; j >= 0 && i-j <= 20 && event.TimestampMS-events[j].TimestampMS <= 10000; j-- {
			selected[j] = true
		}
	}
	for i := 0; i < len(events) && len(selected) < MaxTimeline; i++ {
		selected[i] = true
	}
	indexes := make([]int, 0, len(selected))
	for i := range selected {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	out := make([]Event, 0, len(indexes))
	for _, i := range indexes {
		out = append(out, events[i])
	}
	return out, true
}

func add(total *float64, value *float64) bool {
	if value == nil || !validAmount(*value) || *total+*value > maxSafe {
		return false
	}
	*total += *value
	return true
}
func validAmount(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= maxSafe
}
func sumPointer(total, value *float64, known *bool) *float64 {
	if total == nil {
		zero := 0.0
		total = &zero
	}
	if !add(total, value) {
		*known = false
	}
	return total
}
