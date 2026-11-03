// Package raiderio models observations returned by Raider.IO, independently of
// Blizzard profile identifiers and the synthetic demonstration dataset.
package raiderio

import "time"

type Key struct {
	Region string `json:"region"`
	Realm  string `json:"realm"`
	Name   string `json:"name"`
	Kind   string `json:"-"`
}
type Realm struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}
type Coverage struct {
	State           string     `json:"state"`
	Reason          *string    `json:"reason"`
	SourceUpdatedAt *time.Time `json:"source_updated_at"`
}
type Detail struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}
type Item struct {
	Slot         string   `json:"slot"`
	ItemID       string   `json:"item_id"`
	Name         *string  `json:"name"`
	ItemLevel    *float64 `json:"item_level"`
	Quality      *int     `json:"quality"`
	EnchantIDs   []string `json:"enchant_ids"`
	Enchantments []Detail `json:"enchantments"`
	GemIDs       []string `json:"gem_ids"`
	BonusIDs     []string `json:"bonus_ids"`
}
type Gear struct {
	EquippedItemLevel *float64   `json:"equipped_item_level"`
	TotalItemLevel    *float64   `json:"total_item_level"`
	UpdatedAt         *time.Time `json:"updated_at"`
	Items             []Item     `json:"items"`
}
type Talents struct {
	ExportCode       *string `json:"export_code"`
	SpecializationID *string `json:"specialization_id"`
}
type Run struct {
	Dungeon     string     `json:"dungeon"`
	Level       int        `json:"level"`
	DurationMS  *int64     `json:"duration_ms"`
	ParTimeMS   *int64     `json:"par_time_ms"`
	Timed       *bool      `json:"timed"`
	Score       *float64   `json:"score"`
	CompletedAt *time.Time `json:"completed_at"`
	URL         *string    `json:"url"`
}
type MythicPlus struct {
	Season   *string             `json:"season"`
	Rating   *float64            `json:"rating"`
	Scores   map[string]*float64 `json:"scores"`
	BestRuns []Run               `json:"best_runs"`
}
type Raid struct {
	Slug        string  `json:"slug"`
	ExpansionID *string `json:"expansion_id"`
	Summary     *string `json:"summary"`
	TotalBosses *int    `json:"total_bosses"`
	Normal      *int    `json:"normal_bosses_killed"`
	Heroic      *int    `json:"heroic_bosses_killed"`
	Mythic      *int    `json:"mythic_bosses_killed"`
}
type Profile struct {
	Name           string              `json:"name"`
	Region         string              `json:"region"`
	Realm          Realm               `json:"realm"`
	ProfileURL     string              `json:"profile_url"`
	Race           *string             `json:"race"`
	Class          *string             `json:"class"`
	Specialization *string             `json:"specialization"`
	Role           *string             `json:"role"`
	Faction        *string             `json:"faction"`
	LastCrawledAt  *time.Time          `json:"last_crawled_at"`
	Gear           *Gear               `json:"gear"`
	Talents        *Talents            `json:"talents"`
	MythicPlus     *MythicPlus         `json:"mythic_plus"`
	Raids          []Raid              `json:"raids"`
	Coverage       map[string]Coverage `json:"coverage"`
	Guild          *GuildLink          `json:"guild,omitempty"`
}
type Snapshot struct {
	Profile                           Profile
	Guild                             *GuildProfile
	FetchedAt, FreshUntil, ValidUntil time.Time
}

type GuildLink struct {
	Name  string `json:"name"`
	Realm string `json:"realm"`
}

type GuildMember struct {
	Key            Key     `json:"key"`
	Rank           *int    `json:"rank"`
	Class          *string `json:"class"`
	Specialization *string `json:"specialization"`
	Role           *string `json:"role"`
}

type Rank struct {
	World  *int `json:"world"`
	Region *int `json:"region"`
	Realm  *int `json:"realm"`
}

type GuildProfile struct {
	Name          string                     `json:"name"`
	Region        string                     `json:"region"`
	Realm         Realm                      `json:"realm"`
	ProfileURL    string                     `json:"profile_url"`
	Faction       *string                    `json:"faction"`
	LastCrawledAt *time.Time                 `json:"last_crawled_at"`
	Members       []GuildMember              `json:"members"`
	Raids         []Raid                     `json:"raids"`
	Rankings      map[string]map[string]Rank `json:"rankings"`
	Coverage      map[string]Coverage        `json:"coverage"`
}
