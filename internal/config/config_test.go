package config

import (
	"testing"
	"time"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"PAKKA_ADDR", "PAKKA_DNS_HOST", "PAKKA_TCP_ADDR", "PAKKA_HEALTH_URL", "PAKKA_CHECK_TIMEOUT", "PAKKA_MAX_RUNS"} {
		t.Setenv(key, "")
	}
}

func TestDefaults(t *testing.T) {
	clearEnv(t)
	cfg := Load()
	if cfg.Addr != "127.0.0.1:24800" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.DNSHost != "localhost" {
		t.Errorf("DNSHost = %q", cfg.DNSHost)
	}
	if cfg.TCPAddr != "" || cfg.HealthURL != "" {
		t.Errorf("targets = %q/%q, want empty (skipped)", cfg.TCPAddr, cfg.HealthURL)
	}
	if cfg.CheckTimeout != 5*time.Second {
		t.Errorf("CheckTimeout = %s", cfg.CheckTimeout)
	}
	if cfg.MaxRuns != 4 {
		t.Errorf("MaxRuns = %d", cfg.MaxRuns)
	}
}

func TestOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("PAKKA_ADDR", "0.0.0.0:24000")
	t.Setenv("PAKKA_DNS_HOST", "example.invalid")
	t.Setenv("PAKKA_TCP_ADDR", "192.0.2.1:443")
	t.Setenv("PAKKA_HEALTH_URL", "https://example.invalid/health")
	t.Setenv("PAKKA_CHECK_TIMEOUT", "2s")
	t.Setenv("PAKKA_MAX_RUNS", "2")
	cfg := Load()
	if cfg.Addr != "0.0.0.0:24000" || cfg.DNSHost != "example.invalid" ||
		cfg.TCPAddr != "192.0.2.1:443" || cfg.HealthURL != "https://example.invalid/health" {
		t.Errorf("overrides not applied: %+v", cfg)
	}
	if cfg.CheckTimeout != 2*time.Second || cfg.MaxRuns != 2 {
		t.Errorf("numeric overrides not applied: %+v", cfg)
	}
}

func TestBadValuesFallBack(t *testing.T) {
	clearEnv(t)
	t.Setenv("PAKKA_CHECK_TIMEOUT", "nope")
	t.Setenv("PAKKA_MAX_RUNS", "-3")
	cfg := Load()
	if cfg.CheckTimeout != 5*time.Second {
		t.Errorf("CheckTimeout = %s, want default", cfg.CheckTimeout)
	}
	if cfg.MaxRuns != 4 {
		t.Errorf("MaxRuns = %d, want default", cfg.MaxRuns)
	}
}
