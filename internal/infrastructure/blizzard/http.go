package blizzard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxResponseBytes int64 = 4 << 20

// Config accepts credentials only on the server. Request URLs are deliberately
// not configurable; tests can inject an HTTP transport without changing hosts.
type Config struct {
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client
	Now          func() time.Time
	Permit       func(context.Context) error
	PauseUntil   func(context.Context, time.Time) error
}

// Error contains only safe, typed provider information. It never includes a URL,
// response body, OAuth credential, or access token.
type Error struct {
	Code      string
	Scope     string
	Retryable bool
	RetryAt   *time.Time
	cause     error
}

func (e *Error) Error() string { return e.Code }
func (e *Error) Unwrap() error { return e.cause }

type Client struct {
	cfg             Config
	http            *http.Client
	mu              sync.Mutex
	accessToken     string
	expiresAt       time.Time
	refreshing      chan struct{}
	tokenError      error
	tokenErrorUntil time.Time
	seasonMu        sync.Mutex
	seasons         map[string]*seasonCatalog
}

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.ClientID) == "" || strings.TrimSpace(cfg.ClientSecret) == "" {
		return nil, errors.New("Blizzard client ID and secret are required in blizzard mode")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	hc := &http.Client{Timeout: 10 * time.Second}
	if cfg.HTTPClient != nil {
		*hc = *cfg.HTTPClient
	}
	if hc.Timeout <= 0 || hc.Timeout > 10*time.Second {
		hc.Timeout = 10 * time.Second
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{cfg: cfg, http: hc}, nil
}

func (c *Client) permit(ctx context.Context) error {
	if c.cfg.Permit != nil {
		return c.cfg.Permit(ctx)
	}
	return nil
}

func (c *Client) token(ctx context.Context) (string, error) {
	for {
		c.mu.Lock()
		now := c.cfg.Now()
		if c.accessToken != "" && now.Before(c.expiresAt) {
			t := c.accessToken
			c.mu.Unlock()
			return t, nil
		}
		if c.tokenError != nil && now.Before(c.tokenErrorUntil) {
			err := c.tokenError
			c.mu.Unlock()
			return "", err
		}
		if pending := c.refreshing; pending != nil {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-pending:
				continue
			}
		}
		c.refreshing = make(chan struct{})
		pending := c.refreshing
		c.mu.Unlock()
		t, expiry, err := c.fetchToken(ctx)
		c.mu.Lock()
		if err == nil {
			c.accessToken, c.expiresAt = t, expiry
			c.tokenError = nil
		} else {
			// One cancelled caller must not poison token acquisition for others.
			if ctx.Err() == nil {
				c.tokenError, c.tokenErrorUntil = err, c.cfg.Now().Add(5*time.Second)
			}
		}
		c.refreshing = nil
		close(pending)
		c.mu.Unlock()
		return t, err
	}
}

func (c *Client) fetchToken(ctx context.Context) (string, time.Time, error) {
	if err := c.permit(ctx); err != nil {
		return "", time.Time{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth.battle.net/token", strings.NewReader("grant_type=client_credentials"))
	if err != nil {
		return "", time.Time{}, &Error{Code: "PROVIDER_AUTH_FAILURE", Scope: "oauth", cause: err}
	}
	req.SetBasicAuth(c.cfg.ClientID, c.cfg.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	response, err := c.do(req, "oauth")
	if err != nil {
		return "", time.Time{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", time.Time{}, c.statusError(ctx, response, "oauth")
	}
	body, err := readBody(response.Body)
	if err != nil {
		return "", time.Time{}, &Error{Code: "PROVIDER_INVALID_RESPONSE", Scope: "oauth", cause: err}
	}
	var payload struct {
		AccessToken string      `json:"access_token"`
		ExpiresIn   json.Number `json:"expires_in"`
		TokenType   string      `json:"token_type"`
	}
	if err = decode(body, &payload); err != nil || payload.AccessToken == "" || len(payload.AccessToken) > 8192 || !strings.EqualFold(payload.TokenType, "bearer") {
		return "", time.Time{}, &Error{Code: "PROVIDER_INVALID_RESPONSE", Scope: "oauth"}
	}
	seconds, err := payload.ExpiresIn.Int64()
	if err != nil || seconds <= 0 {
		return "", time.Time{}, &Error{Code: "PROVIDER_INVALID_RESPONSE", Scope: "oauth"}
	}
	if seconds > 7*24*3600 {
		seconds = 7 * 24 * 3600
	}
	margin := int64(60)
	if seconds < 120 {
		margin = seconds / 2
	}
	return payload.AccessToken, c.cfg.Now().Add(time.Duration(seconds-margin) * time.Second), nil
}

func (c *Client) invalidate(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.accessToken == token {
		c.accessToken = ""
		c.expiresAt = time.Time{}
		c.tokenError = nil
	}
}

func (c *Client) get(ctx context.Context, region, namespace, path, scope string) ([]byte, http.Header, error) {
	if region != "eu" && region != "us" {
		return nil, nil, &Error{Code: "PROVIDER_INVALID_RESPONSE", Scope: scope}
	}
	host := region + ".api.blizzard.com"
	query := url.Values{"namespace": {namespace + "-" + region}, "locale": {"en_US"}}
	if region == "eu" {
		query.Set("locale", "en_GB")
	}
	target := "https://" + host + path + "?" + query.Encode()
	for attempt := 0; attempt < 2; attempt++ {
		token, err := c.token(ctx)
		if err != nil {
			return nil, nil, err
		}
		if err := c.permit(ctx); err != nil {
			return nil, nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, nil, &Error{Code: "PROVIDER_INVALID_RESPONSE", Scope: scope}
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		response, err := c.do(req, scope)
		if err != nil {
			return nil, nil, err
		}
		if response.StatusCode == http.StatusUnauthorized && attempt == 0 {
			response.Body.Close()
			c.invalidate(token)
			continue
		}
		if response.StatusCode != http.StatusOK {
			err := c.statusError(ctx, response, scope)
			response.Body.Close()
			return nil, nil, err
		}
		body, err := readBody(response.Body)
		response.Body.Close()
		if err != nil {
			return nil, nil, &Error{Code: "PROVIDER_INVALID_RESPONSE", Scope: scope, cause: err}
		}
		return body, response.Header, nil
	}
	return nil, nil, &Error{Code: "PROVIDER_AUTH_FAILURE", Scope: scope}
}

func (c *Client) do(req *http.Request, scope string) (*http.Response, error) {
	u := req.URL
	if u.Scheme != "https" || u.User != nil || u.Port() != "" || (u.Host != "oauth.battle.net" && u.Host != "eu.api.blizzard.com" && u.Host != "us.api.blizzard.com") {
		return nil, &Error{Code: "PROVIDER_INVALID_RESPONSE", Scope: scope}
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, &Error{Code: "PROVIDER_UNAVAILABLE", Scope: scope, Retryable: true, cause: err}
	}
	return response, nil
}

func (c *Client) statusError(ctx context.Context, response *http.Response, scope string) error {
	e := &Error{Scope: scope}
	switch response.StatusCode {
	case 401:
		e.Code = "PROVIDER_AUTH_FAILURE"
	case 403:
		e.Code = "PROVIDER_ACCESS_DENIED"
	case 404:
		if scope == "profile" {
			e.Code = "CHARACTER_UNAVAILABLE"
		} else {
			e.Code = "SECTION_UNAVAILABLE"
		}
	case 429:
		e.Code = "PROVIDER_RATE_LIMITED"
		e.Retryable = true
		next := retryAfter(response.Header.Get("Retry-After"), c.cfg.Now())
		e.RetryAt = &next
		if c.cfg.PauseUntil != nil {
			if err := c.cfg.PauseUntil(ctx, next); err != nil {
				return err
			}
		}
	default:
		if response.StatusCode >= 500 {
			e.Code = "PROVIDER_UNAVAILABLE"
			e.Retryable = true
		} else {
			e.Code = "PROVIDER_INVALID_RESPONSE"
		}
	}
	return e
}

func retryAfter(value string, now time.Time) time.Time {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds >= 0 && seconds <= int64((1<<63-1)/int64(time.Second)) {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if t, err := http.ParseTime(value); err == nil && t.After(now) {
		return t
	}
	return now.Add(time.Minute)
}

func readBody(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxResponseBytes {
		return nil, errors.New("provider response exceeds size limit")
	}
	return body, nil
}

func decode(body []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("provider response contains trailing JSON")
	}
	return nil
}
