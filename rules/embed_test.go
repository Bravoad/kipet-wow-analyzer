package rules

import (
	"context"
	"testing"
	"time"

	"kipet-wow-analyzer/internal/domain/analysis"
	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/infrastructure/fixture"
)

func TestEmbeddedCatalogKeepsSyntheticRulesOutOfBlizzard(t *testing.T) {
	demo, err := Load("fixture")
	if err != nil {
		t.Fatal(err)
	}
	live, err := Load("blizzard")
	if err != nil {
		t.Fatal(err)
	}
	if demo.CatalogDigest() == live.CatalogDigest() {
		t.Fatal("synthetic catalog was not separated")
	}
	provider := fixture.New()
	realms, err := provider.Realms(context.Background(), "eu")
	if err != nil {
		t.Fatal(err)
	}
	key, _ := character.NormalizeKey("eu", "гордунни", "Кипет")
	snapshot, err := provider.Fetch(context.Background(), key, realms[0])
	if err != nil {
		t.Fatal(err)
	}
	report := demo.Analyze(analysis.Input{Snapshot: snapshot, Source: "fixture", Content: analysis.GeneralPVE, Now: time.Now().UTC()})
	if report.Ruleset == nil || !report.Ruleset.Synthetic || report.Summary.Warning < 1 || report.Summary.OK < 1 || report.Summary.Info < 1 || report.Summary.Unknown < 1 {
		t.Fatalf("synthetic example lacks expected independent outcomes: %+v", report)
	}
	liveReport := live.Analyze(analysis.Input{Snapshot: snapshot, Source: "blizzard", Content: analysis.GeneralPVE, Now: time.Now().UTC()})
	if liveReport.Ruleset != nil || liveReport.Summary.Unknown != 3 {
		t.Fatal("live claims invented")
	}
	if _, err = Load("unsupported"); err == nil {
		t.Fatal("unknown source mode accepted")
	}
}
