package httptransport

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"kipet-wow-analyzer/internal/application"
	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/raiderio"
)

type raiderAdmission struct {
	application.RaiderRepository
	key raiderio.Key
}

func (*raiderAdmission) LookupRaider(context.Context, raiderio.Key) (*application.RaiderRecord, error) {
	return nil, character.ErrNotFound
}

func (s *raiderAdmission) AdmitRaider(_ context.Context, key raiderio.Key) (character.Job, error) {
	s.key = key
	return character.Job{ID: "00000000-0000-4000-8000-000000000001"}, nil
}

func TestRaiderGuildRoutesUseGuildIdentity(t *testing.T) {
	const guildName = "Авантюрное агентство"
	for _, tc := range []struct {
		method, path, body string
	}{
		{"GET", "/api/v1/raiderio/guilds/eu/gordunni/" + url.PathEscape(guildName), ""},
		{"POST", "/api/v1/refresh/raiderio/guilds", `{"region":"eu","realm":"gordunni","name":"` + guildName + `"}`},
	} {
		t.Run(tc.method, func(t *testing.T) {
			h, _ := handlerForTest()
			repo := &raiderAdmission{}
			h.Raider = &application.RaiderService{Repository: repo}
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.Router().ServeHTTP(w, r)
			want := raiderio.Key{Kind: "guild", Region: "eu", Realm: "gordunni", Name: strings.ToLower(guildName)}
			if w.Code != 202 || repo.key != want || w.Header().Get("Location") != "/api/v1/jobs/00000000-0000-4000-8000-000000000001" {
				t.Fatalf("status %d, key %+v, body %s", w.Code, repo.key, w.Body.String())
			}
		})
	}
}

func TestSocialHTTPStrictBoundedBodies(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, contentType string
		status                        int
	}{
		{"media", "/api/v1/comparisons/characters", `{}`, "text/plain", 415},
		{"too-large", "/api/v1/comparisons/characters", `{"characters":[{"name":"` + strings.Repeat("a", 9000) + `"}]}`, "application/json", 413},
		{"unknown-field", "/api/v1/refresh/guilds", `{"force":true}`, "application/json", 400},
		{"trailing", "/api/v1/comparisons/characters", `{} {}`, "application/json", 400},
		{"empty-comparison", "/api/v1/comparisons/characters", `{"characters":[]}`, "application/json", 400},
		{"duplicate", "/api/v1/comparisons/characters", `{"characters":[{"region":"eu","realm":"гордунни","name":"Кипет"},{"region":"EU","realm":"гордунни","name":"кипет"}]}`, "application/json", 400},
		{"bad-slug", "/api/v1/refresh/guilds", `{"region":"eu","realm":"гордунни","name":"two words"}`, "application/json", 400},
		{"raider-kind", "/api/v1/refresh/raiderio/characters", `{"region":"eu","realm":"gordunni","name":"Кипет","kind":"guild"}`, "application/json", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, store := handlerForTest()
			h.Comparison = &application.ComparisonService{Mode: "fixture"}
			request := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()
			h.Router().ServeHTTP(w, request)
			if w.Code != tc.status || store.calls != 0 {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
}
