package blizzard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(transport.CloseIdleConnections)
	hc := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != "oauth.battle.net" && request.URL.Host != "eu.api.blizzard.com" && request.URL.Host != "us.api.blizzard.com" {
			t.Errorf("unexpected original destination %s", request.URL.Host)
		}
		clone := request.Clone(request.Context())
		u := *request.URL
		u.Scheme = target.Scheme
		u.Host = target.Host
		clone.URL = &u
		return transport.RoundTrip(clone)
	})}
	c, err := New(Config{ClientID: "synthetic-client", ClientSecret: "synthetic-secret", HTTPClient: hc})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func tokenResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"access_token":"synthetic-token","expires_in":3600,"token_type":"bearer"}`)
}

func TestOAuthCoalescesAndCredentialsStayOnTokenEndpoint(t *testing.T) {
	var tokens, gets atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokens.Add(1)
			id, secret, ok := r.BasicAuth()
			if !ok || id != "synthetic-client" || secret != "synthetic-secret" {
				t.Error("missing client authentication")
			}
			if r.Method != http.MethodPost || r.URL.RawQuery != "" {
				t.Error("OAuth credentials must not use URL query")
			}
			body, _ := io.ReadAll(r.Body)
			if string(body) != "grant_type=client_credentials" {
				t.Error("wrong grant")
			}
			tokenResponse(w)
			return
		}
		gets.Add(1)
		if r.Header.Get("Authorization") != "Bearer synthetic-token" {
			t.Error("missing bearer token")
		}
		if r.URL.Query().Get("access_token") != "" {
			t.Error("token in query")
		}
		io.WriteString(w, `{}`)
	})
	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := c.get(context.Background(), "eu", "profile", "/synthetic", "profile"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if tokens.Load() != 1 || gets.Load() != 24 {
		t.Fatalf("token calls=%d GETs=%d", tokens.Load(), gets.Load())
	}
}

func TestUnauthorizedRefreshesOnlyOnce(t *testing.T) {
	var tokens, gets atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokens.Add(1)
			tokenResponse(w)
			return
		}
		gets.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, _, err := c.get(context.Background(), "us", "profile", "/synthetic", "profile")
	var pe *Error
	if !errors.As(err, &pe) || pe.Code != "PROVIDER_AUTH_FAILURE" {
		t.Fatalf("unexpected error: %v", err)
	}
	if tokens.Load() != 2 || gets.Load() != 2 {
		t.Fatalf("unbounded or missing refresh tokens=%d gets=%d", tokens.Load(), gets.Load())
	}
}

func Test429PreservesRetryAfterAndSharedPause(t *testing.T) {
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	var paused time.Time
	var permits atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenResponse(w)
			return
		}
		w.Header().Set("Retry-After", "900")
		w.WriteHeader(429)
		io.WriteString(w, "sensitive upstream details")
	})
	c.cfg.Now = func() time.Time { return now }
	c.cfg.Permit = func(context.Context) error { permits.Add(1); return nil }
	c.cfg.PauseUntil = func(_ context.Context, t time.Time) error { paused = t; return nil }
	_, _, err := c.get(context.Background(), "eu", "profile", "/synthetic", "profile")
	var pe *Error
	if !errors.As(err, &pe) || pe.Code != "PROVIDER_RATE_LIMITED" || pe.RetryAt == nil || !pe.RetryAt.Equal(now.Add(15*time.Minute)) || !paused.Equal(*pe.RetryAt) {
		t.Fatalf("retry metadata lost: %#v paused=%v", err, paused)
	}
	if permits.Load() != 2 {
		t.Fatalf("OAuth and GET must both consume budget; got %d", permits.Load())
	}
	if strings.Contains(err.Error(), "sensitive") {
		t.Fatal("raw response leaked")
	}
}

func TestRedirectIsNotFollowedAndBodyIsLimited(t *testing.T) {
	for _, mode := range []string{"redirect", "oversize", "trailing"} {
		t.Run(mode, func(t *testing.T) {
			var redirected atomic.Int32
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					tokenResponse(w)
					return
				}
				if r.URL.Path == "/target" {
					redirected.Add(1)
					return
				}
				switch mode {
				case "redirect":
					http.Redirect(w, r, "https://eu.api.blizzard.com/target", 302)
				case "oversize":
					io.CopyN(w, strings.NewReader(strings.Repeat("x", int(maxResponseBytes+1))), maxResponseBytes+1)
				case "trailing":
					io.WriteString(w, `{} {}`)
				}
			})
			body, _, err := c.get(context.Background(), "eu", "profile", "/synthetic", "profile")
			if mode == "trailing" {
				var raw object
				if decode(body, &raw) == nil {
					t.Fatal("trailing JSON accepted")
				}
			} else {
				var pe *Error
				if !errors.As(err, &pe) || pe.Code != "PROVIDER_INVALID_RESPONSE" {
					t.Fatalf("expected invalid response: %v", err)
				}
			}
			if redirected.Load() != 0 {
				t.Fatal("redirect followed")
			}
		})
	}
}

func TestFetchPartial404DoesNotDeleteCharacterAndUnknownNodesRemainNull(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenResponse(w)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/equipment") {
			w.WriteHeader(404)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/statistics") {
			io.WriteString(w, `{"health":100,"strength":{"effective":0}}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/specializations") {
			io.WriteString(w, `{"specializations":[{"specialization":{"id":251,"name":"Frost"},"loadouts":[{"id":0,"is_active":true,"talent_loadout_code":"synthetic"}]}]}`)
			return
		}
		io.WriteString(w, `{"id":9007199254740993,"name":"Кипет","realm":{"id":1},"level":80,"race":{"id":1,"name":"Human"},"character_class":{"id":6,"name":"Death Knight"},"active_spec":{"id":251,"name":"Frost"},"faction":{"type":"ALLIANCE"}}`)
	})
	key := character.Key{Region: "eu", Realm: "гордунни", Name: "кипет"}
	realm := character.Realm{ProviderID: "1", Region: "eu", Slug: "гордунни", Names: map[string]string{"en-US": "Gordunni"}}
	snapshot, err := c.Fetch(context.Background(), key, realm)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ProviderID != "9007199254740993" {
		t.Fatal("provider int64 ID lost precision")
	}
	if err := character.ValidateSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Sections["equipment"].State != "unavailable" || snapshot.Sections["profile"].State != "complete" {
		t.Fatal("optional 404 destroyed mandatory profile")
	}
	var talent object
	if decode(snapshot.Sections["talents"].Payload, &talent) != nil {
		t.Fatal("invalid normalized talents")
	}
	loadout := asObject(talent["loadouts"].([]any)[0])
	if loadout["nodes"] != nil {
		t.Fatal("unknown nodes turned into empty list")
	}
	if text(loadout["provider_id"]) != "0" {
		t.Fatal("loadout ID zero was lost")
	}
	var stats object
	decode(snapshot.Sections["statistics"].Payload, &stats)
	if n := number(asObject(stats["primary"])["strength"]); n == nil || *n != 0 {
		t.Fatal("confirmed zero lost")
	}
	if asObject(stats["primary"])["agility"] != nil {
		t.Fatal("unknown stat replaced with zero")
	}
}

func TestProfileErrorsAndIdentityAreDistinct(t *testing.T) {
	for _, entry := range []struct {
		status     int
		body, code string
	}{{404, "", "CHARACTER_UNAVAILABLE"}, {403, "", "PROVIDER_ACCESS_DENIED"}, {200, `{"id":1,"name":"Other","realm":{"id":1},"level":80,"race":{"id":1},"character_class":{"id":6}}`, "PROVIDER_INVALID_RESPONSE"}, {200, `{"id":1,"name":"Кипет","realm":{"id":2},"level":80,"race":{"id":1},"character_class":{"id":6}}`, "PROVIDER_INVALID_RESPONSE"}} {
		t.Run(fmt.Sprintf("%d-%s", entry.status, entry.code), func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					tokenResponse(w)
					return
				}
				w.WriteHeader(entry.status)
				io.WriteString(w, entry.body)
			})
			_, err := c.Fetch(context.Background(), character.Key{Region: "eu", Realm: "гордунни", Name: "кипет"}, character.Realm{ProviderID: "1", Region: "eu", Slug: "гордунни"})
			var f *character.Fault
			if !errors.As(err, &f) || f.Code != entry.code {
				t.Fatalf("expected %s, got %v", entry.code, err)
			}
		})
	}
}

func TestRealmCatalogUsesCanonicalSlug(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenResponse(w)
			return
		}
		if r.URL.Query().Get("namespace") != "dynamic-eu" {
			t.Error("wrong namespace")
		}
		io.WriteString(w, `{"realms":[{"id":1,"slug":"гордунни","name":{"en_GB":"Gordunni","ru_RU":"Гордунни"}}]}`)
	})
	realms, err := c.Realms(context.Background(), "eu")
	if err != nil {
		t.Fatal(err)
	}
	if len(realms) != 1 || realms[0].Slug != "гордунни" || realms[0].Names["ru-RU"] != "Гордунни" {
		t.Fatalf("canonical realm corrupted: %#v", realms)
	}
}

func TestConstructorRejectsAbsentCredentials(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("blizzard mode silently accepted missing credentials")
	}
}

func TestLookupNamePreservesLettersExpandedByCaseFolding(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenResponse(w)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/equipment") || strings.HasSuffix(r.URL.Path, "/statistics") || strings.HasSuffix(r.URL.Path, "/specializations") || strings.HasSuffix(r.URL.Path, "/mythic-keystone-profile") || strings.HasSuffix(r.URL.Path, "/encounters/raids") {
			w.WriteHeader(404)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/straße") {
			t.Errorf("provider lookup used folded identity instead of lowercase input: %s", r.URL.Path)
		}
		io.WriteString(w, `{"id":1,"name":"Straße","realm":{"id":1},"level":80,"race":{"id":1},"character_class":{"id":6}}`)
	})
	key, err := character.NormalizeKey("eu", "example", "Straße")
	if err != nil {
		t.Fatal(err)
	}
	if key.Name != "strasse" {
		t.Fatal("test requires a Unicode folding expansion")
	}
	snapshot, err := c.Fetch(context.Background(), key, character.Realm{ProviderID: "1", Region: "eu", Slug: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.DisplayName != "Straße" {
		t.Fatal("display name lost")
	}
}

func TestCancelledOAuthCallerDoesNotPoisonNextRequest(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenResponse(w)
			return
		}
		io.WriteString(w, `{}`)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := c.get(ctx, "eu", "profile", "/synthetic", "profile"); err == nil {
		t.Fatal("cancelled request succeeded")
	}
	if _, _, err := c.get(context.Background(), "eu", "profile", "/synthetic", "profile"); err != nil {
		t.Fatalf("cancelled caller poisoned token cache: %v", err)
	}
}
