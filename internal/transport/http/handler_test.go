package httptransport

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"kipet-wow-analyzer/internal/application"
	"kipet-wow-analyzer/internal/domain/analysis"
	"kipet-wow-analyzer/internal/domain/character"
)

type httpStore struct {
	application.Store
	key   character.Key
	calls int
}

func (s *httpStore) ResolveRealm(context.Context, string, string) (character.Realm, error) {
	return character.Realm{Slug: "stormrage"}, nil
}
func (s *httpStore) IsMissing(context.Context, character.Key) (bool, error) { return false, nil }
func (s *httpStore) Find(_ context.Context, k character.Key) (*character.Record, error) {
	s.key = k
	return nil, character.ErrNotFound
}
func (s *httpStore) Enqueue(_ context.Context, k character.Key, _ string, _ bool) (character.EnqueueResult, error) {
	s.key = k
	s.calls++
	return character.EnqueueResult{Job: character.Job{ID: "00000000-0000-4000-8000-000000000001"}}, nil
}
func handlerForTest() (*Handler, *httpStore) {
	s := &httpStore{}
	return &Handler{Store: s, Service: &application.CharacterService{Store: s}, Mode: "fixture", HMACSecret: []byte("test-only-hmac-secret-32-bytes-long")}, s
}
func TestURIParametersDecodeExactlyOnce(t *testing.T) {
	for _, test := range []struct {
		path   string
		status int
	}{{"/api/v1/characters/us/stormrage/%41rcanis", 202}, {"/api/v1/characters/%75s/stormrage/Arcanis", 202}, {"/api/v1/characters/us/%73tormrage/Arcanis", 202}, {"/api/v1/characters/us/stormrage/%2541rcanis", 400}, {"/api/v1/characters/us/stormrage/bad%2Fname", 400}} {
		t.Run(test.path, func(t *testing.T) {
			h, s := handlerForTest()
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", test.path, nil)
			h.Router().ServeHTTP(w, r)
			if w.Code != test.status {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if test.status == 202 && s.key.Name != "arcanis" {
				t.Fatal("encoded identity was not normalized")
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-ID") == "" {
				t.Fatal("missing safe response headers")
			}
		})
	}
}
func TestRefreshRejectsBypassFieldsAndOversize(t *testing.T) {
	for _, test := range []struct {
		body   string
		status int
	}{{`{"region":"us","realm":"stormrage","name":"Arcanis","force":true}`, 400}, {`{"region":"us","realm":"stormrage","name":"Arcanis"} {}`, 400}, {`{"name":"` + strings.Repeat("a", 5000) + `"}`, 413}} {
		h, s := handlerForTest()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/v1/refresh/characters", strings.NewReader(test.body))
		r.Header.Set("Content-Type", "application/json")
		h.Router().ServeHTTP(w, r)
		if w.Code != test.status || s.calls != 0 {
			t.Fatalf("status%d calls%d body%s", w.Code, s.calls, w.Body.String())
		}
	}
}
func TestHealthNotPublicAndRuntimeExplicit(t *testing.T) {
	h, _ := handlerForTest()
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, httptest.NewRequest("GET", "/health/live", nil))
	if w.Code != 404 {
		t.Fatal("public health leak")
	}
	w = httptest.NewRecorder()
	h.Router().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/runtime", nil))
	var data map[string]any
	if json.Unmarshal(w.Body.Bytes(), &data) != nil || data["provider_mode"] != "fixture" {
		t.Fatal("demo mode hidden")
	}
}
func TestUntrustedClientCannotForgeLimiterIdentity(t *testing.T) {
	h, _ := handlerForTest()
	h.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("172.29.231.10/32")}
	router := h.Router()
	for i := 0; i < 21; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/v1/characters/us/stormrage/Arcanis", nil)
		r.RemoteAddr = "192.0.2.1:1234"
		r.Header.Set("X-Real-IP", netip.AddrFrom4([4]byte{198, 51, 100, byte(i)}).String())
		router.ServeHTTP(w, r)
		if i == 20 && w.Code != 429 {
			t.Fatalf("spoofed header bypassed limiter: %d", w.Code)
		}
	}
}
func TestFaultIncludesTerminalJob(t *testing.T) {
	h, _ := handlerForTest()
	r := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	id := "00000000-0000-4000-8000-000000000001"
	h.fail(w, r, &character.Fault{Code: "PROVIDER_UNAVAILABLE", Retryable: true, RetryAt: time.Now().Add(time.Minute), JobID: &id}, 503)
	var out struct {
		Error struct {
			JobID *string `json:"job_id"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Error.JobID == nil || *out.Error.JobID != id {
		t.Fatal("terminal job reference lost")
	}
}

func TestAnalysisBootstrapsOnlyAfterValidation(t *testing.T) {
	for _, content := range []string{"general_pve", "raid", "mythic_plus", "pvp"} {
		t.Run(content, func(t *testing.T) {
			h, store := handlerForTest()
			engine, err := analysis.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			h.Analysis = &application.AnalysisService{Characters: h.Service, Store: store, Engine: engine, Mode: "fixture"}
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/api/v1/characters/us/stormrage/%41rcanis/analysis?content="+content, nil)
			h.Router().ServeHTTP(w, r)
			if content == "pvp" {
				if w.Code != 400 || store.calls != 0 {
					t.Fatalf("invalid context scheduled work: %d", w.Code)
				}
			} else {
				if w.Code != 202 || store.calls != 1 || w.Header().Get("Location") == "" {
					t.Fatalf("bootstrap failed: %d %s", w.Code, w.Body.String())
				}
			}
		})
	}
}

func TestProgressRoutesBootstrapThroughSameReadGuards(t *testing.T) {
	for _, suffix := range []string{"mythic-plus", "raids"} {
		t.Run(suffix, func(t *testing.T) {
			h, store := handlerForTest()
			w := httptest.NewRecorder()
			h.Router().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/characters/us/stormrage/Arcanis/"+suffix, nil))
			if w.Code != 202 || store.calls != 1 || w.Header().Get("Location") == "" || !strings.Contains(w.Body.String(), `"source":"fixture"`) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
}
