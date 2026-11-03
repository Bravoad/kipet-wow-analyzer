package fixture

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/progress"
)

func TestOnlyDocumentedSyntheticCharactersExist(t *testing.T) {
	p := New()
	for _, entry := range []struct{ region, name string }{{"eu", "Кипет"}, {"us", "Arcanis"}} {
		realms, err := p.Realms(context.Background(), entry.region)
		if err != nil {
			t.Fatal(err)
		}
		key, err := character.NormalizeKey(entry.region, realms[0].Slug, entry.name)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := p.Fetch(context.Background(), key, realms[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := character.ValidateSnapshot(snapshot); err != nil {
			t.Fatal(err)
		}
		key.Name = "unlisted"
		_, err = p.Fetch(context.Background(), key, realms[0])
		var fault *character.Fault
		if !errors.As(err, &fault) || fault.Code != "CHARACTER_UNAVAILABLE" {
			t.Fatalf("unknown synthetic person invented: %v", err)
		}
	}
}

func TestSyntheticProgressDemonstratesTimedOvertimeAndUnknownKills(t *testing.T) {
	p := New()
	realms, _ := p.Realms(context.Background(), "eu")
	key, _ := character.NormalizeKey("eu", realms[0].Slug, "Кипет")
	snapshot, err := p.Fetch(context.Background(), key, realms[0])
	if err != nil {
		t.Fatal(err)
	}
	var mythic progress.MythicPlus
	if err := json.Unmarshal(snapshot.Sections["mythic_plus"].Payload, &mythic); err != nil {
		t.Fatal(err)
	}
	if snapshot.Season != nil || mythic.Season == nil || mythic.Season.ID != "900005001" || mythic.RunScope != "season_best" || len(mythic.BestRuns) != 2 {
		t.Fatalf("unexpected synthetic season context: %#v", mythic)
	}
	if mythic.BestRuns[0].WithinTime == nil || !*mythic.BestRuns[0].WithinTime || mythic.BestRuns[1].WithinTime == nil || *mythic.BestRuns[1].WithinTime {
		t.Fatal("timed/overtime observations missing")
	}
	var raids progress.Raids
	if err := json.Unmarshal(snapshot.Sections["raids"].Payload, &raids); err != nil {
		t.Fatal(err)
	}
	bosses := raids.Expansions[0].Instances[0].Modes[0].Encounters
	if bosses[1].CompletedCount == nil || *bosses[1].CompletedCount != 0 || bosses[2].CompletedCount != nil || snapshot.Sections["raids"].State != "partial" {
		t.Fatal("confirmed zero and unknown kills conflated")
	}
}
