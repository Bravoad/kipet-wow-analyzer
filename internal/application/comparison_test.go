package application

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/guild"
)

func projectionRecord(now time.Time) *character.Record {
	fresh, valid := now.Add(time.Minute), now.Add(time.Hour)
	patch := "synthetic-demo-v1"
	section := func(raw string) character.Section {
		return character.Section{State: "complete", FetchedAt: &now, FreshUntil: &fresh, ValidUntil: &valid, CheckedAt: now, Payload: json.RawMessage(raw)}
	}
	return &character.Record{ID: "id", Realm: character.Realm{FetchedAt: now, Slug: "realm", Names: map[string]string{"en-US": "Localized"}}, Snapshot: &character.Snapshot{ID: "snapshot", ValidUntil: valid, Patch: &patch, Sections: map[string]character.Section{"profile": section(`{"provider_id":"42","character_class":{"id":"6"},"active_specialization":{"id":"251"}}`), "equipment": section(`{"items":[]}`), "mythic_plus": section(`{"season":{"id":"99"}}`)}}}
}
func TestProjectionPublicationAndSectionDeadlines(t *testing.T) {
	now := time.Now().UTC()
	key, _ := character.NormalizeKey("eu", "realm", "Кипет")
	r := projectionRecord(now)
	expired := now.Add(-time.Second)
	equipment := r.Snapshot.Sections["equipment"]
	equipment.ValidUntil = &expired
	r.Snapshot.Sections["equipment"] = equipment
	p := ProjectCharacter(r, key, "en-US", "fixture", now)
	if p.Data == nil || p.Data.Equipment != nil || p.Meta.SnapshotID == nil {
		t.Fatal("section expiry leaked or hid permitted profile")
	}
	var profile map[string]any
	_ = json.Unmarshal(p.Data.Profile, &profile)
	if profile["realm"].(map[string]any)["name"] != "Localized" {
		t.Fatal("profile realm was not localized")
	}
	for _, mutate := range []func(*character.Record){func(r *character.Record) { r.Blocked = true }, func(r *character.Record) { r.Missing = true }, func(r *character.Record) { r.DataValidUntil = &expired }, func(r *character.Record) { r.Realm.FetchedAt = now.Add(-721 * time.Hour) }, func(r *character.Record) { r.Snapshot.ValidUntil = expired }} {
		r := projectionRecord(now)
		mutate(r)
		p := ProjectCharacter(r, key, "en-US", "fixture", now)
		if p.Data != nil || p.Status != "unavailable" || p.Meta.SnapshotID != nil {
			t.Fatal("publication guard leaked", p.Status)
		}
	}
}
func TestComparisonContextUsesExactClassSpecAndOwnSeason(t *testing.T) {
	now := time.Now().UTC()
	key, _ := character.NormalizeKey("eu", "realm", "Кипет")
	a := ProjectCharacter(projectionRecord(now), key, "ru-RU", "fixture", now)
	b := ProjectCharacter(projectionRecord(now), key, "ru-RU", "fixture", now)
	c := CompareContext([]CharacterProjection{a, b})
	if c.SamePatch == nil || !*c.SamePatch || c.SameSpecialization == nil || !*c.SameSpecialization || c.MythicPlusSameSeason == nil || !*c.MythicPlusSameSeason || c.CombatComparable {
		t.Fatal("equal contexts misclassified")
	}
	b.Data.Profile = json.RawMessage(`{"character_class":{"id":"8"},"active_specialization":{"id":"251"}}`)
	b.Data.MythicPlus = json.RawMessage(`{"season":null}`)
	c = CompareContext([]CharacterProjection{a, b})
	if c.SameSpecialization == nil || *c.SameSpecialization || c.MythicPlusSameSeason != nil {
		t.Fatal("different class or unknown season collapsed")
	}
}

type comparisonFake struct {
	Store
	calls int
}

func (f *comparisonFake) FindMany(context.Context, []character.Key) ([]*character.Record, error) {
	f.calls++
	return nil, nil
}
func TestComparisonValidatesWholeBatchBeforeReadingOrScheduling(t *testing.T) {
	f := &comparisonFake{}
	s := ComparisonService{Store: f, Mode: "fixture"}
	for _, input := range [][]guild.CharacterKey{nil, {{Region: "eu", Realm: "realm", Name: "Кипет"}, {Region: "eu", Realm: "realm", Name: "КИПЕТ"}}, make([]guild.CharacterKey, 6)} {
		_, err := s.Read(context.Background(), input, "ru-RU")
		if character.AsFault(err).Code != "INVALID_ARGUMENT" || f.calls != 0 {
			t.Fatal("invalid or duplicate batch admitted")
		}
	}
}
