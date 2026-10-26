// Package discord redacts infrastructure details from every chat-bound
// message: IPs, tokens, secrets, local paths, and configured internal
// endpoints never reach Discord on any channel.
package discord

import (
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"

	"pakka/internal/config"
)

var (
	ipv4Re         = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	ipv6Re         = regexp.MustCompile(`\b(?:(?:[0-9a-fA-F]{0,4}:){3,}[0-9a-fA-F:.]+|[0-9a-fA-F:.]*::[0-9a-fA-F:.]*)\b`)
	secretAssignRe = regexp.MustCompile(`(?i)\b((?:api[_-]?key|apikey|token|secret|password|passwd|pwd|bearer|authorization|dsn|connection[_-]?string|private[_-]?key))\s*[:=]\s*['"]?\S+`)
	discordTokenRe = regexp.MustCompile(`\b[MN][A-Za-z0-9_-]{23}\.[A-Za-z0-9_-]{6}\.[A-Za-z0-9_-]{27}\b`)
	knownTokenRe   = regexp.MustCompile(`\b(?:ghp_[A-Za-z0-9]+|github_pat_[A-Za-z0-9_]+|xox[bpras]-[A-Za-z0-9-]+|sk-[A-Za-z0-9_-]{8,}|AIza[A-Za-z0-9_-]{10,})\b`)
	localPathRe    = regexp.MustCompile(`\B/(?:home|root|etc|var|opt|srv)/\S*`)
)

// Redact removes infrastructure details from chat-bound text. Mentions,
// channel references, commands, and public URLs pass through unchanged.
func Redact(s string) string { return RedactWith(s, nil) }

// RedactWith also removes every non-empty extra substring (internal
// hostnames and endpoints from config).
func RedactWith(s string, extras []string) string {
	s = secretAssignRe.ReplaceAllString(s, `$1=[redacted]`)
	s = discordTokenRe.ReplaceAllString(s, `[redacted-token]`)
	s = knownTokenRe.ReplaceAllString(s, `[redacted-token]`)
	s = ipv4Re.ReplaceAllString(s, `[ip]`)
	s = ipv6Re.ReplaceAllString(s, `[ip]`)
	s = localPathRe.ReplaceAllString(s, `[path]`)
	for _, e := range extras {
		if e != "" {
			s = strings.ReplaceAll(s, e, `[internal]`)
		}
	}
	return s
}

// RedactExtras collects configured internal endpoints plus the local
// hostname for RedactWith. Localhost placeholders are skipped.
func RedactExtras(cfg config.Config) []string {
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || v == "localhost" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}
	add(cfg.TCPAddr)
	if host := endpointHost(cfg.TCPAddr); host != "" {
		add(host)
	}
	add(cfg.DNSHost)
	add(cfg.HealthURL)
	if u, err := url.Parse(cfg.HealthURL); err == nil && u.Host != "" {
		add(u.Host)
	}
	if h, err := os.Hostname(); err == nil {
		add(h)
	}
	return out
}

// endpointHost splits "host:port" (or bare host) for redaction.
func endpointHost(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[:i]
	}
	return addr
}

// Redacting wraps s so every message is redacted before send: the choke
// point for monitor, welcomer, and log-watcher announcements.
func Redacting(s Sender, extras ...string) Sender {
	return redactSender{inner: s, extras: extras}
}

type redactSender struct {
	inner  Sender
	extras []string
}

func (r redactSender) ChannelMessageSend(channelID, content string, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	return r.inner.ChannelMessageSend(channelID, RedactWith(content, r.extras), options...)
}
