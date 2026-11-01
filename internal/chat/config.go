// Package chat is Pakka's player-facing conversation service: an
// authenticated, per-account chat API shared by the website and the app.
//
// Trust model: only the OpenPak website calls this API. It authenticates a
// person (cookie session or app bearer) itself, then calls here with the
// shared service key and the account id it resolved. The key is the only
// credential this package accepts; nothing a browser or the model supplies
// is ever read as an identity. Tools act for the account bound to the
// conversation and take no account argument.
package chat

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the chat service configuration, read from the environment.
type Config struct {
	// Key is the shared service key the website presents. Empty disables chat.
	Key string
	// WebsiteURL is the website's private base URL, for account-scoped tools.
	WebsiteURL string
	// DocsIndexURL is the MkDocs search index the docs tool reads.
	DocsIndexURL string
	// DocsBaseURL prefixes document locations in links.
	DocsBaseURL string
	// StateDir holds conversations; empty keeps them in memory only.
	StateDir string
	// Retention is how long a conversation is kept after its last message.
	Retention time.Duration
	// MaxConversations caps stored conversations per account.
	MaxConversations int
	// BurstPerMinute caps messages per account per minute.
	BurstPerMinute int
	// DailyCap caps messages per account per UTC day.
	DailyCap int
	// DiagnosesPerHour caps connection diagnoses per account per hour.
	DiagnosesPerHour int
	// MaxMessageChars bounds one user message.
	MaxMessageChars int
	// MaxConcurrent bounds replies being generated at once.
	MaxConcurrent int
}

// LoadConfig reads PAKKA_CHAT_* variables. stateDir is the harness state dir.
func LoadConfig(stateDir string) Config {
	return Config{
		Key:              strings.TrimSpace(os.Getenv("PAKKA_CHAT_KEY")),
		WebsiteURL:       envOr("PAKKA_CHAT_WEBSITE_URL", "http://openpak-website:20010"),
		DocsIndexURL:     envOr("PAKKA_CHAT_DOCS_INDEX", "https://docs.openpak.org/search/search_index.json"),
		DocsBaseURL:      envOr("PAKKA_CHAT_DOCS_BASE", "https://docs.openpak.org/"),
		StateDir:         stateDir,
		Retention:        envDur("PAKKA_CHAT_RETENTION", 30*24*time.Hour),
		MaxConversations: envInt("PAKKA_CHAT_MAX_CONVERSATIONS", 30),
		BurstPerMinute:   envInt("PAKKA_CHAT_BURST", 6),
		DailyCap:         envInt("PAKKA_CHAT_DAILY_CAP", 100),
		DiagnosesPerHour: envInt("PAKKA_CHAT_DIAGNOSES_PER_HOUR", 3),
		MaxMessageChars:  envInt("PAKKA_CHAT_MAX_CHARS", 2000),
		MaxConcurrent:    envInt("PAKKA_CHAT_MAX_CONCURRENT", 6),
	}
}

func (c Config) withDefaults() Config {
	d := LoadConfig(c.StateDir)
	if c.Retention <= 0 {
		c.Retention = d.Retention
	}
	if c.MaxConversations <= 0 {
		c.MaxConversations = d.MaxConversations
	}
	if c.BurstPerMinute <= 0 {
		c.BurstPerMinute = d.BurstPerMinute
	}
	if c.DailyCap <= 0 {
		c.DailyCap = d.DailyCap
	}
	if c.DiagnosesPerHour <= 0 {
		c.DiagnosesPerHour = d.DiagnosesPerHour
	}
	if c.MaxMessageChars <= 0 {
		c.MaxMessageChars = d.MaxMessageChars
	}
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = d.MaxConcurrent
	}
	return c
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(k))); err == nil && n > 0 {
		return n
	}
	return def
}

func envDur(k string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(os.Getenv(k))); err == nil && d > 0 {
		return d
	}
	return def
}
