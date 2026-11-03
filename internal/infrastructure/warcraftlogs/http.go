// Package warcraftlogs accesses only the public client GraphQL API.
package warcraftlogs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
)

const maxResponseBytes int64 = 4 << 20

type Config struct {
	ClientID, ClientSecret string
	HTTPClient             *http.Client
	Now                    func() time.Time
	Permit                 func(context.Context) error
	PauseUntil             func(context.Context, time.Time) error
	ObserveUsage           func(context.Context, float64, time.Time) error
}
type Usage struct {
	LimitPerHour, PointsSpentThisHour float64
	ResetsAt, ObservedAt              time.Time
}
type Client struct {
	cfg             Config
	http            *http.Client
	mu              sync.Mutex
	accessToken     string
	expiresAt       time.Time
	refreshing      chan struct{}
	tokenError      error
	tokenErrorUntil time.Time
	usage           *Usage
}

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.ClientID) == "" || strings.TrimSpace(cfg.ClientSecret) == "" {
		return nil, errors.New("Warcraft Logs server credentials are required")
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
func (c *Client) Usage() *Usage {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.usage == nil {
		return nil
	}
	copy := *c.usage
	return &copy
}
func (c *Client) permit(ctx context.Context) error {
	c.mu.Lock()
	usage := c.usage
	if usage != nil && usage.PointsSpentThisHour >= usage.LimitPerHour && c.cfg.Now().Before(usage.ResetsAt) {
		until := usage.ResetsAt
		c.mu.Unlock()
		return &character.Fault{Code: "PROVIDER_RATE_LIMITED", Retryable: true, RetryAt: until}
	}
	c.mu.Unlock()
	if c.cfg.Permit != nil {
		return c.cfg.Permit(ctx)
	}
	return nil
}
func (c *Client) token(ctx context.Context) (string, error) {
	for {
		c.mu.Lock()
		if c.accessToken != "" && c.cfg.Now().Before(c.expiresAt) {
			token := c.accessToken
			c.mu.Unlock()
			return token, nil
		}
		if c.tokenError != nil && c.cfg.Now().Before(c.tokenErrorUntil) {
			err := c.tokenError
			c.mu.Unlock()
			return "", err
		}
		if pending := c.refreshing; pending != nil {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return "", character.NewFault("PROVIDER_UNAVAILABLE", true)
			case <-pending:
				continue
			}
		}
		pending := make(chan struct{})
		c.refreshing = pending
		c.mu.Unlock()
		token, expiry, err := c.fetchToken(ctx)
		c.mu.Lock()
		if err == nil {
			c.accessToken = token
			c.expiresAt = expiry
			c.tokenError = nil
		} else if ctx.Err() == nil {
			c.tokenError = err
			c.tokenErrorUntil = c.cfg.Now().Add(5 * time.Second)
		}
		c.refreshing = nil
		close(pending)
		c.mu.Unlock()
		return token, err
	}
}
func (c *Client) fetchToken(ctx context.Context) (string, time.Time, error) {
	if err := c.permit(ctx); err != nil {
		return "", time.Time{}, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.warcraftlogs.com/oauth/token", strings.NewReader("grant_type=client_credentials"))
	req.SetBasicAuth(c.cfg.ClientID, c.cfg.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	response, err := c.do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", time.Time{}, c.statusError(ctx, response, true)
	}
	body, err := readBody(response.Body)
	if err != nil {
		return "", time.Time{}, invalid()
	}
	var value struct {
		AccessToken string      `json:"access_token"`
		TokenType   string      `json:"token_type"`
		ExpiresIn   json.Number `json:"expires_in"`
	}
	if decode(body, &value) != nil || value.AccessToken == "" || len(value.AccessToken) > 8192 || !strings.EqualFold(value.TokenType, "bearer") {
		return "", time.Time{}, invalid()
	}
	seconds, err := value.ExpiresIn.Int64()
	if err != nil || seconds < 1 {
		return "", time.Time{}, invalid()
	}
	if seconds > 7*24*3600 {
		seconds = 7 * 24 * 3600
	}
	margin := int64(60)
	if seconds < 120 {
		margin = seconds / 2
	}
	return value.AccessToken, c.cfg.Now().Add(time.Duration(seconds-margin) * time.Second), nil
}
func (c *Client) invalidate(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.accessToken == token {
		c.accessToken = ""
		c.tokenError = nil
		c.expiresAt = time.Time{}
	}
}

// query is private: callers cannot supply an arbitrary query or destination.
func (c *Client) query(ctx context.Context, query string, variables map[string]any) (object, error) {
	encoded, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
	for attempt := 0; attempt < 2; attempt++ {
		token, err := c.token(ctx)
		if err != nil {
			return nil, err
		}
		if err = c.permit(ctx); err != nil {
			return nil, err
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.warcraftlogs.com/api/v2/client", bytes.NewReader(encoded))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		response, err := c.do(req)
		if err != nil {
			return nil, err
		}
		if response.StatusCode == 401 && attempt == 0 {
			response.Body.Close()
			c.invalidate(token)
			continue
		}
		if response.StatusCode != 200 {
			err := c.statusError(ctx, response, false)
			response.Body.Close()
			return nil, err
		}
		body, err := readBody(response.Body)
		response.Body.Close()
		if err != nil {
			return nil, invalid()
		}
		var envelope object
		if decode(body, &envelope) != nil || envelope == nil {
			return nil, invalid()
		}
		data := obj(envelope["data"])
		var graphErr error
		if graphErrors, present := envelope["errors"]; present && graphErrors != nil {
			list, ok := graphErrors.([]any)
			if !ok {
				graphErr = invalid()
			}
			if len(list) > 0 {
				graphErr = graphError(list)
			}
		}
		usageErr := c.observe(ctx, obj(data["rateLimitData"]))
		// A known denial must reach the worker even when usage persistence fails,
		// so an operational budget error cannot suppress quarantine/purge.
		if graphErr != nil {
			return nil, graphErr
		}
		if usageErr != nil {
			return nil, usageErr
		}
		if data == nil {
			return nil, invalid()
		}
		return data, nil
	}
	return nil, character.NewFault("PROVIDER_AUTH_FAILURE", false)
}
func (c *Client) observe(ctx context.Context, raw object) error {
	if raw == nil {
		return nil
	}
	limit, spent, reset := amount(raw["limitPerHour"]), amount(raw["pointsSpentThisHour"]), amount(raw["pointsResetIn"])
	if limit == nil || spent == nil || reset == nil || *limit <= 0 || *reset > 7*24*3600 {
		return nil
	}
	now := c.cfg.Now().UTC()
	until := now.Add(time.Duration(*reset * float64(time.Second)))
	usage := &Usage{LimitPerHour: *limit, PointsSpentThisHour: *spent, ResetsAt: until, ObservedAt: now}
	c.mu.Lock()
	c.usage = usage
	c.mu.Unlock()
	remaining := *limit - *spent
	if remaining < 0 {
		remaining = 0
	}
	if c.cfg.ObserveUsage != nil {
		if err := c.cfg.ObserveUsage(ctx, remaining, until); err != nil {
			return err
		}
	}
	if remaining == 0 && c.cfg.PauseUntil != nil {
		return c.cfg.PauseUntil(ctx, until)
	}
	return nil
}
func graphError(errors []any) error {
	// No provider message is reflected to the caller. A partial GraphQL success
	// is never publishable because permission and completeness are ambiguous.
	for _, value := range errors {
		raw := obj(value)
		code := strings.ToUpper(text(obj(raw["extensions"])["code"]))
		switch code {
		case "UNAUTHENTICATED", "UNAUTHORIZED":
			return character.NewFault("PROVIDER_AUTH_FAILURE", false)
		case "FORBIDDEN", "ACCESS_DENIED":
			return character.NewFault("PROVIDER_ACCESS_DENIED", false)
		case "NOT_FOUND":
			return character.NewFault("REPORT_UNAVAILABLE", false)
		}
	}
	return invalid()
}
func (c *Client) do(req *http.Request) (*http.Response, error) {
	u := req.URL
	if u.Scheme != "https" || u.User != nil || u.Host != "www.warcraftlogs.com" || u.Port() != "" || (u.Path != "/oauth/token" && u.Path != "/api/v2/client") || u.RawQuery != "" {
		return nil, invalid()
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, character.NewFault("PROVIDER_UNAVAILABLE", true)
	}
	return response, nil
}
func (c *Client) statusError(ctx context.Context, response *http.Response, oauth bool) error {
	if oauth && response.StatusCode != 429 && response.StatusCode < 500 {
		return character.NewFault("PROVIDER_AUTH_FAILURE", false)
	}
	switch response.StatusCode {
	case 401:
		return character.NewFault("PROVIDER_AUTH_FAILURE", false)
	case 403:
		return character.NewFault("PROVIDER_ACCESS_DENIED", false)
	case 404:
		return character.NewFault("REPORT_UNAVAILABLE", false)
	case 429:
		until := retryAfter(response.Header.Get("Retry-After"), c.cfg.Now())
		if c.cfg.PauseUntil != nil {
			if err := c.cfg.PauseUntil(ctx, until); err != nil {
				return err
			}
		}
		return &character.Fault{Code: "PROVIDER_RATE_LIMITED", Retryable: true, RetryAt: until}
	default:
		if response.StatusCode >= 500 {
			return character.NewFault("PROVIDER_UNAVAILABLE", true)
		}
		return invalid()
	}
}
func retryAfter(value string, now time.Time) time.Time {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds >= 0 && seconds <= int64((1<<63-1)/int64(time.Second)) {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at
	}
	return now.Add(time.Minute)
}
func readBody(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxResponseBytes+1))
	if err != nil || int64(len(body)) > maxResponseBytes {
		return nil, errors.New("invalid provider body")
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
	if d.Decode(&extra) != io.EOF {
		return errors.New("invalid trailing data")
	}
	return nil
}
func invalid() error { return character.NewFault("PROVIDER_INVALID_RESPONSE", false) }
