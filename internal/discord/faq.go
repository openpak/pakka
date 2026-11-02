package discord

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Patcher releases linked from welcome and FAQ answers (verified remotes).
const (
	switchPatcherURL = "https://github.com/openpak/switch-patcher/releases"
	wiiuPatcherURL   = "https://github.com/openpak/wiiu-patcher/releases"
	dsPatcherURL     = "https://github.com/openpak/3ds-patcher/releases"
)

// FAQEntry is one curated answer: the phrases that trigger it, how many
// distinct phrases must hit, and the reply text.
type FAQEntry struct {
	Name    string
	Phrases []string
	MinHits int
	Answer  string
}

// FAQ answers common support questions inline. It only sees messages the
// router left alone (in-scope or unknown intent), answers on a clear
// keyword match, and stays silent otherwise — humans handle the rest.
// Every answer ends by inviting a human follow-up: that is the escalation.
type FAQ struct {
	entries  []FAQEntry
	cooldown time.Duration
	log      *slog.Logger
	mu       sync.Mutex
	last     map[string]time.Time // author/channel -> last answer
}

// NewFAQ builds the answerer with the built-in entries. matchmakingChannel
// links the find-players answer; empty leaves a plain channel name.
func NewFAQ(matchmakingChannel string, cooldown time.Duration, log *slog.Logger) *FAQ {
	if cooldown <= 0 {
		cooldown = time.Hour
	}
	if log == nil {
		log = slog.Default()
	}
	findPlayers := "#matchmaking"
	if matchmakingChannel != "" {
		findPlayers = "<#" + matchmakingChannel + ">"
	}
	entries := []FAQEntry{
		{
			Name:    "patcher-install",
			Phrases: []string{"patcher", "sd card", "sdcard", "install", "set up", "setup", "getting started", "first time", "cfw", "atmosphere"},
			MinHits: 2,
			Answer: fmt.Sprintf("grab your console's patcher from its releases page (Switch: %s · Wii U: %s · 3DS: %s) and follow the instructions there. If a step fails, paste the exact error here.",
				switchPatcherURL, wiiuPatcherURL, dsPatcherURL),
		},
		{
			Name:    "update-homebrew",
			Phrases: []string{"update", "new version", "out of date", "outdated", "eshop"},
			MinHits: 2,
			Answer:  "homebrew updates arrive through the OpenPak eShop on your console — open the eShop and update from there, like a system update.",
		},
		{
			Name:    "error-code-online",
			Phrases: []string{"error code", "2124", "2110", "2618", "2811", "can't connect", "cant connect", "failed to connect", "connection failed"},
			MinHits: 1,
			Answer:  "connection error codes usually mean your console can't reach the network: check your internet connection, restart the console and router, confirm you're set up on the OpenPak network, then post the full code here if it persists.",
		},
		{
			Name:    "find-players",
			Phrases: []string{"looking for", "lfg", "find players", "anyone playing", "matchmaking", "lobby", "play together", "join me"},
			MinHits: 1,
			Answer:  fmt.Sprintf("head to %s to find players — post the game and when you're around.", findPlayers),
		},
		{
			Name:    "supported-consoles",
			Phrases: []string{"ps3", "ps4", "ps5", "xbox", "wii", "3ds", "supported", "which consoles", "when"},
			MinHits: 2,
			Answer:  "the Switch network is fully live; other consoles vary — check the platform channels and #server-status for the current state.",
		},
	}
	return &FAQ{entries: entries, cooldown: cooldown, log: log, last: map[string]time.Time{}}
}

// Answer returns a reply for a support message; false means stay silent.
// At most one answer per author and channel per cooldown.
func (f *FAQ) Answer(in Incoming) (string, bool) {
	if f == nil {
		return "", false
	}
	t := " " + strings.ToLower(in.Content) + " "
	best, winners := 0, 0
	var entry *FAQEntry
	for i := range f.entries {
		n := 0
		for _, p := range f.entries[i].Phrases {
			if strings.Contains(t, p) {
				n++
			}
		}
		want := f.entries[i].MinHits
		if want <= 0 {
			want = 1
		}
		if n < want {
			continue
		}
		if n > best {
			best, winners, entry = n, 1, &f.entries[i]
		} else if n == best {
			winners++
		}
	}
	if entry == nil || winners != 1 {
		return "", false
	}
	key := in.AuthorID + "/" + in.ChannelID
	f.mu.Lock()
	defer f.mu.Unlock()
	if last, seen := f.last[key]; seen && time.Since(last) < f.cooldown {
		return "", false
	}
	f.last[key] = time.Now()
	f.log.Info("faq answer", "author", in.AuthorID, "channel", in.ChannelID, "entry", entry.Name)
	return fmt.Sprintf("Hey <@%s>, %s\n\nStill stuck? Just reply here — a human will pick it up.",
		in.AuthorID, entry.Answer), true
}
