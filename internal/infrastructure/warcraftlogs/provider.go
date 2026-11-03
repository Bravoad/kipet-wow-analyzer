package warcraftlogs

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/combat"
)

type object = map[string]any

const reportQuery = `query PublicReport($code: String!) {
 reportData { report(code: $code) { code title startTime endTime visibility
 fights { id name encounterID difficulty kill startTime endTime friendlyPlayers }
 masterData { actors { id name type subType } } } }
 rateLimitData { limitPerHour pointsSpentThisHour pointsResetIn }
}`
const eventsQuery = `query PublicFightEvents($code: String!, $fight: Int!, $actor: Int!, $start: Float!, $end: Float!, $withStats: Boolean!) {
 reportData { report(code: $code) { code visibility
 events(fightIDs: [$fight], startTime: $start, endTime: $end, limit: 2000) { data nextPageTimestamp }
 combatant: events(dataType: CombatantInfo, fightIDs: [$fight], sourceID: $actor, startTime: $start, endTime: $end, limit: 10) @include(if: $withStats) { data } } }
 rateLimitData { limitPerHour pointsSpentThisHour pointsResetIn }
}`
const actorAmountEventsQuery = `query PublicActorAmountEvents($code: String!, $fight: Int!, $actor: Int!, $start: Float!, $end: Float!) {
 reportData { report(code: $code) { code visibility
 events(dataType: All, fightIDs: [$fight], sourceID: $actor, startTime: $start, endTime: $end, limit: 2000, useActorIDs: true) { data nextPageTimestamp } } }
 rateLimitData { limitPerHour pointsSpentThisHour pointsResetIn }
}`

func (c *Client) Report(ctx context.Context, code string) (combat.Report, error) {
	if !combat.ValidCode(code) {
		return combat.Report{}, character.NewFault("INVALID_ARGUMENT", false)
	}
	data, err := c.query(ctx, reportQuery, map[string]any{"code": code})
	if err != nil {
		return combat.Report{}, err
	}
	raw := obj(obj(data["reportData"])["report"])
	if raw == nil {
		return combat.Report{}, character.NewFault("REPORT_UNAVAILABLE", false)
	}
	return normalizeReport(raw, code)
}
func normalizeReport(raw object, code string) (combat.Report, error) {
	if text(raw["code"]) != code {
		return combat.Report{}, invalid()
	}
	if strings.ToLower(text(raw["visibility"])) != "public" {
		return combat.Report{}, character.NewFault("REPORT_UNAVAILABLE", false)
	}
	out := combat.Report{Code: code, Title: label(raw["title"]), Visibility: "public", StartTime: timestamp(raw["startTime"]), EndTime: timestamp(raw["endTime"])}
	if out.StartTime != nil && out.EndTime != nil && out.EndTime.Before(*out.StartTime) {
		return combat.Report{}, invalid()
	}
	if value, present := raw["fights"]; present && value != nil {
		list, ok := value.([]any)
		if !ok || len(list) > 200 {
			return combat.Report{}, invalid()
		}
		out.Fights = make([]combat.Fight, 0, len(list))
		seen := map[int]bool{}
		for _, value := range list {
			entry := obj(value)
			id, start, end := localID(entry["id"]), integer(entry["startTime"]), integer(entry["endTime"])
			if id == nil || seen[*id] || start == nil || end == nil || *end <= *start || *end-*start > 7*24*3600000 {
				return combat.Report{}, invalid()
			}
			seen[*id] = true
			fight := combat.Fight{ID: *id, Name: label(entry["name"]), StartMS: *start, EndMS: *end, EncounterID: providerID(entry["encounterID"]), Difficulty: smallInteger(entry["difficulty"]), Kill: boolean(entry["kill"])}
			if participants, present := entry["friendlyPlayers"]; present && participants != nil {
				list, ok := participants.([]any)
				if !ok || len(list) > 1000 {
					return combat.Report{}, invalid()
				}
				fight.ParticipantIDs = make([]int, 0, len(list))
				seenParticipants := map[int]bool{}
				for _, value := range list {
					id := localID(value)
					if id == nil || seenParticipants[*id] {
						return combat.Report{}, invalid()
					}
					seenParticipants[*id] = true
					fight.ParticipantIDs = append(fight.ParticipantIDs, *id)
				}
				sort.Ints(fight.ParticipantIDs)
			}
			out.Fights = append(out.Fights, fight)
		}
		sort.Slice(out.Fights, func(i, j int) bool { return out.Fights[i].ID < out.Fights[j].ID })
	}
	if value, present := obj(raw["masterData"])["actors"]; present && value != nil {
		list, ok := value.([]any)
		if !ok || len(list) > 1000 {
			return combat.Report{}, invalid()
		}
		out.Actors = make([]combat.Actor, 0, len(list))
		seen := map[int]bool{}
		for _, value := range list {
			entry := obj(value)
			kind := strings.ToLower(text(entry["type"]))
			if entry["id"] == json.Number("-1") && kind == "npc" && text(entry["name"]) == "Environment" {
				continue
			}
			id := localID(entry["id"])
			if id == nil || seen[*id] {
				return combat.Report{}, invalid()
			}
			seen[*id] = true
			switch kind {
			case "player", "pet", "npc":
			default:
				kind = "unknown"
			}
			out.Actors = append(out.Actors, combat.Actor{ID: *id, Name: label(entry["name"]), Type: kind, SubType: label(entry["subType"])})
		}
		sort.Slice(out.Actors, func(i, j int) bool { return out.Actors[i].ID < out.Actors[j].ID })
	}
	return out, nil
}

// Collect keeps incoming damage and deaths in the fight timeline. A capped
// window can use a separate exact-actor stream for outgoing amounts. Every page
// independently requires public visibility, and both streams have hard bounds.
func (c *Client) Collect(ctx context.Context, report combat.Report, fightID, actorID int) (combat.Collection, error) {
	fight, _, err := combat.Select(report, fightID, actorID)
	if err != nil {
		return combat.Collection{}, err
	}
	out := combat.Collection{Report: report, FightID: fightID, ActorID: actorID, Events: []combat.Event{}, Complete: true}
	ordered := true
	finishCapped := func() (combat.Collection, error) {
		out.Complete = false
		if ordered {
			events, err := c.collectActorAmounts(ctx, report.Code, fight, actorID)
			if err != nil {
				return combat.Collection{}, err
			}
			out.ActorAmountEvents = events
		}
		return out, nil
	}
	cursor := fight.StartMS
	for page := 0; page < maxEventPages; page++ {
		raw, err := c.publicEventReport(ctx, report.Code, eventsQuery, map[string]any{
			"code": report.Code, "fight": fightID, "actor": actorID,
			"start": cursor, "end": fight.EndMS, "withStats": page == 0,
		})
		if err != nil {
			return combat.Collection{}, err
		}
		if page == 0 {
			out.CombatantStats = normalizeCombatantStats(obj(raw["combatant"])["data"], fight, actorID)
		}
		paginator := obj(raw["events"])
		list, ok := paginator["data"].([]any)
		if !ok || !validEventPageSize(list) {
			out.Complete = false
			return out, nil
		}
		pageLast, pageOrdered := eventPageLast(list, cursor, fight.EndMS)
		ordered = ordered && pageOrdered
		remaining := maxFightEvents - len(out.Events)
		truncated := len(list) > remaining
		if truncated {
			list = list[:remaining]
		}
		last := cursor - 1
		for _, value := range list {
			event, valid := normalizeEvent(obj(value))
			if !valid {
				out.Complete = false
			}
			if event.TimestampMS < cursor || event.TimestampMS > fight.EndMS {
				out.Complete = false
				continue
			}
			if event.TimestampMS > last {
				last = event.TimestampMS
			}
			out.Events = append(out.Events, event)
		}
		next, present := paginator["nextPageTimestamp"]
		if !present {
			out.Complete = false
			return out, nil
		}
		if truncated {
			if next != nil {
				at := integer(next)
				ordered = ordered && at != nil && *at > pageLast && *at > cursor && *at <= fight.EndMS
			}
			return finishCapped()
		}
		if next == nil {
			return out, nil
		}
		at := integer(next)
		if at == nil || *at <= cursor || *at <= last {
			out.Complete = false
			return out, nil
		}
		if *at > fight.EndMS {
			return out, nil
		}
		if len(out.Events) == maxFightEvents {
			return finishCapped()
		}
		cursor = *at
	}
	return finishCapped()
}

func (c *Client) collectActorAmounts(ctx context.Context, code string, fight combat.Fight, actorID int) ([]combat.Event, error) {
	events := []combat.Event{}
	cursor, read := fight.StartMS, 0
	for page := 0; page < maxEventPages; page++ {
		raw, err := c.publicEventReport(ctx, code, actorAmountEventsQuery, map[string]any{
			"code": code, "fight": fight.ID, "actor": actorID, "start": cursor, "end": fight.EndMS,
		})
		if err != nil {
			return nil, err
		}
		paginator := obj(raw["events"])
		list, ok := paginator["data"].([]any)
		if !ok || !validEventPageSize(list) || read+len(list) > maxFightEvents {
			return nil, nil
		}
		last, ordered := eventPageLast(list, cursor, fight.EndMS)
		if !ordered {
			return nil, nil
		}
		read += len(list)
		for _, value := range list {
			row := obj(value)
			if rawFight, exists := row["fight"]; exists {
				id := localID(rawFight)
				if id == nil || *id != fight.ID {
					return nil, nil
				}
			}
			kind := text(row["type"])
			if kind == "" {
				return nil, nil
			}
			if kind != "damage" && kind != "heal" {
				continue
			}
			source := localID(row["sourceID"])
			if source == nil {
				return nil, nil
			}
			// sourceID may also return pet events. Only the exact actor
			// contributes, and unknown amounts remain unknown.
			if *source == actorID {
				event, _ := normalizeEvent(row)
				events = append(events, event)
			}
		}
		next, present := paginator["nextPageTimestamp"]
		if !present {
			return nil, nil
		}
		if next == nil {
			return events, nil
		}
		at := integer(next)
		if at == nil || *at <= cursor || *at <= last || *at > fight.EndMS || read == maxFightEvents {
			return nil, nil
		}
		cursor = *at
	}
	return nil, nil
}

func (c *Client) publicEventReport(ctx context.Context, code, query string, variables map[string]any) (object, error) {
	data, err := c.query(ctx, query, variables)
	if err != nil {
		return nil, err
	}
	raw := obj(obj(data["reportData"])["report"])
	if raw == nil || strings.ToLower(text(raw["visibility"])) != "public" {
		return nil, character.NewFault("REPORT_UNAVAILABLE", false)
	}
	if text(raw["code"]) != code {
		return nil, invalid()
	}
	return raw, nil
}

const maxEventPages = 5
const eventPageSize = 2000
const maxFightEvents = 10000

func eventPageLast(list []any, start, end int64) (int64, bool) {
	last := start
	for _, value := range list {
		at := integer(obj(value)["timestamp"])
		if at == nil || *at < last || *at > end {
			return 0, false
		}
		last = *at
	}
	return last, true
}

// Warcraft Logs can extend a page to finish events sharing the last timestamp.
func validEventPageSize(list []any) bool {
	if len(list) <= eventPageSize {
		return true
	}
	if len(list) > eventPageSize+256 {
		return false
	}
	boundary := integer(obj(list[eventPageSize-1])["timestamp"])
	if boundary == nil {
		return false
	}
	for _, value := range list[eventPageSize:] {
		at := integer(obj(value)["timestamp"])
		if at == nil || *at != *boundary {
			return false
		}
	}
	return true
}

func normalizeEvent(raw object) (combat.Event, bool) {
	event := combat.Event{Kind: "unknown"}
	valid := true
	at := integer(raw["timestamp"])
	if at == nil {
		return event, false
	}
	event.TimestampMS = *at
	switch kind := text(raw["type"]); kind {
	case "damage", "heal", "cast", "interrupt", "death", "applybuff", "refreshbuff", "removebuff":
		event.Kind = kind
	case "combatantinfo", "begincast", "summon", "resourcechange", "energize", "absorbed", "healabsorbed", "dispel", "applydebuff", "refreshdebuff", "removedebuff", "applybuffstack", "removebuffstack", "applydebuffstack", "removedebuffstack", "extraattacks", "encounterstart", "encounterend", "zonechange", "worldmarkerplaced", "worldmarkerremoved":
		event.Kind = "other"
	default:
		valid = false
	}
	event.SourceID = localID(raw["sourceID"])
	event.TargetID = localID(raw["targetID"])
	if event.Kind == "death" && event.TargetID == nil {
		valid = false
	}
	if (event.Kind == "damage" || event.Kind == "heal" || event.Kind == "cast" || event.Kind == "interrupt") && event.SourceID == nil {
		valid = false
	}
	event.AbilityID = providerID(raw["abilityGameID"])
	if event.AbilityID == nil {
		event.AbilityID = providerID(obj(raw["ability"])["guid"])
	}
	if event.Kind == "damage" || event.Kind == "heal" {
		event.Amount = amount(raw["amount"])
		if event.Amount == nil {
			valid = false
		}
	}
	if event.Kind == "heal" {
		event.Overheal = amount(raw["overheal"])
	}
	return event, valid
}
func obj(value any) object  { out, _ := value.(map[string]any); return out }
func text(value any) string { out, _ := value.(string); return out }
func label(value any) *string {
	v := text(value)
	if strings.TrimSpace(v) == "" || len([]rune(v)) > 200 {
		return nil
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return nil
		}
	}
	return &v
}
func integer(value any) *int64 {
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
func localID(value any) *int {
	n := integer(value)
	if n == nil || *n < 1 || *n > math.MaxInt32 {
		return nil
	}
	v := int(*n)
	return &v
}
func smallInteger(value any) *int {
	n := integer(value)
	if n == nil || *n > 1000 {
		return nil
	}
	v := int(*n)
	return &v
}
func providerID(value any) *string {
	n, ok := value.(json.Number)
	if !ok {
		return nil
	}
	v, err := n.Int64()
	if err != nil || v < 1 {
		return nil
	}
	id := strconv.FormatInt(v, 10)
	return &id
}
func amount(value any) *float64 {
	n, ok := value.(json.Number)
	if !ok {
		return nil
	}
	v, err := n.Float64()
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 9007199254740991 {
		return nil
	}
	return &v
}
func boolean(value any) *bool {
	v, ok := value.(bool)
	if !ok {
		return nil
	}
	return &v
}
func timestamp(value any) *time.Time {
	n := integer(value)
	if n == nil || *n < 1 || *n > 253402300799999 {
		return nil
	}
	at := time.UnixMilli(*n).UTC()
	return &at
}
