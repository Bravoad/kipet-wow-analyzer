// Package combatfixture supplies explicitly synthetic combat observations.
package combatfixture

import (
	"context"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/combat"
)

const Code = "DEMOCombat000001"

type Provider struct{}

func New() *Provider { return &Provider{} }

func (*Provider) Report(ctx context.Context, code string) (combat.Report, error) {
	if err := ctx.Err(); err != nil {
		return combat.Report{}, character.NewFault("PROVIDER_UNAVAILABLE", true)
	}
	if !combat.ValidCode(code) {
		return combat.Report{}, character.NewFault("INVALID_ARGUMENT", false)
	}
	if code != Code {
		return combat.Report{}, character.NewFault("REPORT_UNAVAILABLE", false)
	}
	start := time.Date(2026, 10, 31, 18, 0, 0, 0, time.UTC)
	end := start.Add(4 * time.Minute)
	return combat.Report{Code: Code, Title: str("Synthetic · Combat laboratory"), StartTime: &start, EndTime: &end, Visibility: "public", Fights: []combat.Fight{
		{ID: 1, Name: str("Synthetic · Training Sentinel"), EncounterID: str("900006100"), Difficulty: num(14), Kill: flag(true), ParticipantIDs: []int{1, 2}, StartMS: 0, EndMS: 120000},
		{ID: 2, Name: str("Synthetic · Interrupted observation"), EncounterID: str("900006101"), Difficulty: num(14), Kill: flag(false), ParticipantIDs: []int{1, 2}, StartMS: 180000, EndMS: 240000},
	}, Actors: []combat.Actor{{ID: 1, Name: str("Кипет · Synthetic"), Type: "player", SubType: str("DeathKnight")}, {ID: 2, Name: str("Synthetic Healer"), Type: "player", SubType: str("Priest")}}}, nil
}

func (p *Provider) Collect(ctx context.Context, report combat.Report, fightID, actorID int) (combat.Collection, error) {
	// Rebuild from the canonical synthetic report, never trust caller-provided
	// fight durations or actors as a way to fabricate another demo.
	canonical, err := p.Report(ctx, report.Code)
	if err != nil {
		return combat.Collection{}, err
	}
	if _, _, err = combat.Select(canonical, fightID, actorID); err != nil {
		return combat.Collection{}, err
	}
	out := combat.Collection{Report: canonical, FightID: fightID, ActorID: actorID, Complete: true, Events: []combat.Event{}}
	if fightID == 2 {
		if actorID == 1 {
			out.Complete = false
			out.Events = []combat.Event{{TimestampMS: 181000, Kind: "cast", SourceID: num(1), TargetID: num(100), AbilityID: str("900006001")}, {TimestampMS: 181500, Kind: "damage", SourceID: num(1), TargetID: num(100), AbilityID: str("900006001"), Amount: amount(90000)}}
		}
		return out, nil
	}
	if actorID == 1 {
		out.CombatantStats = &combat.CombatantStats{
			ActorID: actorID, ObservedAtMS: 0,
			Values: map[string]float64{
				"strength": 840, "agility": 0, "stamina": 1720,
				"critical_strike_melee": 232, "haste_melee": 287, "mastery": 368, "versatility_damage_done": 149,
			},
		}
	}
	out.Events = []combat.Event{
		{TimestampMS: 1000, Kind: "cast", SourceID: num(1), TargetID: num(100), AbilityID: str("900006001")},
		{TimestampMS: 1500, Kind: "damage", SourceID: num(1), TargetID: num(100), AbilityID: str("900006001"), Amount: amount(120000)},
		{TimestampMS: 2000, Kind: "cast", SourceID: num(2), TargetID: num(1), AbilityID: str("900006003")},
		{TimestampMS: 2500, Kind: "heal", SourceID: num(2), TargetID: num(1), AbilityID: str("900006003"), Amount: amount(60000), Overheal: amount(10000)},
		{TimestampMS: 3000, Kind: "cast", SourceID: num(1), TargetID: num(100), AbilityID: str("900006001")},
		{TimestampMS: 3500, Kind: "damage", SourceID: num(1), TargetID: num(100), AbilityID: str("900006001"), Amount: amount(180000)},
		{TimestampMS: 5000, Kind: "applybuff", SourceID: num(2), TargetID: num(1), AbilityID: str("900006004")},
		{TimestampMS: 5200, Kind: "cast", SourceID: num(2), TargetID: num(1), AbilityID: str("900006003")},
		{TimestampMS: 5500, Kind: "heal", SourceID: num(2), TargetID: num(1), AbilityID: str("900006003"), Amount: amount(90000), Overheal: amount(30000)},
		{TimestampMS: 11500, Kind: "cast", SourceID: num(1), TargetID: num(100), AbilityID: str("900006002")},
		{TimestampMS: 12000, Kind: "damage", SourceID: num(1), TargetID: num(100), AbilityID: str("900006002"), Amount: amount(90000)},
		{TimestampMS: 45000, Kind: "interrupt", SourceID: num(1), TargetID: num(100), AbilityID: str("900006005")},
		{TimestampMS: 65000, Kind: "removebuff", SourceID: num(2), TargetID: num(1), AbilityID: str("900006004")},
		{TimestampMS: 85000, Kind: "damage", SourceID: num(100), TargetID: num(1), AbilityID: str("900006006"), Amount: amount(50000)},
		{TimestampMS: 90000, Kind: "death", SourceID: num(100), TargetID: num(1)},
	}
	return out, nil
}
func str(v string) *string      { return &v }
func num(v int) *int            { return &v }
func amount(v float64) *float64 { return &v }
func flag(v bool) *bool         { return &v }
