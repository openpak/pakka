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
		"PAKKA_DISCORD_TOKEN", "PAKKA_DISCORD_GUILDS", "PAKKA_DISCORD_PREFIX",
		"PAKKA_DISCORD_CHANNELS", "PAKKA_DISCORD_OPS_CHANNELS",
		"PAKKA_DISCORD_MOD_ROLES",
		"PAKKA_DISCORD_STATUS_CHANNEL", "PAKKA_DISCORD_WELCOME_CHANNEL",
		"PAKKA_DISCORD_WELCOME_INTERVAL", "PAKKA_STATUS_TARGETS", "PAKKA_STATUS_INTERVAL",
		"PAKKA_DISCORD_SETUP_CHANNEL", "PAKKA_DISCORD_CONNECTION_CHANNEL",
		"PAKKA_DISCORD_BUG_CHANNEL",
		"PAKKA_DISCORD_MODLOG_CHANNEL", "PAKKA_DISCORD_OPS_ALERTS_CHANNEL",
		"PAKKA_LOG_WATCH_UNITS", "PAKKA_DISCORD_GATEWAY",
		"PAKKA_ESHOP_API_BASE", "PAKKA_ESHOP_INTERNAL_KEY", "PAKKA_ESHOP_SOURCES",
		"PAKKA_ESHOP_INTERVAL", "PAKKA_GITHUB_TOKEN", "GITHUB_TOKEN",
		"PAKKA_ESHOP_SYNC_COMMAND", "PAKKA_ESHOP_SYNC_TIMEOUT", "PAKKA_ESHOP_SYNC_COOLDOWN",
		"PAKKA_DISCORD_ALERTS_CHANNEL", "PAKKA_DISCORD_ANNOUNCE_CHANNEL",
		"PAKKA_ESHOP_DIGEST_INTERVAL", "PAKKA_ESHOP_BROKEN_AFTER", "PAKKA_STATE_DIR",
		"PAKKA_DISCORD_MATCHMAKING_CHANNEL", "PAKKA_MATCHMAKING_MAX_AGE",
		"PAKKA_STATUS_DOWN_AFTER", "PAKKA_STATUS_DEPENDS",
		"PAKKA_NEWS_ADMIN_BASE", "PAKKA_NEWS_ADMIN_KEY",
		"PAKKA_RELEASE_WATCH", "PAKKA_RELEASE_STALE_AFTER",
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
	if !cfg.DiscordGateway {
		t.Error("DiscordGateway should default true")
	}
}

func TestGatewayFlag(t *testing.T) {
	clearEnv(t)
	for value, want := range map[string]bool{"false": false, "0": false, "no": false, "true": true, "1": true, "bogus": true} {
		t.Setenv("PAKKA_DISCORD_GATEWAY", value)
		if got := Load().DiscordGateway; got != want {
			t.Errorf("%q -> %v, want %v", value, got, want)
		}
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

func TestEshopDefaults(t *testing.T) {
	clearEnv(t)
	cfg := Load()
	if cfg.EshopAPIBase != "" || cfg.AlertsChannel != "" || cfg.EshopAnnounceChannel != "" {
		t.Errorf("eshop enabled by default: %+v", cfg)
	}
	if cfg.EshopInterval != time.Hour || cfg.EshopSyncTimeout != 2*time.Hour || cfg.EshopSyncCooldown != 6*time.Hour {
		t.Errorf("eshop durations = %s/%s/%s", cfg.EshopInterval, cfg.EshopSyncTimeout, cfg.EshopSyncCooldown)
	}
	if cfg.EshopDigestInterval != 168*time.Hour || cfg.EshopBrokenAfter != 168*time.Hour {
		t.Errorf("eshop digest/broken = %s/%s", cfg.EshopDigestInterval, cfg.EshopBrokenAfter)
	}
	if cfg.StateDir != "" {
		t.Errorf("StateDir = %q, want empty", cfg.StateDir)
	}
}

func TestEshopOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("PAKKA_ESHOP_API_BASE", "http://shop:20070")
	t.Setenv("PAKKA_ESHOP_INTERNAL_KEY", "k")
	t.Setenv("PAKKA_ESHOP_INTERVAL", "30m")
	t.Setenv("GITHUB_TOKEN", "gh")
	t.Setenv("PAKKA_ESHOP_SYNC_COMMAND", "./run-hb.sh sync")
	t.Setenv("PAKKA_DISCORD_ALERTS_CHANNEL", "staff1")
	t.Setenv("PAKKA_DISCORD_ANNOUNCE_CHANNEL", "public1")
	t.Setenv("PAKKA_ESHOP_DIGEST_INTERVAL", "24h")
	t.Setenv("PAKKA_ESHOP_BROKEN_AFTER", "48h")
	t.Setenv("PAKKA_STATE_DIR", "/data")
	cfg := Load()
	if cfg.EshopAPIBase != "http://shop:20070" || cfg.EshopInternalKey != "k" {
		t.Errorf("eshop api not applied: %+v", cfg)
	}
	if cfg.EshopDigestInterval != 24*time.Hour || cfg.EshopBrokenAfter != 48*time.Hour || cfg.StateDir != "/data" {
		t.Errorf("eshop digest/broken/state not applied: %+v", cfg)
	}
	if cfg.EshopInterval != 30*time.Minute || cfg.GitHubToken != "gh" || cfg.EshopSyncCommand != "./run-hb.sh sync" {
		t.Errorf("eshop overrides not applied: %+v", cfg)
	}
	if cfg.AlertsChannel != "staff1" || cfg.EshopAnnounceChannel != "public1" {
		t.Errorf("eshop channels not applied: %+v", cfg)
	}
	t.Setenv("PAKKA_GITHUB_TOKEN", "pk")
	if cfg := Load(); cfg.GitHubToken != "pk" {
		t.Errorf("PAKKA_ prefix should win for github token: %+v", cfg)
	}
}

func TestCommunityDefaults(t *testing.T) {
	clearEnv(t)
	cfg := Load()
	if cfg.MatchmakingChannel != "" {
		t.Errorf("MatchmakingChannel = %q, want empty", cfg.MatchmakingChannel)
	}
	if cfg.MatchmakingMaxAge != 24*time.Hour {
		t.Errorf("MatchmakingMaxAge = %s", cfg.MatchmakingMaxAge)
	}
	t.Setenv("PAKKA_DISCORD_MATCHMAKING_CHANNEL", "match1")
	t.Setenv("PAKKA_MATCHMAKING_MAX_AGE", "12h")
	cfg = Load()
	if cfg.MatchmakingChannel != "match1" || cfg.MatchmakingMaxAge != 12*time.Hour {
		t.Errorf("matchmaking overrides not applied: %+v", cfg)
	}
}

func TestStatusTuning(t *testing.T) {
	clearEnv(t)
	cfg := Load()
	if cfg.StatusDownAfter != 3 || cfg.StatusDependsRaw != "" {
		t.Errorf("status tuning = %d/%q", cfg.StatusDownAfter, cfg.StatusDependsRaw)
	}
	t.Setenv("PAKKA_STATUS_DOWN_AFTER", "1")
	t.Setenv("PAKKA_STATUS_DEPENDS", "a=b")
	cfg = Load()
	if cfg.StatusDownAfter != 1 || cfg.StatusDependsRaw != "a=b" {
		t.Errorf("status tuning not applied: %+v", cfg)
	}
}

func TestNewsReleases(t *testing.T) {
	clearEnv(t)
	cfg := Load()
	if cfg.NewsAdminBase != "http://openpak-news-experiment:20043" || cfg.NewsAdminKey != "" {
		t.Errorf("news admin = %q/%q", cfg.NewsAdminBase, cfg.NewsAdminKey)
	}
	if cfg.ReleaseWatch != "" || cfg.ReleaseStaleAfter != 72*time.Hour {
		t.Errorf("release watch = %q/%s", cfg.ReleaseWatch, cfg.ReleaseStaleAfter)
	}
	t.Setenv("PAKKA_NEWS_ADMIN_BASE", "http://news:1")
	t.Setenv("PAKKA_NEWS_ADMIN_KEY", "k")
	t.Setenv("PAKKA_RELEASE_WATCH", "pakka,news")
	t.Setenv("PAKKA_RELEASE_STALE_AFTER", "24h")
	cfg = Load()
	if cfg.NewsAdminBase != "http://news:1" || cfg.NewsAdminKey != "k" {
		t.Errorf("news admin not applied: %+v", cfg)
	}
	if cfg.ReleaseWatch != "pakka,news" || cfg.ReleaseStaleAfter != 24*time.Hour {
		t.Errorf("release watch not applied: %+v", cfg)
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
