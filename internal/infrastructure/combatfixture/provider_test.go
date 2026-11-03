package combatfixture

import (
	"context"
	"reflect"
	"testing"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/combat"
)

func TestSyntheticObservationsAreExplicitAndCalculated(t *testing.T) {
	p := New()
	report, err := p.Report(context.Background(), Code)
	if err != nil {
		t.Fatal(err)
	}
	if report.Visibility != "public" || len(report.Actors) != 2 || len(report.Fights) != 2 {
		t.Fatal("fixture shape changed")
	}
	for _, entry := range []struct {
		fight, actor int
		dps, hps     float64
		complete     bool
	}{{1, 1, 3250, 0, true}, {1, 2, 0, 1250, true}, {2, 1, 0, 0, false}, {2, 2, 0, 0, true}} {
		c, err := p.Collect(context.Background(), report, entry.fight, entry.actor)
		if err != nil {
			t.Fatal(err)
		}
		a, err := combat.Calculate(c, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		if a.Source != "fixture" || a.EventsComplete != entry.complete {
			t.Fatal("synthetic provenance lost")
		}
		if entry.fight == 1 && entry.actor == 1 {
			if a.CombatantStats == nil || a.CombatantStats.ObservedAtMS != 0 || a.CombatantStats.Values["strength"] != 840 {
				t.Fatal("fixture combatant stats missing")
			}
			if zero, ok := a.CombatantStats.Values["agility"]; !ok || zero != 0 {
				t.Fatal("confirmed zero stat missing")
			}
			if _, ok := a.CombatantStats.Values["intellect"]; ok {
				t.Fatal("unobserved stat invented")
			}
		} else if a.CombatantStats != nil {
			t.Fatal("fixture combatant stats leaked across actor or fight")
		}
		if entry.complete {
			if *a.Metrics.DPS != entry.dps || *a.Metrics.HPS != entry.hps {
				t.Fatalf("unexpected synthetic metrics %+v", a.Metrics)
			}
		} else if !reflect.DeepEqual(a.Metrics, combat.Metrics{}) {
			t.Fatal("incomplete synthetic stream invented metrics")
		}
	}
	_, err = p.Report(context.Background(), "UnknownReport001")
	if character.AsFault(err).Code != "REPORT_UNAVAILABLE" {
		t.Fatal("unknown synthetic report invented")
	}
}
