package main

import (
	"os"
	"testing"
)

// clearGatewayEnv resets every variable loadEnv reads, so one test cannot
// see another's os.Setenv calls.
func clearGatewayEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"DATABASE_URL", "REDIS_URL", "NATS_URL", "TELEGRAM_API_BASE_URL",
		"GATEWAY_INSTANCE_ID", "LOG_LEVEL", "TORN_CONFIG", "TORN_LOCALES_DIR",
		"TORN_COMMANDS", "TORN_ACTIONS",
	} {
		t.Setenv(k, "")
	}
}

// TestLoadEnvDefaultsInstanceIDToHostname is the scale-out fix: N replicas
// started from the same compose service (the same env_file, so no per-replica
// GATEWAY_INSTANCE_ID) must not all claim the bot lease under the same
// identity. Without an explicit value, loadEnv now falls back to the
// hostname — unique per container — exactly like cmd/scheduler already does.
func TestLoadEnvDefaultsInstanceIDToHostname(t *testing.T) {
	clearGatewayEnv(t)
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("REDIS_URL", "redis://x")
	t.Setenv("NATS_URL", "nats://x")

	e, err := loadEnv()
	if err != nil {
		t.Fatalf("loadEnv: %v", err)
	}

	host, herr := os.Hostname()
	if herr != nil {
		t.Fatalf("os.Hostname: %v", herr)
	}
	if e.gatewayInstanceID != host {
		t.Errorf("gatewayInstanceID = %q, want the hostname %q", e.gatewayInstanceID, host)
	}
}

// TestLoadEnvKeepsAnExplicitInstanceID: an operator who sets one by hand
// still gets exactly that value, not the hostname.
func TestLoadEnvKeepsAnExplicitInstanceID(t *testing.T) {
	clearGatewayEnv(t)
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("REDIS_URL", "redis://x")
	t.Setenv("NATS_URL", "nats://x")
	t.Setenv("GATEWAY_INSTANCE_ID", "gateway-eu-01")

	e, err := loadEnv()
	if err != nil {
		t.Fatalf("loadEnv: %v", err)
	}
	if e.gatewayInstanceID != "gateway-eu-01" {
		t.Errorf("gatewayInstanceID = %q, want the explicit value", e.gatewayInstanceID)
	}
}

// TestLoadEnvStillRequiresTheRest: the hostname default is scoped to the
// instance id alone; the other required variables are still required.
func TestLoadEnvStillRequiresTheRest(t *testing.T) {
	clearGatewayEnv(t)

	if _, err := loadEnv(); err == nil {
		t.Fatal("loadEnv succeeded with no DATABASE_URL, REDIS_URL or NATS_URL set")
	}
}
