package application

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"kipet-wow-analyzer/internal/domain/analysis"
	"kipet-wow-analyzer/internal/domain/character"
)

type analysisCache struct {
	payload      json.RawMessage
	until        time.Time
	now          *time.Time
	loads, saves int
}

func (c *analysisCache) LoadAnalysis(context.Context, string, string, string) (json.RawMessage, error) {
	c.loads++
	if len(c.payload) == 0 || !c.now.Before(c.until) {
		return nil, character.ErrNotFound
	}
	return c.payload, nil
}
func (c *analysisCache) SaveAnalysis(_ context.Context, _, _, _ string, p json.RawMessage, until time.Time) error {
	c.saves++
	c.payload = append([]byte(nil), p...)
	c.until = until
	return nil
}

func analysisFixture(t *testing.T) (*AnalysisService, *readStore, *analysisCache, *time.Time) {
	t.Helper()
	now := time.Now().UTC()
	fetched := now.Add(-time.Minute)
	fresh := now.Add(14 * time.Minute)
	valid := now.Add(time.Hour)
	profile := json.RawMessage(`{"id":"00000000-0000-4000-8000-000000000001","character_class":{"id":"6"},"active_specialization":{"id":"251"}}`)
	sections := map[string]character.Section{}
	for _, name := range character.Sections {
		payload := json.RawMessage(`{}`)
		if name == "profile" {
			payload = profile
		}
		sections[name] = character.Section{State: "complete", FetchedAt: &fetched, CheckedAt: fetched, FreshUntil: &fresh, ValidUntil: &valid, Payload: payload}
	}
	store := &readStore{record: &character.Record{ID: "00000000-0000-4000-8000-000000000001", Realm: character.Realm{Slug: "synthetic"}, Snapshot: &character.Snapshot{ID: "00000000-0000-4000-8000-000000000002", FetchedAt: fetched, ProfileVerifiedAt: fetched, ValidUntil: valid, Sections: sections}}}
	clock := func() time.Time { return now }
	cache := &analysisCache{now: &now}
	engine, err := analysis.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	service := &AnalysisService{Characters: &CharacterService{Store: store, Now: clock}, Store: store, Repository: cache, Engine: engine, Mode: "blizzard", Now: clock}
	return service, store, cache, &now
}

func TestAnalysisUnknownIsHonestAndCachedAcrossLocales(t *testing.T) {
	s, _, cache, now := analysisFixture(t)
	key := character.Key{Region: "eu", Realm: "synthetic", Name: "synthetic"}
	r, err := s.Read(context.Background(), key, "ru-RU", analysis.GeneralPVE)
	if err != nil {
		t.Fatal(err)
	}
	report := r.Data.(analysis.Report)
	if report.Ruleset != nil || report.Summary.Unknown != 3 || report.Summary.Warning != 0 || report.SnapshotID != *r.Meta.SnapshotID {
		t.Fatalf("unsupported assessment: %+v", report)
	}
	*now = now.Add(10 * time.Second)
	other, err := s.Read(context.Background(), key, "en-US", analysis.GeneralPVE)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report, other.Data) || cache.saves != 1 || cache.loads != 2 || other.Meta.Locale != "en-US" {
		t.Fatal("localized request changed cached assessment")
	}
}

func TestAnalysisCannotUseCacheToBypassQuarantineOrExpiry(t *testing.T) {
	for _, test := range []string{"quarantine", "expired"} {
		t.Run(test, func(t *testing.T) {
			s, store, cache, now := analysisFixture(t)
			key := character.Key{Region: "eu", Realm: "synthetic", Name: "synthetic"}
			if _, err := s.Read(context.Background(), key, "ru-RU", analysis.GeneralPVE); err != nil {
				t.Fatal(err)
			}
			loads := cache.loads
			if test == "quarantine" {
				store.record.Blocked = true
			} else {
				*now = now.Add(2 * time.Hour)
			}
			r, err := s.Read(context.Background(), key, "ru-RU", analysis.GeneralPVE)
			if test == "quarantine" && character.AsFault(err).Code != "PROVIDER_ACCESS_DENIED" {
				t.Fatal("quarantine hidden")
			}
			if test == "expired" && (err != nil || !r.Pending || r.Data != nil) {
				t.Fatal("expired profile reused")
			}
			if cache.loads != loads {
				t.Fatal("analysis cache accessed before publication guard")
			}
		})
	}
}

func TestAnalysisSanitizesExpiredEvidence(t *testing.T) {
	s, store, cache, now := analysisFixture(t)
	old := store.record.Snapshot.Sections["equipment"]
	expired := now.Add(-time.Second)
	old.ValidUntil = &expired
	old.Payload = json.RawMessage(`{"items":[{"name":"must-not-disclose-expired-equipment"}]}`)
	store.record.Snapshot.Sections["equipment"] = old
	r, err := s.Read(context.Background(), character.Key{Region: "eu", Realm: "synthetic", Name: "synthetic"}, "ru-RU", analysis.GeneralPVE)
	if err != nil {
		t.Fatal(err)
	}
	if r.Meta.Coverage["equipment"].(map[string]any)["state"] != "unavailable" || bytes.Contains(cache.payload, []byte("must-not-disclose-expired-equipment")) {
		t.Fatal("expired evidence disclosed")
	}
}

func TestAnalysisFreshnessChangeInvalidatesReport(t *testing.T) {
	s, _, cache, now := analysisFixture(t)
	key := character.Key{Region: "eu", Realm: "synthetic", Name: "synthetic"}
	if _, err := s.Read(context.Background(), key, "ru-RU", analysis.GeneralPVE); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(15 * time.Minute)
	r, err := s.Read(context.Background(), key, "ru-RU", analysis.GeneralPVE)
	if err != nil {
		t.Fatal(err)
	}
	if r.Meta.Status != "stale" || cache.saves != 2 {
		t.Fatal("freshness transition was frozen by cached analysis")
	}
	found := false
	for _, d := range r.Data.(analysis.Report).Diagnostics {
		if d.Code == "DATA_STALE" {
			found = true
		}
	}
	if !found {
		t.Fatal("stale data not explained")
	}
}

func TestAnalysisRejectsUnknownContentBeforeDataAccess(t *testing.T) {
	s := &AnalysisService{}
	_, err := s.Read(context.Background(), character.Key{}, "ru-RU", "pvp")
	if character.AsFault(err).Code != "INVALID_ARGUMENT" {
		t.Fatal(err)
	}
}
