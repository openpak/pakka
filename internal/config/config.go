// Package config loads harness configuration from the environment. Every
// value has a safe local default. Live-check targets come only from here
// (approved destinations); request bodies must never supply targets.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the static harness configuration.
type Config struct {
	// Addr is the HTTP listen address. Default loopback, port >= 20000.
	Addr string
	// DNSHost is the host the dns.resolve check looks up.
	DNSHost string
	// TCPAddr is the address the tcp.connect check dials. Empty means the
	// check reports skipped until a target is configured.
	TCPAddr string
	// HealthURL is the URL the http.health check fetches. Empty means the
	// check reports skipped until a target is configured.
	HealthURL string
	// CheckTimeout bounds every check (short-lived request context).
	CheckTimeout time.Duration
	// MaxRuns bounds concurrent diagnostic runs.
	MaxRuns int
	// MetaAPIKey is the Meta Model API credential, server-side only.
	MetaAPIKey string
	// MetaAPIBase overrides the Meta Model API base URL.
	MetaAPIBase string
	// MetaModel selects the investigation model.
	MetaModel string
	// InvestigateSteps bounds model turns per investigation.
	InvestigateSteps int
	// DiscordToken is the bot token, server-side only.
	DiscordToken string
	// DiscordGuilds allowlists guild IDs (empty = all guilds).
	DiscordGuilds []string
	// DiscordPrefix is the command prefix.
	DiscordPrefix string
	// DiscordChannels gates command responses to these channel IDs (empty = all).
	DiscordChannels []string
	// DiscordOpsChannels gates reserved commands (empty = no restriction).
	DiscordOpsChannels []string
	// DiscordStatusChannel receives automatic outage alerts (empty = disabled).
	DiscordStatusChannel string
	// DiscordWelcomeChannel receives newcomer welcomes (empty = disabled).
	DiscordWelcomeChannel string
	// DiscordWelcomeInterval paces welcome passes.
	DiscordWelcomeInterval time.Duration
	// StatusTargetsRaw lists monitor targets as name=url pairs.
	StatusTargetsRaw string
	// StatusInterval paces monitor polls.
	StatusInterval time.Duration
	// SupportSetupChannel scopes setup questions (empty = routing disabled).
	SupportSetupChannel string
	// SupportConnectionChannel scopes connection questions.
	SupportConnectionChannel string
	// SupportBugChannel scopes bug reports.
	SupportBugChannel string
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// getenvFirst returns the first set variable, else def.
func getenvFirst(def string, keys ...string) string {
	for _, key := range keys {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return def
}

// Load reads configuration from the environment, after loading ./.env when
// present (plain environment variables take precedence).
func Load() Config {
	loadDotEnv(".")
	return Config{
		Addr:                     getenv("PAKKA_ADDR", "127.0.0.1:24800"),
		DNSHost:                  getenv("PAKKA_DNS_HOST", "localhost"),
		TCPAddr:                  os.Getenv("PAKKA_TCP_ADDR"),
		HealthURL:                os.Getenv("PAKKA_HEALTH_URL"),
		CheckTimeout:             getenvDuration("PAKKA_CHECK_TIMEOUT", 5*time.Second),
		MaxRuns:                  getenvInt("PAKKA_MAX_RUNS", 4),
		MetaAPIKey:               getenvFirst("", "PAKKA_META_API_KEY", "META_API_KEY"),
		MetaAPIBase:              getenvFirst("https://api.meta.ai/v1", "PAKKA_META_API_BASE", "META_API_BASE"),
		MetaModel:                getenv("PAKKA_META_MODEL", "muse-spark-1.3-contributor"),
		InvestigateSteps:         getenvInt("PAKKA_INVESTIGATE_STEPS", 5),
		DiscordToken:             os.Getenv("PAKKA_DISCORD_TOKEN"),
		DiscordGuilds:            splitCSV(os.Getenv("PAKKA_DISCORD_GUILDS")),
		DiscordPrefix:            getenv("PAKKA_DISCORD_PREFIX", "!pakka"),
		DiscordChannels:          splitCSV(os.Getenv("PAKKA_DISCORD_CHANNELS")),
		DiscordOpsChannels:       splitCSV(os.Getenv("PAKKA_DISCORD_OPS_CHANNELS")),
		DiscordStatusChannel:     os.Getenv("PAKKA_DISCORD_STATUS_CHANNEL"),
		DiscordWelcomeChannel:    os.Getenv("PAKKA_DISCORD_WELCOME_CHANNEL"),
		DiscordWelcomeInterval:   getenvDuration("PAKKA_DISCORD_WELCOME_INTERVAL", 15*time.Minute),
		StatusTargetsRaw:         os.Getenv("PAKKA_STATUS_TARGETS"),
		StatusInterval:           getenvDuration("PAKKA_STATUS_INTERVAL", time.Minute),
		SupportSetupChannel:      os.Getenv("PAKKA_DISCORD_SETUP_CHANNEL"),
		SupportConnectionChannel: os.Getenv("PAKKA_DISCORD_CONNECTION_CHANNEL"),
		SupportBugChannel:        os.Getenv("PAKKA_DISCORD_BUG_CHANNEL"),
	}
}

func splitCSV(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func getenvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
