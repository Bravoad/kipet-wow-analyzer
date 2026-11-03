package httptransport

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

type metricsStore struct {
	*httpStore
	failure bool
}

func (s *metricsStore) Metrics(context.Context) (map[string]float64, error) {
	if s.failure {
		return nil, errors.New("synthetic storage failure")
	}
	return map[string]float64{"queue_active": 2}, nil
}
func TestHTTPMetricsUseRouteTemplatesAndExposeScrapeFailure(t *testing.T) {
	h, store := handlerForTest()
	s := &metricsStore{httpStore: store}
	h.Store = s
	router := h.Router()
	for _, path := range []string{"/api/v1/characters/us/stormrage/Privatealpha?token=private-query", "/api/v1/characters/us/stormrage/Privatebeta", "/unknown-private-path"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	}
	w := httptest.NewRecorder()
	h.InternalRouter().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	output := w.Body.String()
	if w.Code != 200 || !strings.Contains(output, `route="/api/v1/characters/{region}/{realm}/{name}",method="GET",status="202"`) || !strings.Contains(output, `route="unmatched"`) || !strings.Contains(output, "kipet_metrics_scrape_success 1") {
		t.Fatal(w.Code, output)
	}
	for _, private := range []string{"Privatealpha", "Privatebeta", "private-query", "unknown-private-path"} {
		if strings.Contains(output, private) {
			t.Fatal("metrics leaked user input", private)
		}
	}
	s.failure = true
	w = httptest.NewRecorder()
	h.InternalRouter().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "kipet_metrics_scrape_success 0") || strings.Contains(w.Body.String(), "kipet_queue_active 2") {
		t.Fatal("failed scrape looked healthy", w.Code, w.Body.String())
	}
}
func TestRuntimeUsesReadLimiterAndDirectAPISecurityHeaders(t *testing.T) {
	h, _ := handlerForTest()
	router := h.Router()
	for i := 0; i < 21; i++ {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/runtime", nil))
		if i == 20 && (w.Code != 429 || w.Header().Get("Retry-After") == "") {
			t.Fatal("runtime bypassed limiter", w.Code)
		}
		for header, want := range map[string]string{"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "same-origin", "X-Frame-Options": "DENY"} {
			if w.Header().Get(header) != want {
				t.Fatal("missing security header", header)
			}
		}
	}
}
