package httptransport

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"kipet-wow-analyzer/internal/application"
	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/guild"
	"kipet-wow-analyzer/internal/domain/raiderio"
)

type Handler struct {
	Service             *application.CharacterService
	Analysis            *application.AnalysisService
	Combat              *application.CombatService
	Raider              *application.RaiderService
	Guild               *application.GuildService
	Comparison          *application.ComparisonService
	Store               application.Store
	Cache               application.SnapshotCache
	Mode                string
	CombatMode          string
	CombatSetupRequired bool
	HMACSecret          []byte
	Replicas            int
	Logger              *slog.Logger
	TrustedProxies      []netip.Prefix
	requests            atomic.Uint64
	failures            atomic.Uint64
	limiterFallback     atomic.Uint64
	httpMetrics         httpMetrics
	fallbackMu          sync.Mutex
	fallback            map[string]*localBucket
}
type localBucket struct {
	tokens float64
	at     time.Time
}
type requestIDKey struct{}

func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(h.middleware)
	r.Group(func(r chi.Router) {
		r.Use(h.limiter(false))
		r.Get("/api/v1/runtime", h.runtime)
		r.Get("/api/v1/meta/realms", h.realms)
		r.Get("/api/v1/jobs/{id}", h.job)
		r.Get("/api/v1/raiderio/characters/{region}/{realm}/{name}", h.readRaider(raiderio.NormalizeKey))
		r.Get("/api/v1/raiderio/guilds/{region}/{realm}/{name}", h.readRaider(raiderio.NormalizeGuildKey))
		r.Get("/api/v1/guilds/{region}/{realm}/{name}", func(w http.ResponseWriter, r *http.Request) { h.guild(w, r, "profile") })
		r.Get("/api/v1/guilds/{region}/{realm}/{name}/roster", func(w http.ResponseWriter, r *http.Request) { h.guild(w, r, "roster") })
		r.Get("/api/v1/guilds/{region}/{realm}/{name}/analysis", func(w http.ResponseWriter, r *http.Request) { h.guild(w, r, "analysis") })
		r.Post("/api/v1/comparisons/characters", h.compare)
		r.Get("/api/v1/reports/{code}", func(w http.ResponseWriter, r *http.Request) { h.report(w, r, "report") })
		r.Get("/api/v1/reports/{code}/fights", func(w http.ResponseWriter, r *http.Request) { h.report(w, r, "fights") })
		r.Get("/api/v1/reports/{code}/fights/{fightID}/analysis", func(w http.ResponseWriter, r *http.Request) { h.report(w, r, "analysis") })
		r.Get("/api/v1/characters/{region}/{realm}/{name}/analysis", h.analysis)
		for _, section := range character.AllSections {
			section := section
			path := "/api/v1/characters/{region}/{realm}/{name}"
			if section != "profile" {
				slug := section
				if section == "mythic_plus" {
					slug = "mythic-plus"
				}
				path += "/" + slug
			}
			r.Get(path, func(w http.ResponseWriter, r *http.Request) { h.read(w, r, section) })
		}
	})
	r.With(h.limiter(true)).Post("/api/v1/refresh/characters", h.refresh)
	r.With(h.limiter(true)).Post("/api/v1/refresh/guilds", h.refreshGuild)
	r.With(h.limiter(true)).Post("/api/v1/refresh/raiderio/characters", h.refreshRaider(raiderio.NormalizeKey))
	r.With(h.limiter(true)).Post("/api/v1/refresh/raiderio/guilds", h.refreshRaider(raiderio.NormalizeGuildKey))
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		h.fail(w, r, character.NewFault("INVALID_ARGUMENT", false), http.StatusNotFound)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		h.fail(w, r, character.NewFault("INVALID_ARGUMENT", false), http.StatusMethodNotAllowed)
	})
	return r
}
func (h *Handler) InternalRouter() http.Handler {
	r := chi.NewRouter()
	r.Use(h.middleware)
	r.Get("/health/live", func(w http.ResponseWriter, r *http.Request) { h.health(w, r, "ok") })
	r.Get("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		check := h.Store.Ping
		if store, ok := h.Store.(interface{ CheckReady(context.Context) error }); ok {
			check = store.CheckReady
		}
		if err := check(ctx); err != nil {
			h.fail(w, r, err, 503)
			return
		}
		status := "ok"
		if h.Cache != nil && h.Cache.Ping(ctx) != nil {
			status = "degraded"
		}
		h.health(w, r, status)
	})
	r.Get("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		var values map[string]float64
		success := false
		if store, ok := h.Store.(interface {
			Metrics(context.Context) (map[string]float64, error)
		}); ok {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			var err error
			values, err = store.Metrics(ctx)
			if err == nil {
				if ready, ok := h.Store.(interface{ CheckReady(context.Context) error }); ok {
					err = ready.CheckReady(ctx)
				}
			}
			success = err == nil
		}
		if !success {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		if success {
			_, _ = io.WriteString(w, "# TYPE kipet_metrics_scrape_success gauge\nkipet_metrics_scrape_success 1\n")
		} else {
			_, _ = io.WriteString(w, "# TYPE kipet_metrics_scrape_success gauge\nkipet_metrics_scrape_success 0\n")
		}
		_, _ = io.WriteString(w, "# TYPE kipet_http_requests_total counter\nkipet_http_requests_total "+strconv.FormatUint(h.requests.Load(), 10)+"\n# TYPE kipet_http_errors_total counter\nkipet_http_errors_total "+strconv.FormatUint(h.failures.Load(), 10)+"\n")
		_, _ = io.WriteString(w, "# TYPE kipet_limiter_fallback_total counter\nkipet_limiter_fallback_total "+strconv.FormatUint(h.limiterFallback.Load(), 10)+"\n")
		_, _ = io.WriteString(w, h.httpMetrics.prometheus())
		if success {
			for name, value := range values {
				_, _ = io.WriteString(w, "# TYPE kipet_"+name+" gauge\nkipet_"+name+" "+strconv.FormatFloat(value, 'g', -1, 64)+"\n")
			}
		}
	})
	return r
}
func (h *Handler) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		began := time.Now()
		response := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
		w = response
		h.requests.Add(1)
		id := uuid.NewString()
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		defer func() {
			if recover() != nil {
				if response.Status() == 0 {
					h.fail(w, r, character.NewFault("INTERNAL_ERROR", true), 500)
				}
			}
			status := response.Status()
			if status == 0 {
				status = http.StatusOK
			}
			route := "unmatched"
			if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
				route = rc.RoutePattern()
			}
			h.httpMetrics.observe(route, r.Method, status, time.Since(began).Seconds())
		}()
		next.ServeHTTP(w, r)
	})
}
func (h *Handler) runtime(w http.ResponseWriter, r *http.Request) {
	demos := []map[string]string{}
	if h.Mode == "fixture" {
		demos = []map[string]string{{"region": "eu", "realm": "гордунни", "name": "Кипет"}, {"region": "us", "realm": "stormrage", "name": "Arcanis"}}
	}
	combatMode := h.CombatMode
	if combatMode == "" {
		combatMode = "disabled"
	}
	reports := []string{}
	if combatMode == "fixture" {
		reports = append(reports, "DEMOCombat000001")
	}
	guilds := []guild.CharacterKey{}
	comparisons := [][]guild.CharacterKey{}
	if h.Mode == "fixture" {
		guilds = append(guilds, guild.CharacterKey{Region: "eu", Realm: "гордунни", Name: "kipet-lab"})
		comparisons = append(comparisons, []guild.CharacterKey{{Region: "eu", Realm: "гордунни", Name: "Кипет"}, {Region: "eu", Realm: "гордунни", Name: "Ледник"}})
	}
	h.write(w, r, 200, map[string]any{
		"provider_mode": h.Mode, "demo_characters": demos, "combat_mode": combatMode,
		"demo_reports": reports, "demo_guilds": guilds, "demo_comparisons": comparisons,
		"raiderio_enabled": h.Raider != nil, "combat_setup_required": h.CombatSetupRequired,
	})
}

func (h *Handler) report(w http.ResponseWriter, r *http.Request, resource string) {
	locale, err := localeOf(r)
	code, codeErr := pathParam(r, "code")
	valid := len(code) == 16
	for _, ch := range code {
		if !(ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9') {
			valid = false
		}
	}
	if err != nil || codeErr != nil || !valid {
		h.fail(w, r, character.NewFault("INVALID_ARGUMENT", false), 400)
		return
	}
	var fightID, actorID int
	if resource == "analysis" {
		fight, pathErr := pathParam(r, "fightID")
		fightID, err = positiveReportID(fight)
		actorID, codeErr = positiveReportID(r.URL.Query().Get("actor_id"))
		if err != nil || codeErr != nil || pathErr != nil || len(r.URL.Query()["actor_id"]) != 1 {
			h.fail(w, r, character.NewFault("INVALID_ARGUMENT", false), 400)
			return
		}
	}
	if h.Combat == nil {
		h.fail(w, r, character.NewFault("COMBAT_NOT_CONFIGURED", false), 503)
		return
	}
	var result application.Result
	switch resource {
	case "report":
		result, err = h.Combat.ReadReport(r.Context(), code, locale)
	case "fights":
		result, err = h.Combat.ReadFights(r.Context(), code, locale)
	case "analysis":
		result, err = h.Combat.ReadAnalysis(r.Context(), code, fightID, actorID, locale)
	}
	if err != nil {
		h.fail(w, r, err, 0)
		return
	}
	h.result(w, r, result)
}

func positiveReportID(raw string) (int, error) {
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value < 1 || strconv.FormatInt(value, 10) != raw {
		return 0, character.NewFault("INVALID_ARGUMENT", false)
	}
	return int(value), nil
}

func (h *Handler) analysis(w http.ResponseWriter, r *http.Request) {
	locale, err := localeOf(r)
	if err != nil {
		h.fail(w, r, err, 0)
		return
	}
	region, e1 := pathParam(r, "region")
	realm, e2 := pathParam(r, "realm")
	name, e3 := pathParam(r, "name")
	if e1 != nil || e2 != nil || e3 != nil {
		h.fail(w, r, character.NewFault("INVALID_ARGUMENT", false), 400)
		return
	}
	key, err := character.NormalizeKey(region, realm, name)
	if err != nil {
		h.fail(w, r, err, 0)
		return
	}
	content := r.URL.Query().Get("content")
	if content == "" {
		content = "general_pve"
	}
	if h.Analysis == nil {
		h.fail(w, r, character.NewFault("INFRASTRUCTURE_UNAVAILABLE", true), 503)
		return
	}
	result, err := h.Analysis.Read(r.Context(), key, locale, content)
	if err != nil {
		h.fail(w, r, err, 0)
		return
	}
	h.result(w, r, result)
}
func (h *Handler) health(w http.ResponseWriter, r *http.Request, status string) {
	h.result(w, r, application.Result{Data: map[string]any{"status": status}, Meta: application.NewMeta("system", "ru-RU", "fresh")})
}
func (h *Handler) read(w http.ResponseWriter, r *http.Request, section string) {
	locale, err := localeOf(r)
	if err != nil {
		h.fail(w, r, err, 0)
		return
	}
	region, e1 := pathParam(r, "region")
	realm, e2 := pathParam(r, "realm")
	name, e3 := pathParam(r, "name")
	if e1 != nil || e2 != nil || e3 != nil {
		h.fail(w, r, character.NewFault("INVALID_ARGUMENT", false), 400)
		return
	}
	key, err := character.NormalizeKey(region, realm, name)
	if err != nil {
		h.fail(w, r, err, 0)
		return
	}
	v, err := h.Service.Read(r.Context(), key, locale, section)
	if err != nil {
		h.fail(w, r, err, 0)
		return
	}
	h.result(w, r, v)
}
func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		h.fail(w, r, character.NewFault("UNSUPPORTED_MEDIA_TYPE", false), 415)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body struct {
		Region string `json:"region"`
		Realm  string `json:"realm"`
		Name   string `json:"name"`
		Locale string `json:"locale"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			h.fail(w, r, character.NewFault("REQUEST_TOO_LARGE", false), 413)
		} else {
			h.fail(w, r, character.NewFault("INVALID_ARGUMENT", false), 400)
		}
		return
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		h.fail(w, r, character.NewFault("INVALID_ARGUMENT", false), 400)
		return
	}
	if body.Locale == "" {
		body.Locale = "ru-RU"
	}
	if body.Locale != "ru-RU" && body.Locale != "en-US" {
		h.fail(w, r, character.NewFault("INVALID_ARGUMENT", false), 400)
		return
	}
	key, err := character.NormalizeKey(body.Region, body.Realm, body.Name)
	if err != nil {
		h.fail(w, r, err, 0)
		return
	}
	v, err := h.Service.Refresh(r.Context(), key, body.Locale)
	if err != nil {
		h.fail(w, r, err, 0)
		return
	}
	h.result(w, r, v)
}
func (h *Handler) realms(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	region := q.Get("region")
	if region != "eu" && region != "us" {
		h.fail(w, r, character.NewFault("INVALID_ARGUMENT", false), 0)
		return
	}
	locale, err := localeOf(r)
	if err != nil {
		h.fail(w, r, err, 0)
		return
	}
	limit := 30
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > 100 {
			h.fail(w, r, character.NewFault("INVALID_ARGUMENT", false), 0)
			return
		}
	}
	if len([]rune(q.Get("q"))) > 80 || len(q.Get("cursor")) > 1024 {
		h.fail(w, r, character.NewFault("INVALID_ARGUMENT", false), 0)
		return
	}
	rows, next, err := h.Store.ListRealms(r.Context(), region, locale, q.Get("q"), q.Get("cursor"), limit)
	if err != nil {
		h.fail(w, r, err, 0)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	var fetched, until *time.Time
	for _, row := range rows {
		items = append(items, row.DTO(locale))
		if fetched == nil || row.FetchedAt.Before(*fetched) {
			at := row.FetchedAt
			fetched = &at
			end := at.Add(720 * time.Hour)
			until = &end
		}
	}
	meta := application.NewMeta("blizzard", locale, "fresh")
	meta.FetchedAt = fetched
	meta.ValidUntil = until
	if fetched != nil {
		fresh := fetched.Add(24 * time.Hour)
		state := "complete"
		if time.Now().After(fresh) {
			meta.Status = "stale"
		}
		meta.Coverage["realms"] = map[string]any{"state": state, "fetched_at": fetched, "fresh_until": fresh, "valid_until": until, "reason": nil}
	}
	var cursor any
	if next != "" {
		cursor = next
	}
	h.result(w, r, application.Result{Data: map[string]any{"items": items, "next_cursor": cursor}, Meta: meta})
}
func (h *Handler) job(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		h.fail(w, r, character.NewFault("INVALID_ARGUMENT", false), 0)
		return
	}
	j, err := h.Store.GetJob(r.Context(), id)
	if errors.Is(err, character.ErrNotFound) && h.Guild != nil {
		j, err = h.Guild.GetJob(r.Context(), id)
	}
	if errors.Is(err, character.ErrNotFound) && h.Raider != nil {
		j, err = h.Raider.GetJob(r.Context(), id)
	}
	if errors.Is(err, character.ErrNotFound) && h.Combat != nil {
		j, err = h.Combat.GetJob(r.Context(), id)
	}
	if errors.Is(err, character.ErrNotFound) {
		err = character.NewFault("JOB_NOT_FOUND", false)
	}
	if err != nil {
		h.fail(w, r, err, 0)
		return
	}
	var poll any
	if j.Active() {
		poll = 3
	}
	kind := j.Kind
	if kind == "" {
		kind = "refresh_character"
	}
	h.result(w, r, application.Result{Data: map[string]any{"id": j.ID, "kind": kind, "status": j.Status, "created_at": j.CreatedAt, "updated_at": j.UpdatedAt, "next_poll_after_seconds": poll, "error_code": j.ErrorCode}, Meta: application.NewMeta("system", "ru-RU", "fresh")})
}
func (h *Handler) result(w http.ResponseWriter, r *http.Request, v application.Result) {
	if h.Mode == "fixture" && v.Meta.Source == "blizzard" {
		v.Meta.Source = "fixture"
	}
	status := 200
	if v.Pending {
		status = 202
		w.Header().Set("Retry-After", "3")
		if v.Meta.JobID != nil {
			w.Header().Set("Location", "/api/v1/jobs/"+*v.Meta.JobID)
		}
	}
	h.write(w, r, status, map[string]any{"data": v.Data, "meta": v.Meta, "request_id": r.Context().Value(requestIDKey{})})
}
func (h *Handler) write(w http.ResponseWriter, r *http.Request, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error, status int) {
	h.failures.Add(1)
	f := character.AsFault(err)
	if status == 0 {
		switch f.Code {
		case "INVALID_ARGUMENT":
			status = 400
		case "CHARACTER_UNAVAILABLE", "REALM_NOT_FOUND", "JOB_NOT_FOUND", "REPORT_UNAVAILABLE", "GUILD_UNAVAILABLE":
			status = 404
		case "USER_RATE_LIMITED", "REFRESH_COOLDOWN":
			status = 429
		case "PROVIDER_ACCESS_DENIED", "PROVIDER_AUTH_FAILURE", "PROVIDER_INVALID_RESPONSE":
			status = 502
		case "INTERNAL_ERROR":
			status = 500
		default:
			status = 503
		}
	}
	var retry any
	if !f.RetryAt.IsZero() {
		seconds := max(1, int(math.Ceil(time.Until(f.RetryAt).Seconds())))
		retry = seconds
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	if status >= 500 && h.Logger != nil {
		h.Logger.Error("request failed", "request_id", r.Context().Value(requestIDKey{}), "code", f.Code, "route", chi.RouteContext(r.Context()).RoutePattern())
	}
	h.write(w, r, status, map[string]any{"error": map[string]any{"code": f.Code, "message": safeMessage(f.Code), "retryable": f.Retryable, "retry_after_seconds": retry, "job_id": f.JobID, "fields": []any{}}, "request_id": r.Context().Value(requestIDKey{})})
}
func localeOf(r *http.Request) (string, error) {
	locale := r.URL.Query().Get("locale")
	if locale == "" {
		locale = "ru-RU"
	}
	if locale != "ru-RU" && locale != "en-US" {
		return "", character.NewFault("INVALID_ARGUMENT", false)
	}
	return locale, nil
}

func pathParam(r *http.Request, name string) (string, error) {
	value := chi.URLParam(r, name)
	if r.URL.RawPath != "" {
		return url.PathUnescape(value)
	}
	return value, nil
}
func safeMessage(code string) string {
	switch code {
	case "INVALID_ARGUMENT":
		return "Invalid request parameters."
	case "CHARACTER_UNAVAILABLE":
		return "Character not found or unavailable."
	case "REPORT_UNAVAILABLE":
		return "Public report not found or unavailable."
	case "GUILD_UNAVAILABLE":
		return "Guild not found or unavailable."
	case "COMBAT_NOT_CONFIGURED":
		return "Public report analysis is not configured."
	case "RAIDERIO_NOT_CONFIGURED":
		return "Raider.IO profile lookup is not configured."
	case "REALM_NOT_FOUND":
		return "Realm not found in this region."
	case "JOB_NOT_FOUND":
		return "Refresh job not found."
	case "USER_RATE_LIMITED", "REFRESH_COOLDOWN":
		return "Please wait before trying again."
	default:
		return "The request could not be completed. Please try again later."
	}
}
func (h *Handler) limiter(refresh bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				host = r.RemoteAddr
			}
			if peer, e := netip.ParseAddr(host); e == nil {
				for _, prefix := range h.TrustedProxies {
					if prefix.Contains(peer.Unmap()) {
						if client, e := netip.ParseAddr(r.Header.Get("X-Real-IP")); e == nil {
							host = client.Unmap().String()
						}
						break
					}
				}
			}
			mac := hmac.New(sha256.New, h.HMACSecret)
			_, _ = mac.Write([]byte(host))
			bucket := hex.EncodeToString(mac.Sum(nil))
			limit, burst := 120, 20
			if refresh {
				limit, burst = 3, 1
				bucket = "refresh:" + bucket
			} else {
				bucket = "read:" + bucket
			}
			allowed := false
			wait := time.Second
			var limitErr error
			if h.Cache != nil {
				allowed, wait, limitErr = h.Cache.Allow(r.Context(), bucket, limit, burst, time.Minute)
			} else {
				limitErr = errors.New("cache unavailable")
			}
			if limitErr != nil {
				h.limiterFallback.Add(1)
				allowed, wait = h.localAllow(bucket, limit, burst)
			}
			if !allowed {
				fault := character.NewFault("USER_RATE_LIMITED", true)
				fault.RetryAt = time.Now().Add(wait)
				h.fail(w, r, fault, 429)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
func (h *Handler) localAllow(key string, limit, burst int) (bool, time.Duration) {
	h.fallbackMu.Lock()
	defer h.fallbackMu.Unlock()
	if h.fallback == nil {
		h.fallback = map[string]*localBucket{}
	}
	now := time.Now()
	if len(h.fallback) > 10000 {
		for k, b := range h.fallback {
			if now.Sub(b.at) > 2*time.Minute {
				delete(h.fallback, k)
			}
		}
		if len(h.fallback) > 10000 {
			return false, time.Minute
		}
	}
	replicas := max(1, h.Replicas)
	rate := float64(limit) / float64(replicas) / 60
	capacity := math.Max(1, float64(burst)/float64(replicas))
	b := h.fallback[key]
	if b == nil {
		b = &localBucket{tokens: capacity, at: now}
		h.fallback[key] = b
	}
	b.tokens = math.Min(capacity, b.tokens+now.Sub(b.at).Seconds()*rate)
	b.at = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / rate * float64(time.Second))
}
