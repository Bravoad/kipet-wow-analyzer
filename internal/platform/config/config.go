package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DatabaseURL, RedisURL, APIAddr, InternalAddr, ProviderMode, ClientID, ClientSecret, HMACSecret string
	CombatMode, CombatDatabaseURL, WCLClientID, WCLClientSecret                                    string
	WorkerInternalAddr                                                                             string
	CombatSetupRequired, RaiderEnabled                                                             bool
	Workers, Replicas                                                                              int
	TrustedProxies                                                                                 []netip.Prefix
}

func Load() (Config, error) {
	c := Config{DatabaseURL: env("DATABASE_URL", "postgres://kipet:kipet@127.0.0.1:5432/kipet?sslmode=disable"), RedisURL: env("REDIS_URL", "redis://127.0.0.1:6379/0"), APIAddr: env("API_ADDR", "127.0.0.1:8080"), InternalAddr: env("INTERNAL_ADDR", "127.0.0.1:9091"), ProviderMode: env("PROVIDER_MODE", "blizzard"), ClientID: os.Getenv("BLIZZARD_CLIENT_ID"), ClientSecret: os.Getenv("BLIZZARD_CLIENT_SECRET"), HMACSecret: os.Getenv("APP_HMAC_SECRET"), Workers: 4, Replicas: 1}
	c.WCLClientID, c.WCLClientSecret = env("WCL_CLIENT_ID", ""), env("WCL_CLIENT_SECRET", "")
	c.CombatDatabaseURL = env("COMBAT_DATABASE_URL", "")
	c.WorkerInternalAddr = env("WORKER_INTERNAL_ADDR", "127.0.0.1:9092")
	combatCredentialsReady := c.WCLClientID != "" && c.WCLClientSecret != ""
	switch mode := env("COMBAT_PROVIDER_MODE", ""); mode {
	case "":
		if (c.WCLClientID == "") != (c.WCLClientSecret == "") {
			return c, fmt.Errorf("WCL_CLIENT_ID and WCL_CLIENT_SECRET must be configured together")
		}
		c.CombatMode = "disabled"
		if c.ProviderMode == "fixture" {
			c.CombatMode = "fixture"
		} else if combatCredentialsReady {
			c.CombatMode = "warcraftlogs"
		}
	case "fixture", "disabled":
		c.CombatMode = mode
	case "warcraftlogs":
		c.CombatMode = "warcraftlogs"
		if !combatCredentialsReady {
			c.CombatMode = "disabled"
			c.CombatSetupRequired = true
		}
	default:
		return c, fmt.Errorf("COMBAT_PROVIDER_MODE must be fixture, warcraftlogs or disabled")
	}
	if v := env("RAIDERIO_ENABLED", ""); v != "" {
		var err error
		c.RaiderEnabled, err = strconv.ParseBool(v)
		if err != nil {
			return c, fmt.Errorf("RAIDERIO_ENABLED must be a boolean")
		}
	}
	if value := os.Getenv("TRUSTED_PROXY_CIDRS"); value != "" {
		for _, raw := range strings.Split(value, ",") {
			prefix, e := netip.ParsePrefix(strings.TrimSpace(raw))
			if e != nil {
				return c, fmt.Errorf("invalid TRUSTED_PROXY_CIDRS")
			}
			c.TrustedProxies = append(c.TrustedProxies, prefix)
		}
	}
	if c.ProviderMode != "blizzard" && c.ProviderMode != "fixture" {
		return c, fmt.Errorf("PROVIDER_MODE must be blizzard or fixture")
	}
	if c.ProviderMode == "blizzard" && (c.ClientID == "" || c.ClientSecret == "") {
		return c, fmt.Errorf("BLIZZARD_CLIENT_ID and BLIZZARD_CLIENT_SECRET are required in blizzard mode")
	}
	if len(c.HMACSecret) < 32 {
		return c, fmt.Errorf("APP_HMAC_SECRET must contain at least 32 bytes")
	}
	if c.ProviderMode == "blizzard" || c.CombatMode == "warcraftlogs" {
		for _, demoPrefix := range []string{"local-development-", "ci-synthetic-", "change-me"} {
			if strings.HasPrefix(c.HMACSecret, demoPrefix) {
				return c, fmt.Errorf("APP_HMAC_SECRET must be replaced with a private random value for a live provider")
			}
		}
	}
	if v := os.Getenv("WORKER_CONCURRENCY"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 32 {
			return c, fmt.Errorf("WORKER_CONCURRENCY must be 1..32")
		}
		c.Workers = n
	}
	if v := os.Getenv("API_REPLICAS"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 100 {
			return c, fmt.Errorf("API_REPLICAS must be 1..100")
		}
		c.Replicas = n
	}
	for _, endpoint := range []string{c.DatabaseURL, c.RedisURL} {
		u, e := url.Parse(endpoint)
		if e != nil || u.Host == "" {
			return c, fmt.Errorf("invalid dependency URL")
		}
	}
	if c.CombatDatabaseURL != "" {
		u, err := url.Parse(c.CombatDatabaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
			return c, fmt.Errorf("COMBAT_DATABASE_URL must be a PostgreSQL URL")
		}
	}
	return c, nil
}
func env(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}
