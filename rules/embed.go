// Package rules contains reviewed configuration bundles, not provider integrations.
package rules

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"

	"kipet-wow-analyzer/internal/domain/analysis"
)

//go:embed retail/*.json
var bundles embed.FS

// Load never enables synthetic rules in Blizzard mode. No real ruleset is
// bundled until its patch-specific references and fixtures have been reviewed.
func Load(mode string) (*analysis.Engine, error) {
	if mode != "fixture" && mode != "blizzard" {
		return nil, fmt.Errorf("invalid rules source mode")
	}
	files, err := fs.Glob(bundles, "retail/*.json")
	if err != nil {
		return nil, err
	}
	catalog := []analysis.Ruleset{}
	for _, file := range files {
		data, err := bundles.ReadFile(file)
		if err != nil {
			return nil, err
		}
		rule, err := analysis.DecodeRuleset(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("invalid bundled ruleset %s: %w", file, err)
		}
		if rule.Synthetic == (mode == "fixture") {
			catalog = append(catalog, rule)
		}
	}
	return analysis.New(catalog)
}
