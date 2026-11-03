// Package combat describes public report observations and explicit calculations.
package combat

import "time"

type Actor struct {
	ID      int     `json:"id"`
	Name    *string `json:"name"`
	Type    string  `json:"type"`
	SubType *string `json:"sub_type"`
}
type Fight struct {
	ID             int     `json:"id"`
	Name           *string `json:"name"`
	EncounterID    *string `json:"encounter_id"`
	Difficulty     *int    `json:"difficulty"`
	Kill           *bool   `json:"kill"`
	ParticipantIDs []int   `json:"participant_ids"`
	StartMS        int64   `json:"start_ms"`
	EndMS          int64   `json:"end_ms"`
}
type Report struct {
	Code       string     `json:"code"`
	Title      *string    `json:"title"`
	StartTime  *time.Time `json:"start_time"`
	EndTime    *time.Time `json:"end_time"`
	Visibility string     `json:"visibility"`
	Fights     []Fight    `json:"fights"`
	Actors     []Actor    `json:"actors"`
}
type Event struct {
	TimestampMS int64    `json:"timestamp_ms"`
	Kind        string   `json:"kind"`
	SourceID    *int     `json:"source_id"`
	TargetID    *int     `json:"target_id"`
	AbilityID   *string  `json:"ability_id"`
	Amount      *float64 `json:"amount"`
	Overheal    *float64 `json:"overheal"`
}

// CombatantStats contains the values observed together in one report event.
type CombatantStats struct {
	ActorID      int                `json:"-"`
	ObservedAtMS int64              `json:"observed_at_ms"`
	Values       map[string]float64 `json:"values"`
}

// Complete describes the fight-wide event stream. ActorAmountEvents is set
// only for an independently completed stream of the selected actor's outgoing
// damage and healing. A nil slice conveys that coverage was not established.
type Collection struct {
	Report            Report
	FightID, ActorID  int
	Events            []Event
	Complete          bool
	ActorAmountEvents []Event
	CombatantStats    *CombatantStats
}
type Metrics struct {
	DamageTotal     *float64 `json:"damage_total"`
	HealingTotal    *float64 `json:"healing_total"`
	DPS             *float64 `json:"dps"`
	HPS             *float64 `json:"hps"`
	OverhealTotal   *float64 `json:"overheal_total"`
	OverhealPercent *float64 `json:"overheal_percent"`
	Casts           *int     `json:"casts"`
	Interrupts      *int     `json:"interrupts"`
	Deaths          *int     `json:"deaths"`
}
type Ability struct {
	ID      string   `json:"id"`
	Casts   int      `json:"casts"`
	Damage  *float64 `json:"damage"`
	Healing *float64 `json:"healing"`
}
type Finding struct {
	Code         string `json:"code"`
	Outcome      string `json:"outcome"`
	EventIndexes []int  `json:"event_indexes"`
}
type Analysis struct {
	ReportCode                string          `json:"report_code"`
	FightID                   int             `json:"fight_id"`
	ActorID                   int             `json:"actor_id"`
	FightStartMS              int64           `json:"fight_start_ms"`
	FightEndMS                int64           `json:"fight_end_ms"`
	DurationMS                int64           `json:"duration_ms"`
	Method                    string          `json:"method"`
	Source                    string          `json:"source"`
	EventsComplete            bool            `json:"events_complete"`
	ActorAmountEventsComplete bool            `json:"actor_amount_events_complete,omitempty"`
	TimelineTruncated         bool            `json:"timeline_truncated"`
	Metrics                   Metrics         `json:"metrics"`
	CombatantStats            *CombatantStats `json:"combatant_stats,omitempty"`
	Abilities                 []Ability       `json:"abilities"`
	Timeline                  []Event         `json:"timeline"`
	Findings                  []Finding       `json:"findings"`
}
