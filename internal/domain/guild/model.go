// Package guild describes source rosters and explicitly bounded observations.
package guild

import (
	"encoding/json"
	"time"
)

type Key struct{ Region, Realm, Slug string }
type CharacterKey struct {
	Region string `json:"region"`
	Realm  string `json:"realm"`
	Name   string `json:"name"`
}
type NamedID struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}
type Profile struct {
	ID          string         `json:"id"`
	ProviderID  string         `json:"provider_id"`
	Region      string         `json:"region"`
	Realm       map[string]any `json:"realm"`
	Slug        string         `json:"slug"`
	Name        string         `json:"name"`
	Faction     string         `json:"faction"`
	MemberCount *int           `json:"member_count"`
	CreatedAt   *time.Time     `json:"created_at"`
}
type Member struct {
	Key        CharacterKey `json:"key"`
	ProviderID string       `json:"provider_id"`
	Class      *NamedID     `json:"character_class"`
	Level      *int         `json:"level"`
	Rank       *int         `json:"rank"`
}
type Roster struct {
	GuildID       string   `json:"guild_id"`
	Members       []Member `json:"members"`
	ReportedCount *int     `json:"reported_count"`
}
type Bundle struct {
	Profile Profile `json:"profile"`
	Roster  Roster  `json:"roster"`
}

// Observation is already authorized and deadline-filtered by the application.
// Missing profiles or sections remain nil, never invented empty collections.
type Observation struct {
	Member     Member
	SnapshotID *string
	Status     string
	Profile    json.RawMessage
	Sections   map[string]json.RawMessage
	Notices    *int
}
type Distribution struct {
	ID    string  `json:"id"`
	Name  *string `json:"name"`
	Count int     `json:"count"`
}
type Coverage struct {
	RosterKnown bool           `json:"roster_known"`
	Reported    *int           `json:"reported"`
	Roster      int            `json:"roster"`
	Profiles    int            `json:"profiles"`
	Fresh       int            `json:"fresh"`
	Stale       int            `json:"stale"`
	Unavailable int            `json:"unavailable"`
	Pending     int            `json:"pending"`
	Sections    map[string]int `json:"sections"`
}
type MemberSummary struct {
	Member         Member   `json:"member"`
	Status         string   `json:"status"`
	SnapshotID     *string  `json:"snapshot_id"`
	Specialization *NamedID `json:"specialization"`
	Role           *string  `json:"role"`
	ItemLevel      *float64 `json:"item_level"`
	MythicRating   *float64 `json:"mythic_rating"`
	MythicSeason   *string  `json:"mythic_season"`
	Notices        *int     `json:"notices"`
}
type Analysis struct {
	GuildID         string         `json:"guild_id"`
	Coverage        Coverage       `json:"coverage"`
	Classes         []Distribution `json:"classes"`
	Specializations []Distribution `json:"specializations"`
	Roles           []Distribution `json:"roles"`
	ItemLevel       struct {
		Count  int      `json:"count"`
		Mean   *float64 `json:"mean"`
		Median *float64 `json:"median"`
	} `json:"item_level"`
	Gear struct {
		KnownEnchantments   int `json:"known_enchantments"`
		Enchanted           int `json:"enchanted"`
		EmptyEnchantments   int `json:"empty_enchantments"`
		UnknownEnchantments int `json:"unknown_enchantments"`
		KnownSets           int `json:"known_sets"`
	} `json:"gear"`
	Progress struct {
		MythicProfiles int `json:"mythic_profiles"`
		RaidProfiles   int `json:"raid_profiles"`
	} `json:"progress"`
	Members     []MemberSummary `json:"members"`
	Limitations []string        `json:"limitations"`
}
