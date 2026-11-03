// Package progress contains normalized observations, never inferred player scores.
package progress

import "time"

type NamedID struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}

type Season struct {
	ID        string  `json:"id"`
	Name      *string `json:"name"`
	IsCurrent *bool   `json:"is_current"`
}

type Run struct {
	Dungeon       NamedID    `json:"dungeon"`
	KeystoneLevel int        `json:"keystone_level"`
	DurationMS    *int64     `json:"duration_ms"`
	CompletedAt   *time.Time `json:"completed_at"`
	WithinTime    *bool      `json:"within_time"`
	Rating        *float64   `json:"rating"`
}

type MythicPlus struct {
	CharacterID      string   `json:"character_id"`
	Season           *Season  `json:"season"`
	Rating           *float64 `json:"rating"`
	RunScope         string   `json:"run_scope"`
	BestRuns         []Run    `json:"best_runs"`
	AvailableSeasons []Season `json:"available_seasons"`
}

type Encounter struct {
	ID             string     `json:"id"`
	Name           *string    `json:"name"`
	CompletedCount *int64     `json:"completed_count"`
	LastKillAt     *time.Time `json:"last_kill_at"`
}

type RaidMode struct {
	Difficulty string      `json:"difficulty"`
	Status     string      `json:"status"`
	Completed  *int64      `json:"completed"`
	Total      *int64      `json:"total"`
	Encounters []Encounter `json:"encounters"`
}

type RaidInstance struct {
	ID    string     `json:"id"`
	Name  *string    `json:"name"`
	Modes []RaidMode `json:"modes"`
}

type Expansion struct {
	ID        string         `json:"id"`
	Name      *string        `json:"name"`
	Instances []RaidInstance `json:"instances"`
}

type Raids struct {
	CharacterID string      `json:"character_id"`
	Expansions  []Expansion `json:"expansions"`
}
