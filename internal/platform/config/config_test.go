package config

import "testing"

func TestCombatConfigurationKeepsSourcesExplicit(t *testing.T) {
	for _, tc := range []struct {
		mode, id, secret, want string
		invalid                bool
	}{
		{"fixture", "", "", "fixture", false},
		{"fixture", "configured", "configured", "fixture", false},
		{"blizzard", "", "", "disabled", false},
		{"blizzard", "configured", "configured", "warcraftlogs", false},
		{"blizzard", "configured", "", "", true},
	} {
		t.Run(tc.mode+tc.want+tc.secret, func(t *testing.T) {
			t.Setenv("COMBAT_PROVIDER_MODE", "")
			t.Setenv("PROVIDER_MODE", tc.mode)
			t.Setenv("BLIZZARD_CLIENT_ID", "synthetic-test")
			t.Setenv("BLIZZARD_CLIENT_SECRET", "synthetic-test")
			t.Setenv("WCL_CLIENT_ID", tc.id)
			t.Setenv("WCL_CLIENT_SECRET", tc.secret)
			t.Setenv("APP_HMAC_SECRET", "synthetic-test-configuration-32bytes")
			c, err := Load()
			if tc.invalid {
				if err == nil {
					t.Fatal("incomplete credentials accepted")
				}
				return
			}
			if err != nil || c.CombatMode != tc.want {
				t.Fatalf("mode=%s err=%v", c.CombatMode, err)
			}
		})
	}
}

func TestExplicitWarcraftLogsKeepsOtherSourcesAvailableDuringSetup(t *testing.T) {
	t.Setenv("PROVIDER_MODE", "fixture")
	t.Setenv("COMBAT_PROVIDER_MODE", "warcraftlogs")
	t.Setenv("COMBAT_DATABASE_URL", "postgres://user@localhost/combat")
	t.Setenv("WCL_CLIENT_ID", "configured")
	t.Setenv("WCL_CLIENT_SECRET", "")
	t.Setenv("APP_HMAC_SECRET", "synthetic-test-configuration-32bytes")
	t.Setenv("RAIDERIO_ENABLED", "true")
	c, err := Load()
	if err != nil || c.CombatMode != "disabled" || !c.CombatSetupRequired || !c.RaiderEnabled {
		t.Fatalf("partial WCL setup must keep Raider.IO available: %v", err)
	}
	t.Setenv("WCL_CLIENT_SECRET", "configured")
	c, err = Load()
	if err != nil || c.CombatMode != "warcraftlogs" || c.CombatSetupRequired || c.CombatDatabaseURL != "postgres://user@localhost/combat" {
		t.Fatalf("explicit WCL setup must enable real reports independently: %v", err)
	}
}

func TestLiveSourceRejectsKnownDemoHMAC(t *testing.T) {
	t.Setenv("COMBAT_PROVIDER_MODE", "")
	t.Setenv("PROVIDER_MODE", "blizzard")
	t.Setenv("BLIZZARD_CLIENT_ID", "synthetic-test")
	t.Setenv("BLIZZARD_CLIENT_SECRET", "synthetic-test")
	t.Setenv("WCL_CLIENT_ID", "")
	t.Setenv("WCL_CLIENT_SECRET", "")
	for _, secret := range []string{"local-development-only-replace-this-before-real-use", "ci-synthetic-only-do-not-use-in-production"} {
		t.Setenv("APP_HMAC_SECRET", secret)
		if _, err := Load(); err == nil {
			t.Fatal("known demonstration secret accepted for live source")
		}
	}
	t.Setenv("PROVIDER_MODE", "fixture")
	if _, err := Load(); err != nil {
		t.Fatal("explicit local fixture configuration rejected", err)
	}
	t.Setenv("COMBAT_PROVIDER_MODE", "warcraftlogs")
	t.Setenv("WCL_CLIENT_ID", "configured")
	t.Setenv("WCL_CLIENT_SECRET", "configured")
	if _, err := Load(); err == nil {
		t.Fatal("known demonstration secret accepted for live combat source")
	}
}
