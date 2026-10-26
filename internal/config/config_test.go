package config

import (
	"testing"
	"time"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"PAKKA_ADDR", "PAKKA_DNS_HOST", "PAKKA_TCP_ADDR", "PAKKA_HEALTH_URL",
		"PAKKA_CHECK_TIMEOUT", "PAKKA_MAX_RUNS",
		"PAKKA_META_API_KEY", "META_API_KEY", "PAKKA_META_API_BASE", "META_API_BASE",
		"PAKKA_META_MODEL", "PAKKA_INVESTIGATE_STEPS",
	} {
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
	if cfg.MetaAPIKey != "" {
		t.Errorf("MetaAPIKey set by default")
	}
	if cfg.MetaAPIBase != "https://api.meta.ai/v1" {
		t.Errorf("MetaAPIBase = %q", cfg.MetaAPIBase)
	}
	if cfg.MetaModel != "muse-spark-1.3-contributor" {
		t.Errorf("MetaModel = %q", cfg.MetaModel)
	}
	if cfg.InvestigateSteps != 5 {
		t.Errorf("InvestigateSteps = %d", cfg.InvestigateSteps)
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
	t.Setenv("PAKKA_META_API_KEY", "k")
	t.Setenv("PAKKA_META_MODEL", "other-model")
	t.Setenv("PAKKA_INVESTIGATE_STEPS", "3")
	cfg := Load()
	if cfg.Addr != "0.0.0.0:24000" || cfg.DNSHost != "example.invalid" ||
		cfg.TCPAddr != "192.0.2.1:443" || cfg.HealthURL != "https://example.invalid/health" {
		t.Errorf("overrides not applied: %+v", cfg)
	}
	if cfg.CheckTimeout != 2*time.Second || cfg.MaxRuns != 2 {
		t.Errorf("numeric overrides not applied: %+v", cfg)
	}
	if cfg.MetaAPIKey != "k" || cfg.MetaModel != "other-model" || cfg.InvestigateSteps != 3 {
		t.Errorf("meta overrides not applied: %+v", cfg)
	}
}

func TestMetaFallbackVars(t *testing.T) {
	clearEnv(t)
	t.Setenv("META_API_KEY", "fallback-key")
	t.Setenv("META_API_BASE", "https://fallback.invalid/v1")
	if cfg := Load(); cfg.MetaAPIKey != "fallback-key" || cfg.MetaAPIBase != "https://fallback.invalid/v1" {
		t.Errorf("fallback not applied: %+v", cfg)
	}
	t.Setenv("PAKKA_META_API_KEY", "primary-key")
	if cfg := Load(); cfg.MetaAPIKey != "primary-key" {
		t.Errorf("PAKKA_ prefix should win: %+v", cfg)
	}
}

func TestBadValuesFallBack(t *testing.T) {
	clearEnv(t)
	t.Setenv("PAKKA_CHECK_TIMEOUT", "nope")
	t.Setenv("PAKKA_MAX_RUNS", "-3")
	t.Setenv("PAKKA_INVESTIGATE_STEPS", "0")
	cfg := Load()
	if cfg.CheckTimeout != 5*time.Second {
		t.Errorf("CheckTimeout = %s, want default", cfg.CheckTimeout)
	}
	if cfg.MaxRuns != 4 {
		t.Errorf("MaxRuns = %d, want default", cfg.MaxRuns)
	}
	if cfg.InvestigateSteps != 5 {
		t.Errorf("InvestigateSteps = %d, want default", cfg.InvestigateSteps)
	}
}
