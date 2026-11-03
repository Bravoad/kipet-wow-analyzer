package observability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeStore struct {
	pingError, error error
	metrics          map[string]float64
}

func (s *fakeStore) Ping(context.Context) error { return s.pingError }
func (s *fakeStore) Metrics(context.Context) (map[string]float64, error) {
	copy := map[string]float64{}
	for k, v := range s.metrics {
		copy[k] = v
	}
	return copy, s.error
}

type fakeCache struct{ error error }

func (s *fakeCache) Ping(context.Context) error { return s.error }

type schemaStore struct{ fakeStore }

func (s *schemaStore) CheckReady(context.Context) error { return errors.New("migration not applied") }

func TestWorkerSchemaCheckTakesPrecedenceOverSuccessfulPing(t *testing.T) {
	h := NewWorkerHealth(context.Background(), &schemaStore{fakeStore{metrics: map[string]float64{"queue_active": 9}}}, nil, []string{"characters"})
	stop, done := runTracked(h, "characters")
	defer func() { stop(); <-done }()
	if w := response(h.Handler(), "/health/ready"); w.Code != 503 {
		t.Fatal("database connection hid invalid migration ledger", w.Code)
	}
	if w := response(h.Handler(), "/metrics"); w.Code != 503 || strings.Contains(w.Body.String(), "kipet_queue_active") || !strings.Contains(w.Body.String(), "kipet_metrics_scrape_success 0\n") {
		t.Fatal("metrics hid incompatible schema", w.Code, w.Body.String())
	}
}

func response(h http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}
func runTracked(h *WorkerHealth, name string) (func(), <-chan error) {
	started, stop, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- h.Track(name, func(context.Context) error { close(started); <-stop; return nil })(context.Background())
	}()
	<-started
	return func() { close(stop) }, done
}
func TestWorkerReadinessTracksAllRunnersDependenciesAndDrain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, cache := &fakeStore{}, &fakeCache{}
	h := NewWorkerHealth(ctx, store, cache, []string{"characters", "guilds"})
	router := h.Handler()
	if w := response(router, "/health/ready"); w.Code != 503 {
		t.Fatal("startup prematurely ready", w.Code)
	}
	stopCharacter, characterDone := runTracked(h, "characters")
	defer func() { stopCharacter(); <-characterDone }()
	if w := response(router, "/health/ready"); w.Code != 503 {
		t.Fatal("missing guild runner accepted", w.Code)
	}
	stopGuild, guildDone := runTracked(h, "guilds")
	if w := response(router, "/health/ready"); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	cache.error = errors.New("Redis unavailable")
	if w := response(router, "/health/ready"); w.Code != 200 || !strings.Contains(w.Body.String(), `"degraded"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	store.pingError = errors.New("secret dependency URL must not escape")
	if w := response(router, "/health/ready"); w.Code != 503 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	store.pingError = nil
	stopGuild()
	<-guildDone
	if w := response(router, "/health/ready"); w.Code != 503 {
		t.Fatal("returned runner still ready", w.Code)
	}
	stopGuild, guildDone = runTracked(h, "guilds")
	defer func() { stopGuild(); <-guildDone }()
	cancel()
	if w := response(router, "/health/ready"); w.Code != 503 {
		t.Fatal("draining worker ready", w.Code)
	}
	if w := response(router, "/health/live"); w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("process liveness failed", w.Code)
	}
}
func TestWorkerMetricsReportScrapeFailureWithoutPublishingStaleValues(t *testing.T) {
	store := &fakeStore{metrics: map[string]float64{"guild_queue_active": 3, "queue_active": 2, "bad\nmetric": 123}}
	h := NewWorkerHealth(context.Background(), store, nil, []string{"characters"})
	stop, done := runTracked(h, "characters")
	defer func() { stop(); <-done }()
	w := response(h.Handler(), "/metrics")
	for _, line := range []string{"kipet_guild_queue_active 3\n", "kipet_queue_active 2\n", "kipet_worker_runners_expected 1\n", "kipet_worker_runners_running 1\n", "kipet_worker_running 1\n", "kipet_metrics_scrape_success 1\n"} {
		if !strings.Contains(w.Body.String(), line) {
			t.Fatal("missing metric", line, w.Body.String())
		}
	}
	if w.Code != 200 || strings.Contains(w.Body.String(), "bad") {
		t.Fatal(w.Code, w.Body.String())
	}
	store.error = errors.New("database password=secret")
	w = response(h.Handler(), "/metrics")
	if w.Code != 503 || !strings.Contains(w.Body.String(), "kipet_metrics_scrape_success 0\n") || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "kipet_guild_queue_active") {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestWorkerCannotRegisterAnUnexpectedRunner(t *testing.T) {
	h := NewWorkerHealth(context.Background(), &fakeStore{}, nil, []string{"characters"})
	called := false
	err := h.Track("unregistered", func(context.Context) error { called = true; return nil })(context.Background())
	if err == nil || called {
		t.Fatal("unexpected runner accepted")
	}
	if response(h.Handler(), "/health/ready").Code != 503 {
		t.Fatal("unexpected readiness")
	}
}
