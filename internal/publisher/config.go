package publisher

import (
	"strconv"
	"strings"
	"time"
)

// Config is the publisher's environment-only configuration.
type Config struct {
	// Kill stops every publish (PAKKA_PUBLISH_KILL=1).
	Kill bool
	// Categories are the categories switched on (PAKKA_PUBLISH_CATEGORIES,
	// comma list). Empty = nothing publishes.
	Categories map[string]bool
	// OpenVerseURL and OpenVerseKey reach the official-account routes
	// (PAKKA_OPENVERSE_URL, PAKKA_OPENVERSE_KEY).
	OpenVerseURL, OpenVerseKey string
	// VerseURL is the public OpenVerse address used in links.
	VerseURL string
	// NewsBase and NewsKey reach the News admin API (PAKKA_NEWS_ADMIN_BASE/_KEY).
	NewsBase, NewsKey string
	// AdminKey guards the /v1/publisher endpoints (PAKKA_PUBLISH_ADMIN_KEY).
	// Empty = the endpoints answer 404.
	AdminKey string
	// StateDir holds the queue, the audit log and the mirror state.
	StateDir string
	// ImageHosts are the hosts an image may come from (our own only).
	ImageHosts []string
	// MirrorInterval paces the News to OpenVerse mirror (0 = off).
	MirrorInterval time.Duration
	// HighlightsEvery paces the scheduled highlights draft (0 = manual only).
	HighlightsEvery time.Duration
	// BossKeys says BOSS keys exist (PAKKA_BOSS_KEYS=1). Until the BOSS
	// sender is built the tool stays blocked either way.
	BossKeys bool
}

// LoadConfig reads the configuration through getenv.
func LoadConfig(getenv func(string) string) Config {
	dur := func(name string, def time.Duration) time.Duration {
		v := strings.TrimSpace(getenv(name))
		if v == "" {
			return def
		}
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return def
		}
		return d
	}
	cats := map[string]bool{}
	for _, c := range strings.Split(getenv("PAKKA_PUBLISH_CATEGORIES"), ",") {
		if c = strings.TrimSpace(c); c != "" {
			cats[c] = true
		}
	}
	hosts := splitList(getenv("PAKKA_PUBLISH_IMAGE_HOSTS"))
	if len(hosts) == 0 {
		hosts = []string{"openpak.org"}
	}
	verse := strings.TrimRight(strings.TrimSpace(getenv("PAKKA_VERSE_URL")), "/")
	if verse == "" {
		verse = "https://verse.openpak.org"
	}
	kill, _ := strconv.ParseBool(strings.TrimSpace(getenv("PAKKA_PUBLISH_KILL")))
	boss, _ := strconv.ParseBool(strings.TrimSpace(getenv("PAKKA_BOSS_KEYS")))
	return Config{
		Kill:            kill,
		Categories:      cats,
		OpenVerseURL:    strings.TrimRight(strings.TrimSpace(getenv("PAKKA_OPENVERSE_URL")), "/"),
		OpenVerseKey:    strings.TrimSpace(getenv("PAKKA_OPENVERSE_KEY")),
		VerseURL:        verse,
		NewsBase:        strings.TrimRight(strings.TrimSpace(getenv("PAKKA_NEWS_ADMIN_BASE")), "/"),
		NewsKey:         strings.TrimSpace(getenv("PAKKA_NEWS_ADMIN_KEY")),
		AdminKey:        strings.TrimSpace(getenv("PAKKA_PUBLISH_ADMIN_KEY")),
		StateDir:        strings.TrimSpace(getenv("PAKKA_STATE_DIR")),
		ImageHosts:      hosts,
		MirrorInterval:  dur("PAKKA_PUBLISH_MIRROR_INTERVAL", 10*time.Minute),
		HighlightsEvery: dur("PAKKA_PUBLISH_HIGHLIGHTS_EVERY", 0),
		BossKeys:        boss,
	}
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Enabled reports whether publishing is configured at all.
func (c Config) Enabled() bool { return c.OpenVerseURL != "" && c.OpenVerseKey != "" }
