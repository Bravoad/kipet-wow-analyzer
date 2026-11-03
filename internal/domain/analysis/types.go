// Package analysis evaluates normalized immutable snapshots without external I/O.
package analysis

import (
	"time"

	"kipet-wow-analyzer/internal/domain/character"
)

const (
	GeneralPVE = "general_pve"
	Raid       = "raid"
	MythicPlus = "mythic_plus"
)

type Input struct {
	Snapshot character.Snapshot
	Source   string
	Content  string
	Now      time.Time
}

type Context struct {
	Patch            *string `json:"patch"`
	Season           *string `json:"season"`
	ClassID          *string `json:"class_id"`
	SpecializationID *string `json:"specialization_id"`
	Content          string  `json:"content"`
}

type Reference struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

type RulesetInfo struct {
	Version    string      `json:"version"`
	Digest     string      `json:"digest"`
	Synthetic  bool        `json:"synthetic"`
	ReviewedAt time.Time   `json:"reviewed_at"`
	ReviewedBy string      `json:"reviewed_by"`
	References []Reference `json:"references"`
}

type Evidence struct {
	Section   string     `json:"section"`
	Path      string     `json:"path"`
	Value     any        `json:"value"`
	FetchedAt *time.Time `json:"fetched_at"`
	Reference *string    `json:"reference"`
}

type Result struct {
	RuleID           string         `json:"rule_id"`
	RuleVersion      string         `json:"rule_version"`
	Applicability    string         `json:"applicability"`
	Outcome          *string        `json:"outcome"`
	Confidence       string         `json:"confidence"`
	ConfidenceReason string         `json:"confidence_reason"`
	ReasonCode       string         `json:"reason_code"`
	MessageKey       string         `json:"message_key"`
	Params           map[string]any `json:"params"`
	Evidence         []Evidence     `json:"evidence"`
}

type Diagnostic struct {
	Code       string         `json:"code"`
	Section    *string        `json:"section"`
	MessageKey string         `json:"message_key"`
	Params     map[string]any `json:"params"`
	Evidence   []Evidence     `json:"evidence"`
}

type Summary struct {
	OK            int `json:"ok"`
	Info          int `json:"info"`
	Warning       int `json:"warning"`
	Critical      int `json:"critical"`
	Unknown       int `json:"unknown"`
	NotApplicable int `json:"not_applicable"`
}

type Report struct {
	SnapshotID  string       `json:"snapshot_id"`
	Source      string       `json:"source"`
	EvaluatedAt time.Time    `json:"evaluated_at"`
	Context     Context      `json:"context"`
	Ruleset     *RulesetInfo `json:"ruleset"`
	Summary     Summary      `json:"summary"`
	Results     []Result     `json:"results"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// Ruleset is a reviewed configuration, never a claim that the current patch is known.
// A null Season declares a season-independent rule; otherwise the ID must match.
type Ruleset struct {
	Version          string      `json:"version"`
	GameFlavor       string      `json:"game_flavor"`
	Patch            string      `json:"patch"`
	Season           *string     `json:"season"`
	ClassID          string      `json:"class_id"`
	SpecializationID string      `json:"specialization_id"`
	Content          string      `json:"content"`
	Synthetic        bool        `json:"synthetic"`
	Enabled          bool        `json:"enabled"`
	ReviewStatus     string      `json:"review_status"`
	ReviewedAt       time.Time   `json:"reviewed_at"`
	ReviewedBy       string      `json:"reviewed_by"`
	References       []Reference `json:"references"`
	Enchant          EnchantRule `json:"enchant"`
	SetBonus         SetRule     `json:"set_bonus"`
	Talents          TalentRule  `json:"talents"`
}

type RuleMetadata struct {
	Version   string `json:"version"`
	Enabled   bool   `json:"enabled"`
	Reference string `json:"reference"`
}

type EnchantRule struct {
	RuleMetadata
	Slots []EnchantSlot `json:"slots"`
}
type EnchantSlot struct {
	Slot                     string   `json:"slot"`
	ItemIDs                  []string `json:"item_ids"`
	Applicable               bool     `json:"applicable"`
	CompatibleEnchantmentIDs []string `json:"compatible_enchantment_ids"`
}

type SetRule struct {
	RuleMetadata
	Sets []SetDefinition `json:"sets"`
}
type SetDefinition struct {
	SetID        string   `json:"set_id"`
	ItemIDs      []string `json:"item_ids"`
	Thresholds   []int    `json:"thresholds"`
	TargetPieces *int     `json:"target_pieces"`
	// ItemCountOnly must be explicitly reviewed; unknown activation mechanics are not inferred.
	ItemCountOnly bool `json:"item_count_only"`
}

type TalentRule struct {
	RuleMetadata
	TreeHash     string              `json:"tree_hash"`
	Baseline     []TalentNode        `json:"baseline"`
	Alternatives []TalentAlternative `json:"alternatives"`
	Required     []TalentNode        `json:"required"`
}
type TalentNode struct {
	NodeID  string  `json:"node_id"`
	EntryID *string `json:"entry_id"`
	Rank    int     `json:"rank"`
	Tree    string  `json:"tree"`
}
type TalentAlternative struct {
	Baseline TalentNode   `json:"baseline"`
	Allowed  []TalentNode `json:"allowed"`
}

func ValidContent(content string) bool {
	return content == GeneralPVE || content == Raid || content == MythicPlus
}
