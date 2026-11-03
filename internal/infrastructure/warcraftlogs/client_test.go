package warcraftlogs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/combat"
)

const syntheticReport = `{"code":"DEMOCombat000001","title":"Synthetic report","visibility":"public","startTime":1793469600000,"endTime":1793469840000,"fights":[{"id":1,"name":"Synthetic encounter","encounterID":9007199254740993,"difficulty":14,"kill":false,"startTime":0,"endTime":120000}],"masterData":{"actors":[{"id":1,"name":"Synthetic player","type":"Player","subType":"DeathKnight"},{"id":2,"type":"Pet"}]}}`

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}
func tokenResponse() *http.Response {
	return response(200, `{"access_token":"synthetic-token","token_type":"Bearer","expires_in":3600}`)
}
func reportResponse(raw string) *http.Response {
	return response(200, `{"data":{"reportData":{"report":`+raw+`},"rateLimitData":{"limitPerHour":1000,"pointsSpentThisHour":1,"pointsResetIn":3600}}}`)
}
func testClient(t *testing.T, fn func(*http.Request) (*http.Response, error)) *Client {
	t.Helper()
	c, err := New(Config{ClientID: "synthetic-client", ClientSecret: "synthetic-secret", HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "www.warcraftlogs.com" || r.URL.Scheme != "https" || r.URL.RawQuery != "" {
			t.Error("unexpected request destination")
		}
		return fn(r)
	})}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func requestPayload(t *testing.T, r *http.Request) (string, object) {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	var payload object
	if decode(body, &payload) != nil {
		t.Fatal("invalid request JSON")
	}
	return text(payload["query"]), obj(payload["variables"])
}

func TestOAuthCoalescesAndSecretsNeverEnterGraphQL(t *testing.T) {
	var tokenCalls, apiCalls atomic.Int32
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/oauth/token" {
			tokenCalls.Add(1)
			id, secret, ok := r.BasicAuth()
			if !ok || id != "synthetic-client" || secret != "synthetic-secret" {
				t.Error("missing client credentials")
			}
			body, _ := io.ReadAll(r.Body)
			if string(body) != "grant_type=client_credentials" {
				t.Error("wrong grant")
			}
			return tokenResponse(), nil
		}
		apiCalls.Add(1)
		if r.URL.Path != "/api/v2/client" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer synthetic-token" {
			t.Error("wrong GraphQL authentication")
		}
		if _, _, ok := r.BasicAuth(); ok {
			t.Error("credentials leaked into GraphQL")
		}
		query, variables := requestPayload(t, r)
		if !strings.Contains(query, "rateLimitData") || text(variables["code"]) != "DEMOCombat000001" {
			t.Error("unsafe query variables")
		}
		return reportResponse(syntheticReport), nil
	})
	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Report(context.Background(), "DEMOCombat000001"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if tokenCalls.Load() != 1 || apiCalls.Load() != 24 {
		t.Fatalf("token=%d API=%d", tokenCalls.Load(), apiCalls.Load())
	}
}
func TestOneTimeUnauthorizedRecovery(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		var tokens, requests int
		c := testClient(t, func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/oauth/token" {
				tokens++
				return tokenResponse(), nil
			}
			requests++
			if requests == 1 || persistent {
				return response(401, `{"secret":"never reflected"}`), nil
			}
			return reportResponse(syntheticReport), nil
		})
		_, err := c.Report(context.Background(), "DEMOCombat000001")
		if tokens != 2 || requests != 2 {
			t.Fatal("unbounded OAuth recovery")
		}
		if persistent && character.AsFault(err).Code != "PROVIDER_AUTH_FAILURE" || !persistent && err != nil {
			t.Fatal(err)
		}
	}
}
func TestOnlyExplicitPublicReportsAreReturned(t *testing.T) {
	for _, visibility := range []string{"private", "unlisted", "", "PUBLIC"} {
		c := testClient(t, func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/oauth/token" {
				return tokenResponse(), nil
			}
			return reportResponse(strings.Replace(syntheticReport, `"visibility":"public"`, `"visibility":"`+visibility+`"`, 1)), nil
		})
		report, err := c.Report(context.Background(), "DEMOCombat000001")
		if visibility == "PUBLIC" {
			if err != nil || report.Visibility != "public" {
				t.Fatal(err)
			}
		} else if character.AsFault(err).Code != "REPORT_UNAVAILABLE" || report.Code != "" {
			t.Fatal("non-public data published")
		}
	}
	for _, code := range []string{"", "https://evil.com/reports/DEMOCombat000001", `x"){reportData{`} {
		var calls int
		c := testClient(t, func(*http.Request) (*http.Response, error) { calls++; return nil, nil })
		_, err := c.Report(context.Background(), code)
		if calls != 0 || character.AsFault(err).Code != "INVALID_ARGUMENT" {
			t.Fatal("invalid code reached network")
		}
	}
}
func TestGraphQLPartialErrorsFailClosedAndDoNotReflectMessages(t *testing.T) {
	for _, code := range []string{"FORBIDDEN", "NOT_FOUND", "UNAUTHENTICATED", "UNKNOWN"} {
		c := testClient(t, func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/oauth/token" {
				return tokenResponse(), nil
			}
			return response(200, `{"data":{"reportData":{"report":`+syntheticReport+`}},"errors":[{"message":"SENSITIVE_RAW_MESSAGE","extensions":{"code":"`+code+`"}}]}`), nil
		})
		actual, err := c.Report(context.Background(), "DEMOCombat000001")
		if err == nil || actual.Code != "" || strings.Contains(err.Error(), "SENSITIVE") {
			t.Fatal("partial GraphQL publication or raw error leak")
		}
	}
}

func TestGraphQLDenialWinsOverUsagePersistenceFailure(t *testing.T) {
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/oauth/token" {
			return tokenResponse(), nil
		}
		return response(200, `{"data":{"reportData":{"report":null},"rateLimitData":{"limitPerHour":100,"pointsSpentThisHour":10,"pointsResetIn":100}},"errors":[{"extensions":{"code":"FORBIDDEN"}}]}`), nil
	})
	c.cfg.ObserveUsage = func(context.Context, float64, time.Time) error {
		return character.NewFault("INFRASTRUCTURE_UNAVAILABLE", true)
	}
	_, err := c.Report(context.Background(), "DEMOCombat000001")
	if character.AsFault(err).Code != "PROVIDER_ACCESS_DENIED" {
		t.Fatal("budget error suppressed privacy denial")
	}
}

func TestExplicitFightMembershipIsSortedAndValidated(t *testing.T) {
	var raw object
	decode([]byte(strings.Replace(syntheticReport, `"startTime":0,"endTime":120000`, `"startTime":0,"endTime":120000,"friendlyPlayers":[2,1]`, 1)), &raw)
	report, err := normalizeReport(raw, "DEMOCombat000001")
	if err != nil || len(report.Fights[0].ParticipantIDs) != 2 || report.Fights[0].ParticipantIDs[0] != 1 {
		t.Fatal("fight participants not preserved")
	}
	for _, list := range []string{`[0]`, `[1,1]`, `[1.5]`, `"invalid"`} {
		raw = nil
		decode([]byte(strings.Replace(syntheticReport, `"startTime":0,"endTime":120000`, `"startTime":0,"endTime":120000,"friendlyPlayers":`+list, 1)), &raw)
		if _, err = normalizeReport(raw, "DEMOCombat000001"); err == nil {
			t.Fatal("invalid participant IDs accepted")
		}
	}
}
func TestPointsBudgetAndRetryAfterAreObserved(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	var observed bool
	var paused time.Time
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/oauth/token" {
			return tokenResponse(), nil
		}
		return response(200, `{"data":{"reportData":{"report":`+syntheticReport+`},"rateLimitData":{"limitPerHour":100,"pointsSpentThisHour":100,"pointsResetIn":7200}}}`), nil
	})
	c.cfg.Now = func() time.Time { return now }
	c.cfg.ObserveUsage = func(_ context.Context, remaining float64, until time.Time) error {
		observed = remaining == 0 && until.Equal(now.Add(2*time.Hour))
		return nil
	}
	c.cfg.PauseUntil = func(_ context.Context, until time.Time) error { paused = until; return nil }
	if _, err := c.Report(context.Background(), "DEMOCombat000001"); err != nil || !observed || !paused.Equal(now.Add(2*time.Hour)) || c.Usage() == nil {
		t.Fatal("points observation lost")
	}
	if _, err := c.Report(context.Background(), "DEMOCombat000001"); character.AsFault(err).Code != "PROVIDER_RATE_LIMITED" {
		t.Fatal("known exhausted local quota ignored")
	}
	c = testClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/oauth/token" {
			return tokenResponse(), nil
		}
		r429 := response(429, "")
		r429.Header.Set("Retry-After", "7200")
		return r429, nil
	})
	c.cfg.Now = func() time.Time { return now }
	c.cfg.PauseUntil = func(_ context.Context, until time.Time) error { paused = until; return nil }
	_, err := c.Report(context.Background(), "DEMOCombat000001")
	fault := character.AsFault(err)
	if fault.Code != "PROVIDER_RATE_LIMITED" || !fault.RetryAt.Equal(now.Add(2*time.Hour)) || !paused.Equal(fault.RetryAt) {
		t.Fatal("Retry-After shortened")
	}
}
func TestPaginationAndEventNormalization(t *testing.T) {
	var pages int
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/oauth/token" {
			return tokenResponse(), nil
		}
		query, variables := requestPayload(t, r)
		if !strings.Contains(query, "PublicFightEvents") {
			return reportResponse(syntheticReport), nil
		}
		pages++
		if !strings.Contains(query, "events(fightIDs: [$fight], startTime: $start, endTime: $end, limit: 2000)") ||
			!strings.Contains(query, "combatant: events(dataType: CombatantInfo, fightIDs: [$fight], sourceID: $actor") ||
			!strings.Contains(query, "@include(if: $withStats)") ||
			localID(variables["actor"]) == nil || *localID(variables["actor"]) != 1 ||
			variables["withStats"] != (pages == 1) {
			t.Error("expected unfiltered event pages and a first-page actor stats selection")
		}
		paginator := `{"data":[{"timestamp":1000,"type":"cast","sourceID":1,"abilityGameID":9007199254740993},{"timestamp":1500,"type":"damage","sourceID":1,"targetID":100,"abilityGameID":9007199254740993,"amount":12000}],"nextPageTimestamp":2000}`
		stats := `,"combatant":{"data":[
			{"type":"combatantinfo","sourceID":2,"timestamp":0,"strength":999},
			{"type":"combatantinfo","sourceID":1,"timestamp":0,"fight":2,"strength":999},
			{"type":"combatantinfo","sourceID":1,"timestamp":120001,"strength":999},
			{"type":"combatantinfo","sourceID":1,"timestamp":250,"strength":999},
			{"type":"combatantinfo","sourceID":1,"timestamp":10,"fight":1,"strength":840,"mastery":2,
			 "stats":{"agility":0,"critMelee":232,"stamina":1720,"hasteSpell":-4,"mastery":3},
			 "gear":[{"id":555}],"unrecognized":100}
		]}`
		if pages == 2 {
			if *integer(variables["start"]) != 2000 {
				t.Error("wrong cursor")
			}
			paginator = `{"data":[{"timestamp":2500,"type":"death","targetID":1}],"nextPageTimestamp":null}`
			stats = ""
		}
		return response(200, `{"data":{"reportData":{"report":{"code":"DEMOCombat000001","visibility":"public","events":`+paginator+stats+`}}}}`), nil
	})
	report, err := c.Report(context.Background(), "DEMOCombat000001")
	if err != nil {
		t.Fatal(err)
	}
	collected, err := c.Collect(context.Background(), report, 1, 1)
	if err != nil || !collected.Complete || pages != 2 || len(collected.Events) != 3 || *collected.Events[0].AbilityID != "9007199254740993" {
		t.Fatalf("pagination failed %+v %v", collected, err)
	}
	a, err := combat.Calculate(collected, "warcraftlogs")
	if err != nil || *a.Metrics.DPS != 100 || *a.Metrics.Deaths != 1 {
		t.Fatal("normalized observations miscalculated")
	}
	stats := a.CombatantStats
	if stats == nil || stats.ObservedAtMS != 10 || len(stats.Values) != 4 ||
		stats.Values["strength"] != 840 || stats.Values["stamina"] != 1720 ||
		stats.Values["critical_strike_melee"] != 232 {
		t.Fatalf("unexpected actor statistics: %+v", stats)
	}
	if value, ok := stats.Values["agility"]; !ok || value != 0 {
		t.Fatal("observed zero was lost")
	}
}

func TestReportMetadataSkipsEnvironmentSentinel(t *testing.T) {
	var raw object
	if err := decode([]byte(syntheticReport), &raw); err != nil {
		t.Fatal(err)
	}
	master := obj(raw["masterData"])
	actors := master["actors"].([]any)
	environment := object{"id": json.Number("-1"), "name": "Environment", "type": "NPC"}
	master["actors"] = append(actors, environment)
	report, err := normalizeReport(raw, "DEMOCombat000001")
	if err != nil || len(report.Actors) != len(actors) || report.Actors[0].ID != 1 {
		t.Fatalf("environment actor invalidated report: %+v, %v", report, err)
	}
	environment["type"] = "Player"
	if _, err := normalizeReport(raw, "DEMOCombat000001"); err == nil {
		t.Fatal("negative player ID accepted")
	}
}

func TestEventPageAllowsBoundedTimestampOverflow(t *testing.T) {
	event := object{"timestamp": json.Number("77988")}
	list := make([]any, eventPageSize+4)
	for i := range list {
		list[i] = event
	}
	if !validEventPageSize(list) {
		t.Fatal("same-timestamp overflow rejected")
	}
	list[len(list)-1] = object{"timestamp": json.Number("78027")}
	if validEventPageSize(list) || validEventPageSize(make([]any, eventPageSize+257)) {
		t.Fatal("invalid overflow accepted")
	}
}
func TestPaginationLimitsAndPrivacyChangesNeverProduceCompleteMetrics(t *testing.T) {
	for _, mode := range []string{"cap", "loop", "missing", "invalid", "private"} {
		pages := 0
		c := testClient(t, func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/oauth/token" {
				return tokenResponse(), nil
			}
			query, variables := requestPayload(t, r)
			if !strings.Contains(query, "PublicFightEvents") {
				return reportResponse(syntheticReport), nil
			}
			pages++
			start := *integer(variables["start"])
			paginator := map[string]any{"data": []any{map[string]any{"timestamp": start + 10, "type": "damage", "sourceID": 1, "amount": 10}}, "nextPageTimestamp": start + 100}
			switch mode {
			case "loop":
				paginator["nextPageTimestamp"] = start
			case "missing":
				delete(paginator, "nextPageTimestamp")
			case "invalid":
				paginator["data"] = []any{map[string]any{"timestamp": start + 10, "type": "damage", "sourceID": 1, "amount": -1}}
				paginator["nextPageTimestamp"] = nil
			}
			visibility := "public"
			if mode == "private" && pages == 2 {
				visibility = "private"
			}
			rawReport := map[string]any{"code": "DEMOCombat000001", "visibility": visibility, "events": paginator}
			if mode == "private" && pages == 1 {
				rawReport["combatant"] = map[string]any{"data": []any{map[string]any{"type": "combatantinfo", "sourceID": 1, "timestamp": 0, "strength": 840}}}
			}
			encoded, _ := json.Marshal(map[string]any{"data": map[string]any{"reportData": map[string]any{"report": rawReport}}})
			return response(200, string(encoded)), nil
		})
		report, _ := c.Report(context.Background(), "DEMOCombat000001")
		collected, err := c.Collect(context.Background(), report, 1, 1)
		if mode == "private" {
			if character.AsFault(err).Code != "REPORT_UNAVAILABLE" || pages != 2 || collected.CombatantStats != nil || len(collected.Events) != 0 {
				t.Fatal("privacy change ignored")
			}
			continue
		}
		if err != nil || collected.Complete || pages > 5 || mode == "cap" && pages != 5 {
			t.Fatalf("unsafe pagination %s %+v %v", mode, collected, err)
		}
		a, _ := combat.Calculate(collected, "warcraftlogs")
		if a.Metrics.DPS != nil {
			t.Fatal("partial stream yielded DPS")
		}
	}
}
func TestRedirectsOversizedBodiesAndUnexpectedHostsAreRejected(t *testing.T) {
	for _, mode := range []string{"redirect", "oversize"} {
		calls := 0
		c := testClient(t, func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.Path == "/oauth/token" {
				return tokenResponse(), nil
			}
			if mode == "redirect" {
				redirect := response(302, "")
				redirect.Header.Set("Location", "https://evil.example/steal")
				return redirect, nil
			}
			return response(200, strings.Repeat("a", int(maxResponseBytes)+1)), nil
		})
		_, err := c.Report(context.Background(), "DEMOCombat000001")
		if character.AsFault(err).Code != "PROVIDER_INVALID_RESPONSE" || calls != 2 {
			t.Fatal("unsafe redirect/body accepted")
		}
	}
	c := testClient(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected host reached transport")
		return nil, nil
	})
	req, _ := http.NewRequest("POST", "https://evil.example/api/v2/client", nil)
	if _, err := c.do(req); err == nil {
		t.Fatal("SSRF destination accepted")
	}
}
func TestMetadataNullEmptyAndInvalidIdentities(t *testing.T) {
	var raw object
	if decode([]byte(syntheticReport), &raw) != nil {
		t.Fatal("bad fixture")
	}
	report, err := normalizeReport(raw, "DEMOCombat000001")
	if err != nil || report.Fights[0].Kill == nil || *report.Fights[0].Kill || *report.Fights[0].EncounterID != "9007199254740993" || report.Actors[1].Type != "pet" {
		t.Fatal("normalization lost false/exact IDs")
	}
	raw = nil
	if decode([]byte(`{"code":"DEMOCombat000001","visibility":"public","fights":[],"masterData":{"actors":[]}}`), &raw) != nil {
		t.Fatal("bad fixture")
	}
	report, err = normalizeReport(raw, "DEMOCombat000001")
	if err != nil || report.Fights == nil || report.Actors == nil {
		t.Fatal("empty collection lost")
	}
	raw = nil
	if decode([]byte(`{"code":"DEMOCombat000001","visibility":"public"}`), &raw) != nil {
		t.Fatal("bad fixture")
	}
	report, err = normalizeReport(raw, "DEMOCombat000001")
	if err != nil || report.Fights != nil || report.Actors != nil {
		t.Fatal("missing collection invented")
	}
	raw = nil
	if decode([]byte(strings.Replace(syntheticReport, `"id":1`, `"id":0`, 1)), &raw) != nil {
		t.Fatal("bad fixture")
	}
	if _, err = normalizeReport(raw, "DEMOCombat000001"); err == nil {
		t.Fatal("invalid fight ID accepted")
	}
}
