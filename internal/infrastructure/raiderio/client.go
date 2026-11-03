// Package raiderio reads the official public Raider.IO profile API.
package raiderio

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/raiderio"
)

const (
	profileEndpoint  = "https://raider.io/api/v1/characters/profile"
	profileFields    = "gear,talents,guild,mythic_plus_scores_by_season:current,mythic_plus_best_runs,raid_progression"
	guildEndpoint    = "https://raider.io/api/v1/guilds/profile"
	guildFields      = "members,raid_progression,raid_rankings"
	requestDeadline  = 10 * time.Second
	maxResponseBytes = 2 << 20
)

type Config struct {
	Permit     func(context.Context) error
	PauseUntil func(context.Context, time.Time) error
	HTTPClient *http.Client
	Now        func() time.Time
}

type Client struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config) (*Client, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	client := http.Client{Timeout: requestDeadline}
	if cfg.HTTPClient != nil {
		client = *cfg.HTTPClient
	}
	if client.Timeout <= 0 || client.Timeout > requestDeadline {
		client.Timeout = requestDeadline
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{cfg: cfg, http: &client}, nil
}

func (c *Client) Fetch(ctx context.Context, key raiderio.Key) (raiderio.Snapshot, error) {
	var endpoint, fields, missingCode string
	var err error
	switch key.Kind {
	case "":
		endpoint, fields, missingCode = profileEndpoint, profileFields, "CHARACTER_UNAVAILABLE"
		key, err = raiderio.NormalizeKey(key.Region, key.Realm, key.Name)
	case "guild":
		endpoint, fields, missingCode = guildEndpoint, guildFields, "GUILD_UNAVAILABLE"
		key, err = raiderio.NormalizeGuildKey(key.Region, key.Realm, key.Name)
	default:
		return raiderio.Snapshot{}, character.NewFault("INVALID_ARGUMENT", false)
	}
	if err != nil {
		return raiderio.Snapshot{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, requestDeadline)
	defer cancel()
	if c.cfg.Permit != nil {
		if err := c.cfg.Permit(ctx); err != nil {
			return raiderio.Snapshot{}, err
		}
	}
	query := url.Values{
		"region": {key.Region},
		"realm":  {key.Realm},
		"name":   {key.Name},
		"fields": {fields},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return raiderio.Snapshot{}, character.NewFault("INVALID_ARGUMENT", false)
	}
	req.Header.Set("Accept", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return raiderio.Snapshot{}, character.NewFault("PROVIDER_UNAVAILABLE", true)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return raiderio.Snapshot{}, c.statusError(ctx, response, missingCode)
	}
	if response.ContentLength > maxResponseBytes {
		return raiderio.Snapshot{}, invalid()
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return raiderio.Snapshot{}, character.NewFault("PROVIDER_UNAVAILABLE", true)
	}
	if len(body) > maxResponseBytes || !utf8.Valid(body) {
		return raiderio.Snapshot{}, invalid()
	}
	now := c.cfg.Now().UTC()
	snapshot := raiderio.Snapshot{
		FetchedAt:  now,
		FreshUntil: now.Add(raiderio.FreshFor), ValidUntil: now.Add(raiderio.KeepFor),
	}
	if key.Kind == "guild" {
		snapshot.Guild, err = normalizeGuildProfile(key, body)
	} else {
		snapshot.Profile, err = normalizeProfile(key, body)
	}
	if err != nil {
		return raiderio.Snapshot{}, err
	}
	return snapshot, nil
}

func invalid() *character.Fault {
	return character.NewFault("PROVIDER_INVALID_RESPONSE", false)
}

func (c *Client) statusError(ctx context.Context, response *http.Response, missingCode string) error {
	switch response.StatusCode {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return character.NewFault("INVALID_ARGUMENT", false)
	case http.StatusUnauthorized:
		return character.NewFault("PROVIDER_AUTH_FAILURE", false)
	case http.StatusForbidden:
		return character.NewFault("PROVIDER_ACCESS_DENIED", false)
	case http.StatusNotFound, http.StatusGone:
		return character.NewFault(missingCode, false)
	case http.StatusTooManyRequests:
		now := c.cfg.Now().UTC()
		until := retryUntil(response.Header, now)
		if c.cfg.PauseUntil != nil {
			if err := c.cfg.PauseUntil(ctx, until); err != nil {
				return &character.Fault{Code: "INFRASTRUCTURE_UNAVAILABLE", Retryable: true, RetryAt: until}
			}
		}
		return &character.Fault{Code: "PROVIDER_RATE_LIMITED", Retryable: true, RetryAt: until}
	case http.StatusRequestTimeout:
		return character.NewFault("PROVIDER_UNAVAILABLE", true)
	default:
		if response.StatusCode >= http.StatusInternalServerError {
			return character.NewFault("PROVIDER_UNAVAILABLE", true)
		}
		return invalid()
	}
}

func retryUntil(headers http.Header, now time.Time) time.Time {
	retry := strings.TrimSpace(headers.Get("Retry-After"))
	if seconds, err := strconv.ParseInt(retry, 10, 64); err == nil && seconds >= 0 {
		const maxSeconds = int64((1<<63 - 1) / time.Second)
		return now.Add(time.Duration(min(seconds, maxSeconds)) * time.Second)
	}
	if date, err := http.ParseTime(retry); err == nil && date.After(now) {
		return date
	}
	if unix, err := strconv.ParseInt(strings.TrimSpace(headers.Get("X-RateLimit-Reset")), 10, 64); err == nil {
		const lastSupportedUnix = 253402300799 // 9999-12-31T23:59:59Z
		if unix > now.Unix() && unix <= lastSupportedUnix {
			return time.Unix(unix, 0).UTC()
		}
	}
	return now.Add(time.Minute)
}
