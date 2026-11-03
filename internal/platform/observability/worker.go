// Package observability serves internal operational endpoints. Its readiness
// check observes runner lifecycles and dependencies, not provider availability
// or successful job progress. Queue age and expired leases cover that separately.
package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"sync"
	"time"
)

type Pinger interface{ Ping(context.Context) error }
type Store interface {
	Pinger
	Metrics(context.Context) (map[string]float64, error)
}

type WorkerHealth struct {
	ctx     context.Context
	store   Store
	cache   Pinger
	mu      sync.RWMutex
	runners map[string]bool
}

func NewWorkerHealth(ctx context.Context, store Store, cache Pinger, names []string) *WorkerHealth {
	h := &WorkerHealth{ctx: ctx, store: store, cache: cache, runners: map[string]bool{}}
	for _, name := range names {
		h.runners[name] = false
	}
	return h
}

// Track marks an expected runner active only while its Run method is executing.
// Returning from any runner withdraws readiness, even before sibling shutdown.
func (h *WorkerHealth) Track(name string, run func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		h.mu.Lock()
		_, expected := h.runners[name]
		if expected {
			h.runners[name] = true
		}
		h.mu.Unlock()
		if !expected {
			return fmt.Errorf("unregistered worker component")
		}
		defer func() { h.mu.Lock(); h.runners[name] = false; h.mu.Unlock() }()
		return run(ctx)
	}
}

func (h *WorkerHealth) counts() (int, int) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	active := 0
	for _, running := range h.runners {
		if running {
			active++
		}
	}
	return len(h.runners), active
}
func (h *WorkerHealth) running() bool {
	expected, active := h.counts()
	return h.ctx.Err() == nil && expected > 0 && expected == active
}

func (h *WorkerHealth) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { h.health(w, 200, "ok") })
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if !h.running() || h.store == nil {
			h.health(w, 503, "unavailable")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		check := h.store.Ping
		if ready, ok := h.store.(interface{ CheckReady(context.Context) error }); ok {
			check = ready.CheckReady
		}
		if check(ctx) != nil || !h.running() {
			h.health(w, 503, "unavailable")
			return
		}
		status := "ok"
		if h.cache != nil && h.cache.Ping(ctx) != nil {
			status = "degraded"
		}
		if !h.running() {
			h.health(w, 503, "unavailable")
			return
		}
		h.health(w, 200, status)
	})
	mux.HandleFunc("GET /metrics", h.metrics)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}
func (h *WorkerHealth) health(w http.ResponseWriter, code int, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
}

var metricName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func (h *WorkerHealth) metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	metrics := map[string]float64{}
	var err error
	if h.store == nil {
		err = fmt.Errorf("metrics unavailable")
	} else {
		if ready, ok := h.store.(interface{ CheckReady(context.Context) error }); ok {
			err = ready.CheckReady(ctx)
		}
		if err == nil {
			metrics, err = h.store.Metrics(ctx)
		}
	}
	if err != nil {
		metrics = map[string]float64{}
	}
	if metrics == nil {
		metrics = map[string]float64{}
	}
	expected, active := h.counts()
	metrics["worker_runners_expected"], metrics["worker_runners_running"] = float64(expected), float64(active)
	metrics["worker_running"] = 0
	if h.running() {
		metrics["worker_running"] = 1
	}
	metrics["metrics_scrape_success"] = 1
	if err != nil {
		metrics["metrics_scrape_success"] = 0
		w.WriteHeader(503)
	}
	names := make([]string, 0, len(metrics))
	for name, value := range metrics {
		if metricName.MatchString(name) && !math.IsNaN(value) && !math.IsInf(value, 0) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		_, _ = fmt.Fprintf(w, "# TYPE kipet_%s gauge\nkipet_%s %g\n", name, name, metrics[name])
	}
}
