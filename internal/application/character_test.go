package application

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
)

type readStore struct {
	Store
	record  *character.Record
	missing bool
	calls   int
	fail    error
}

func (f *readStore) ResolveRealm(context.Context, string, string) (character.Realm, error) {
	return character.Realm{Slug: "synthetic"}, nil
}
func (f *readStore) Find(context.Context, character.Key) (*character.Record, error) {
	if f.record == nil {
		return nil, character.ErrNotFound
	}
	return f.record, nil
}
func (f *readStore) IsMissing(context.Context, character.Key) (bool, error) { return f.missing, nil }
func (f *readStore) Enqueue(context.Context, character.Key, string, bool) (character.EnqueueResult, error) {
	f.calls++
	return character.EnqueueResult{Job: character.Job{ID: "job"}}, f.fail
}
func TestReadStateBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	fresh := now.Add(time.Minute)
	valid := now.Add(time.Hour)
	key := character.Key{Region: "eu", Realm: "synthetic", Name: "synthetic"}
	newStore := func() *readStore {
		return &readStore{record: &character.Record{ID: "id", Realm: character.Realm{Slug: "synthetic"}, Snapshot: &character.Snapshot{ID: "snapshot", ValidUntil: valid, Sections: map[string]character.Section{"profile": {State: "complete", FetchedAt: &past, FreshUntil: &fresh, ValidUntil: &valid, CheckedAt: past, Payload: json.RawMessage(`{"name":"Synthetic"}`)}, "equipment": character.Unavailable("not_provided", now), "statistics": character.Unavailable("not_provided", now), "talents": character.Unavailable("not_provided", now)}}}}
	}
	t.Run("fresh read does not enqueue", func(t *testing.T) {
		f := newStore()
		s := CharacterService{Store: f, Now: func() time.Time { return now }}
		r, e := s.Read(context.Background(), key, "ru-RU", "profile")
		if e != nil || r.Meta.Status != "fresh" || f.calls != 0 {
			t.Fatalf("%+v %v calls%d", r, e, f.calls)
		}
	})
	t.Run("optional unknown is final not pending", func(t *testing.T) {
		f := newStore()
		s := CharacterService{Store: f, Now: func() time.Time { return now }}
		r, e := s.Read(context.Background(), key, "ru-RU", "talents")
		if e != nil || r.Data != nil || r.Pending || r.Meta.Status != "unavailable" || f.calls != 0 {
			t.Fatalf("%+v %v", r, e)
		}
	})
	t.Run("stale works during queue outage", func(t *testing.T) {
		f := newStore()
		p := f.record.Snapshot.Sections["profile"]
		p.FreshUntil = &past
		f.record.Snapshot.Sections["profile"] = p
		f.fail = character.NewFault("QUEUE_FULL", true)
		s := CharacterService{Store: f, Now: func() time.Time { return now }}
		r, e := s.Read(context.Background(), key, "en-US", "profile")
		if e != nil || r.Data == nil || r.Meta.Status != "stale" || r.Meta.JobID != nil {
			t.Fatalf("%+v %v", r, e)
		}
	})
	t.Run("expired profile cannot leak", func(t *testing.T) {
		f := newStore()
		f.record.Snapshot.ValidUntil = past
		s := CharacterService{Store: f, Now: func() time.Time { return now }}
		r, e := s.Read(context.Background(), key, "ru-RU", "profile")
		if e != nil || r.Data != nil || !r.Pending || f.calls != 1 {
			t.Fatalf("%+v %v", r, e)
		}
	})
	t.Run("quarantine wins over fresh", func(t *testing.T) {
		f := newStore()
		f.record.Blocked = true
		s := CharacterService{Store: f, Now: func() time.Time { return now }}
		_, e := s.Read(context.Background(), key, "ru-RU", "profile")
		if character.AsFault(e).Code != "PROVIDER_ACCESS_DENIED" || f.calls != 1 {
			t.Fatal(e)
		}
	})
	t.Run("confirmed missing does not enqueue", func(t *testing.T) {
		f := newStore()
		f.missing = true
		s := CharacterService{Store: f, Now: func() time.Time { return now }}
		_, e := s.Read(context.Background(), key, "ru-RU", "profile")
		if character.AsFault(e).Code != "CHARACTER_UNAVAILABLE" || f.calls != 0 {
			t.Fatal(e)
		}
	})
	t.Run("terminal failure does not become pending", func(t *testing.T) {
		f := newStore()
		f.record.Snapshot = nil
		f.fail = character.NewFault("PROVIDER_UNAVAILABLE", true)
		s := CharacterService{Store: f, Now: func() time.Time { return now }}
		r, e := s.Read(context.Background(), key, "ru-RU", "profile")
		if e == nil || r.Pending {
			t.Fatal("terminal failure hidden by pending")
		}
	})
}

func TestTalentReuseNeedsConfirmedMatchingSpecialization(t *testing.T) {
	unknown := json.RawMessage(`{"active_specialization":null}`)
	known := json.RawMessage(`{"active_specialization":{"id":"251"}}`)
	other := json.RawMessage(`{"active_specialization":{"id":"252"}}`)
	if sameSpecialization(unknown, unknown) || sameSpecialization(known, unknown) || sameSpecialization(known, other) {
		t.Fatal("cannot reuse talents without confirmed same specialization")
	}
	if !sameSpecialization(known, known) {
		t.Fatal("confirmed same specialization should permit reuse")
	}
}

func TestProgressReadIsolation(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	fresh, valid := now.Add(time.Minute), now.Add(time.Hour)
	section := func(payload string) character.Section {
		return character.Section{State: "complete", FetchedAt: &now, FreshUntil: &fresh, ValidUntil: &valid, CheckedAt: now, Payload: json.RawMessage(payload)}
	}
	makeStore := func() *readStore {
		legacySeason := "42"
		return &readStore{record: &character.Record{ID: "character", Snapshot: &character.Snapshot{ID: "snapshot", ValidUntil: valid, Season: &legacySeason, Sections: map[string]character.Section{"profile": section(`{"name":"Synthetic"}`)}}}}
	}
	key := character.Key{Region: "eu", Realm: "synthetic", Name: "synthetic"}
	for _, test := range []struct {
		name, section, payload string
		season                 *string
	}{
		{"own season", "mythic_plus", `{"season":{"id":"9001"}}`, func() *string { s := "9001"; return &s }()},
		{"unknown season", "mythic_plus", `{"season":null}`, nil},
		{"raids do not inherit mythic season", "raids", `{"expansions":[]}`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := makeStore()
			store.record.Snapshot.Sections[test.section] = section(test.payload)
			svc := CharacterService{Store: store, Now: func() time.Time { return now }}
			result, err := svc.Read(context.Background(), key, "ru-RU", test.section)
			if err != nil || result.Meta.Status != "fresh" || len(result.Meta.Coverage) != 6 || store.calls != 0 {
				t.Fatalf("result=%+v err=%v calls=%d", result, err, store.calls)
			}
			if test.season == nil && result.Meta.Season != nil || test.season != nil && (result.Meta.Season == nil || *test.season != *result.Meta.Season) {
				t.Fatalf("wrong progress season: %v", result.Meta.Season)
			}
		})
	}
	for _, reason := range []string{"missing", "never_fetched", "not_provided", "provider_unavailable"} {
		t.Run(reason, func(t *testing.T) {
			store := makeStore()
			if reason != "missing" {
				store.record.Snapshot.Sections["mythic_plus"] = character.Unavailable(reason, now)
			}
			svc := CharacterService{Store: store, Now: func() time.Time { return now }}
			result, err := svc.Read(context.Background(), key, "en-US", "mythic_plus")
			wantCalls := 0
			if reason == "missing" || reason == "never_fetched" {
				wantCalls = 1
			}
			if err != nil || result.Data != nil || result.Pending || result.Meta.Status != "unavailable" || result.Meta.Season != nil || store.calls != wantCalls {
				t.Fatalf("result=%+v err=%v calls=%d", result, err, store.calls)
			}
		})
	}
	t.Run("invalid section cannot schedule work", func(t *testing.T) {
		store := makeStore()
		svc := CharacterService{Store: store}
		_, err := svc.Read(context.Background(), key, "ru-RU", "mythic-plus")
		if character.AsFault(err).Code != "INVALID_ARGUMENT" || store.calls != 0 {
			t.Fatal(err)
		}
	})
	t.Run("expired progress is hidden under fresh profile", func(t *testing.T) {
		store := makeStore()
		expired := section(`{"season":{"id":"9001"}}`)
		expiredAt := now.Add(-time.Minute)
		expired.ValidUntil = &expiredAt
		store.record.Snapshot.Sections["mythic_plus"] = expired
		svc := CharacterService{Store: store, Now: func() time.Time { return now }}
		result, err := svc.Read(context.Background(), key, "ru-RU", "mythic_plus")
		if err != nil || result.Data != nil || result.Meta.Status != "unavailable" || result.Meta.Season != nil || store.calls != 1 {
			t.Fatalf("%+v %v", result, err)
		}
	})
}
