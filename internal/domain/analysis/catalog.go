package analysis

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const EngineVersion = "1.0.0"
const maxRulesetBytes = 512 << 10

type catalogEntry struct {
	rules  Ruleset
	digest string
}
type Engine struct {
	entries []catalogEntry
	digest  string
}

// New owns a deep copy of the catalog. Mutation by a caller cannot change its digest.
func New(rulesets []Ruleset) (*Engine, error) {
	engine := &Engine{entries: []catalogEntry{}}
	for _, rule := range rulesets {
		if err := validateRuleset(rule); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(rule)
		if err != nil {
			return nil, err
		}
		var copy Ruleset
		if err = json.Unmarshal(encoded, &copy); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(encoded)
		engine.entries = append(engine.entries, catalogEntry{rules: copy, digest: hex.EncodeToString(sum[:])})
	}
	sort.Slice(engine.entries, func(i, j int) bool { return engine.entries[i].digest < engine.entries[j].digest })
	for i, a := range engine.entries {
		for _, b := range engine.entries[i+1:] {
			if contextBase(a.rules) == contextBase(b.rules) && a.rules.Synthetic == b.rules.Synthetic && seasonsOverlap(a.rules.Season, b.rules.Season) && ((a.rules.Enabled && b.rules.Enabled) || a.rules.Version == b.rules.Version) {
				return nil, fmt.Errorf("ambiguous or duplicate ruleset context")
			}
		}
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("kipet-analysis:" + EngineVersion + "\n"))
	for _, entry := range engine.entries {
		_, _ = hash.Write([]byte(entry.digest + "\n"))
	}
	engine.digest = hex.EncodeToString(hash.Sum(nil))
	return engine, nil
}

func (e *Engine) CatalogDigest() string { return e.digest }

// DecodeRuleset rejects unknown fields, duplicate keys, trailing JSON and oversized input.
func DecodeRuleset(reader io.Reader) (Ruleset, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxRulesetBytes+1))
	if err != nil {
		return Ruleset{}, err
	}
	if len(data) > maxRulesetBytes {
		return Ruleset{}, fmt.Errorf("ruleset exceeds size limit")
	}
	if err = uniqueJSONKeys(json.NewDecoder(bytes.NewReader(data)), 0); err != nil {
		return Ruleset{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var rule Ruleset
	if err = decoder.Decode(&rule); err != nil {
		return rule, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return rule, fmt.Errorf("trailing ruleset content")
	}
	if err = validateRuleset(rule); err != nil {
		return rule, err
	}
	return rule, nil
}

func uniqueJSONKeys(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("ruleset nesting limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate ruleset property")
			}
			seen[name] = true
			if err = uniqueJSONKeys(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err = uniqueJSONKeys(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("invalid ruleset delimiter")
	}
	_, err = decoder.Token()
	return err
}

func validateRuleset(rule Ruleset) error {
	if !bounded(rule.Version, 80) || rule.GameFlavor != "retail" || !bounded(rule.Patch, 64) || !positiveID(rule.ClassID) || !positiveID(rule.SpecializationID) || !ValidContent(rule.Content) {
		return fmt.Errorf("invalid ruleset identity or context")
	}
	if rule.Season != nil && !positiveID(*rule.Season) {
		return fmt.Errorf("invalid ruleset season")
	}
	if rule.Synthetic && !strings.HasPrefix(rule.Patch, "synthetic-") {
		return fmt.Errorf("synthetic ruleset requires synthetic patch namespace")
	}
	if rule.ReviewStatus != "approved" && rule.ReviewStatus != "draft" {
		return fmt.Errorf("invalid review status")
	}
	if rule.Enabled && (rule.ReviewStatus != "approved" || rule.ReviewedAt.IsZero() || !bounded(rule.ReviewedBy, 200) || len(rule.References) == 0) {
		return fmt.Errorf("enabled ruleset lacks approval provenance")
	}
	references := map[string]bool{}
	for _, ref := range rule.References {
		if !bounded(ref.ID, 80) || !bounded(ref.Title, 300) || references[ref.ID] {
			return fmt.Errorf("invalid or duplicate reference")
		}
		references[ref.ID] = true
		u, err := url.Parse(ref.URL)
		if err != nil || len(ref.URL) > 2048 || !(u.Scheme == "https" && u.Host != "" && u.User == nil || rule.Synthetic && u.Scheme == "urn" && strings.HasPrefix(ref.URL, "urn:kipet:synthetic:")) {
			return fmt.Errorf("invalid reference URL")
		}
	}
	for _, metadata := range []RuleMetadata{rule.Enchant.RuleMetadata, rule.SetBonus.RuleMetadata, rule.Talents.RuleMetadata} {
		if !bounded(metadata.Version, 80) || metadata.Enabled && !references[metadata.Reference] {
			return fmt.Errorf("rule lacks version or reference")
		}
	}
	seenSlots := map[string]bool{}
	for _, slot := range rule.Enchant.Slots {
		if !bounded(slot.Slot, 64) || seenSlots[slot.Slot] || len(slot.ItemIDs) == 0 || !uniqueIDs(slot.ItemIDs) || slot.Applicable && (len(slot.CompatibleEnchantmentIDs) == 0 || !uniqueIDs(slot.CompatibleEnchantmentIDs)) || !slot.Applicable && len(slot.CompatibleEnchantmentIDs) > 0 {
			return fmt.Errorf("invalid enchant applicability matrix")
		}
		seenSlots[slot.Slot] = true
	}
	seenSets := map[string]bool{}
	for _, set := range rule.SetBonus.Sets {
		if !positiveID(set.SetID) || seenSets[set.SetID] || len(set.ItemIDs) == 0 || !uniqueIDs(set.ItemIDs) || len(set.Thresholds) == 0 {
			return fmt.Errorf("invalid set definition")
		}
		seenSets[set.SetID] = true
		previous := 0
		for _, threshold := range set.Thresholds {
			if threshold <= previous || threshold > len(set.ItemIDs) {
				return fmt.Errorf("invalid set thresholds")
			}
			previous = threshold
		}
		if set.TargetPieces != nil && (*set.TargetPieces < 1 || *set.TargetPieces > len(set.ItemIDs)) {
			return fmt.Errorf("invalid set target")
		}
	}
	if rule.Talents.Enabled && (!bounded(rule.Talents.TreeHash, 200) || len(rule.Talents.Baseline) == 0) {
		return fmt.Errorf("enabled talent rule lacks tree or baseline")
	}
	if !validNodes(rule.Talents.Baseline) || !validNodes(rule.Talents.Required) {
		return fmt.Errorf("invalid talent nodes")
	}
	baseline := map[string]bool{}
	baselineIdentities := map[string]bool{}
	for _, node := range rule.Talents.Baseline {
		baseline[nodeKey(node)] = true
		baselineIdentities[node.Tree+":"+node.NodeID] = true
	}
	seenAlternatives := map[string]bool{}
	owners := map[string]string{}
	for _, alternative := range rule.Talents.Alternatives {
		key := nodeKey(alternative.Baseline)
		if !baseline[key] || seenAlternatives[key] || len(alternative.Allowed) == 0 || !validAlternativeNodes(alternative.Allowed) {
			return fmt.Errorf("invalid talent alternatives")
		}
		seenAlternatives[key] = true
		baseIdentity := alternative.Baseline.Tree + ":" + alternative.Baseline.NodeID
		for _, allowed := range alternative.Allowed {
			identity := allowed.Tree + ":" + allowed.NodeID
			if baselineIdentities[identity] && identity != baseIdentity {
				return fmt.Errorf("talent alternative overlaps another baseline node")
			}
			if owner, exists := owners[identity]; exists && owner != key {
				return fmt.Errorf("talent alternative shared across baseline groups")
			}
			owners[identity] = key
		}
	}
	return nil
}

func positiveID(id string) bool {
	if id == "" || id[0] == '0' {
		return false
	}
	n, err := strconv.ParseInt(id, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == id
}
func bounded(value string, max int) bool {
	return strings.TrimSpace(value) == value && value != "" && len(value) <= max
}
func uniqueIDs(ids []string) bool {
	seen := map[string]bool{}
	for _, id := range ids {
		if !positiveID(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func validNodes(nodes []TalentNode) bool {
	seen := map[string]bool{}
	for _, node := range nodes {
		key := node.Tree + ":" + node.NodeID
		if !positiveID(node.NodeID) || node.EntryID != nil && !positiveID(*node.EntryID) || node.Rank < 1 || node.Rank > 100 || node.Tree != "class" && node.Tree != "specialization" && node.Tree != "hero" || seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}
func nodeKey(node TalentNode) string {
	entry := ""
	if node.EntryID != nil {
		entry = *node.EntryID
	}
	return node.Tree + ":" + node.NodeID + ":" + entry + ":" + strconv.Itoa(node.Rank)
}

func validAlternativeNodes(nodes []TalentNode) bool {
	seen := map[string]bool{}
	for _, node := range nodes {
		key := nodeKey(node)
		if !validNodes([]TalentNode{node}) || seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}
func contextBase(rule Ruleset) string {
	return rule.GameFlavor + "|" + rule.Patch + "|" + rule.ClassID + "|" + rule.SpecializationID + "|" + rule.Content
}
func seasonsOverlap(a, b *string) bool { return a == nil || b == nil || *a == *b }
