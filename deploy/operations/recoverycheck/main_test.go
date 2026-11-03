package main

import "testing"

func TestRecoveryTargetCannotSelectApplicationDatabase(t *testing.T) {
	valid := "postgres://kipet:kipet@127.0.0.1:5432/kipet_recovery_drill_0123456789abcdef?sslmode=disable"
	token := "0123456789abcdef0123456789abcdef"
	if _, err := validateTarget(valid, token); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"postgres://kipet:kipet@127.0.0.1:5432/kipet",
		"postgres://kipet:kipet@127.0.0.1:5432/postgres",
		"postgres://kipet:kipet@127.0.0.1:5432/kipet_recovery_drill_0123456789abcdef/extra",
		"postgres://kipet:kipet@other-host/kipet_recovery_drill_0123456789abcdef",
		"http://127.0.0.1/kipet_recovery_drill_0123456789abcdef",
		valid + "&dbname=kipet",
		valid + "&host=other-host",
	} {
		if _, err := validateTarget(raw, token); err == nil {
			t.Fatalf("unsafe target accepted: %s", raw)
		}
	}
	for _, badToken := range []string{"", "token", "0123456789abcdef0123456789abcdef0z"} {
		if _, err := validateTarget(valid, badToken); err == nil {
			t.Fatal("invalid guard token accepted")
		}
	}
}
