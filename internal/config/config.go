// Package config loads harness configuration from the environment. Every
// value has a safe local default. Live-check targets come only from here
// (approved destinations); request bodies must never supply targets.
package config

import (
	"os"
	"strconv"
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
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Load reads configuration from the environment.
func Load() Config {
	return Config{
		Addr:         getenv("PAKKA_ADDR", "127.0.0.1:24800"),
		DNSHost:      getenv("PAKKA_DNS_HOST", "localhost"),
		TCPAddr:      os.Getenv("PAKKA_TCP_ADDR"),
		HealthURL:    os.Getenv("PAKKA_HEALTH_URL"),
		CheckTimeout: getenvDuration("PAKKA_CHECK_TIMEOUT", 5*time.Second),
		MaxRuns:      getenvInt("PAKKA_MAX_RUNS", 4),
	}
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
