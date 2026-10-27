package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"pakka/internal/config"
	"pakka/internal/meta"
)

// Intent is a support-question category.
type Intent string

// Known intents.
const (
	IntentSetup      Intent = "setup"
	IntentConnection Intent = "connection"
	IntentBug        Intent = "bug"
)

// Classifier detects the intent of a support message; "" means unknown.
// Transport errors are reported so callers can fall back.
type Classifier interface {
	Classify(text string) (Intent, error)
}

var setupWords = []string{
	"setup", "set up", "install", "update", "patch", "configure", "config",
	"first time", "getting started", "guide", "tutorial", "download",
	"sd card", "sdcard", "cfw", "homebrew", "atmosphere", "tesla",
}

var connectionWords = []string{
	"connect", "connection", "disconnect", "reconnect", "online", "offline",
	"join", "lobby", "lobbies", "matchmaking", "multiplayer", "lag", "ping",
	"timeout", "timed out", "nat", "friend code", "room", "host", "session",
	"players", "kicked", "cant play", "can't play", "error code",
	"2124", "2110", "2618", "2811",
}

var bugWords = []string{
	"bug", "crash", "freeze", "frozen", "froze", "glitch", "broken", "broke",
	"error", "exception", "traceback", "stack trace", "wrong", "regression",
	"exploit", "visual", "audio", "stuck", "black screen",
}

// keywordClassifier scores keyword hits per intent. Top score wins; ties
// and zero scores mean unknown (better silent than a wrong redirect).
type keywordClassifier struct{}

func (keywordClassifier) Classify(text string) (Intent, error) {
	t := " " + strings.ToLower(text) + " "
	count := func(words []string) int {
		n := 0
		for _, w := range words {
			// "error code 2124" is a connection symptom, not a generic bug.
			if w == "error" && strings.Contains(t, "error code") {
				continue
			}
			if strings.Contains(t, w) {
				n++
			}
		}
		return n
	}
	type scored struct {
		intent Intent
		n      int
	}
	all := []scored{
		{IntentSetup, count(setupWords)},
		{IntentConnection, count(connectionWords)},
		{IntentBug, count(bugWords)},
	}
	best, winners := 0, 0
	var intent Intent
	for _, s := range all {
		if s.n > best {
			best, winners, intent = s.n, 1, s.intent
		} else if s.n == best && s.n > 0 {
			winners++
		}
	}
	if best == 0 || winners != 1 {
		return "", nil
	}
	return intent, nil
}

const classifyPrompt = `You route support questions for a console-network support server. Classify the user's message as exactly one of: setup, connection, bug, unknown.
- setup: installing, updating, patching, configuring, first-time setup, guides.
- connection: connecting, lobbies, matchmaking, lag, disconnects, NAT, error codes going online, multiplayer reachability.
- bug: crashes, freezes, glitches, broken behavior, visual/audio faults in working software.
Reply with JSON only, no prose: {"intent":"setup|connection|bug|unknown"}. When unsure, say unknown.`

// ModelClassifier asks the model to classify with the channel scopes in
// context. Unparseable replies mean unknown; transport errors are returned.
type ModelClassifier struct {
	Client *meta.Client
}

// Classify implements Classifier.
func (m ModelClassifier) Classify(text string) (Intent, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	reply, err := m.Client.Chat(ctx, []meta.Message{
		{Role: meta.RoleSystem, Content: classifyPrompt},
		{Role: meta.RoleUser, Content: text},
	}, nil)
	if err != nil {
		return "", err
	}
	clean := strings.TrimSpace(reply.Content)
	clean = strings.TrimPrefix(clean, "```json")
	clean = strings.TrimPrefix(clean, "```")
	clean = strings.TrimSuffix(strings.TrimSpace(clean), "```")
	var parsed struct {
		Intent Intent `json:"intent"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(clean)), &parsed); err != nil {
		return "", nil
	}
	switch parsed.Intent {
	case IntentSetup, IntentConnection, IntentBug:
		return parsed.Intent, nil
	}
	return "", nil
}

// chainClassifier tries the primary classifier, falling back to keywords
// on transport errors only (a confident "unknown" is respected).
type chainClassifier struct {
	primary, fallback Classifier
}

func (c chainClassifier) Classify(text string) (Intent, error) {
	intent, err := c.primary.Classify(text)
	if err != nil {
		return c.fallback.Classify(text)
	}
	return intent, nil
}

// Router redirects out-of-scope support questions to their channel.
type Router struct {
	scopes     map[string]Intent // channel ID -> its scope
	targets    map[Intent]string // intent -> channel ID
	classifier Classifier
	cooldown   time.Duration
	log        *slog.Logger
	mu         sync.Mutex
	last       map[string]time.Time // author/channel -> last redirect
}

// Describe reports the routed channels for startup logging.
func (r *Router) Describe() string {
	if r == nil {
		return "disabled"
	}
	return fmt.Sprintf("help=%s bug=%s conn=%s",
		r.targets[IntentSetup], r.targets[IntentBug], r.targets[IntentConnection])
}

// NewRouter builds the router. The setup and bug channels are required;
// an empty connection channel merges connection questions into the setup
// (help) channel instead of disabling routing.
func NewRouter(classifier Classifier, setupChannel, connectionChannel, bugChannel string, cooldown time.Duration, log *slog.Logger) *Router {
	if setupChannel == "" || bugChannel == "" {
		return nil
	}
	if cooldown <= 0 {
		cooldown = time.Hour
	}
	scopes := map[string]Intent{setupChannel: IntentSetup, bugChannel: IntentBug}
	targets := map[Intent]string{IntentSetup: setupChannel, IntentBug: bugChannel}
	if connectionChannel != "" && connectionChannel != setupChannel {
		scopes[connectionChannel] = IntentConnection
		targets[IntentConnection] = connectionChannel
	} else {
		targets[IntentConnection] = setupChannel
	}
	if log == nil {
		log = slog.Default()
	}
	return &Router{
		scopes:     scopes,
		targets:    targets,
		classifier: classifier,
		cooldown:   cooldown,
		log:        log,
		last:       map[string]time.Time{},
	}
}

// NewRouterFromConfig builds the router from config, or nil when disabled.
// With a model client the model classifies first and keywords cover API
// failures; without one, keywords classify alone.
func NewRouterFromConfig(cfg config.Config, client *meta.Client) *Router {
	var c Classifier = keywordClassifier{}
	if client != nil {
		c = chainClassifier{primary: ModelClassifier{Client: client}, fallback: keywordClassifier{}}
	}
	return NewRouter(c,
		cfg.SupportSetupChannel, cfg.SupportConnectionChannel, cfg.SupportBugChannel, time.Hour, slog.Default())
}

// Route returns a redirect message when in is an out-of-scope support
// question; false means handle the message normally.
func (r *Router) Route(in Incoming) (string, bool) {
	if r == nil {
		return "", false
	}
	scope, ok := r.scopes[in.ChannelID]
	if !ok {
		return "", false
	}
	intent, err := r.classifier.Classify(in.Content)
	if err != nil {
		r.log.Warn("support classify failed", "err", err)
		return "", false
	}
	if intent == "" || intent == scope {
		return "", false
	}
	target, ok := r.targets[intent]
	if !ok || target == in.ChannelID {
		return "", false
	}
	key := in.AuthorID + "/" + in.ChannelID
	r.mu.Lock()
	defer r.mu.Unlock()
	if last, seen := r.last[key]; seen && time.Since(last) < r.cooldown {
		return "", false
	}
	r.last[key] = time.Now()
	r.log.Info("support redirect", "author", in.AuthorID,
		"channel", in.ChannelID, "intent", string(intent), "target", target)
	return fmt.Sprintf("Hey <@%s>, this looks like %s — you'll get faster help in <#%s>.",
		in.AuthorID, describe(intent), target), true
}

func describe(i Intent) string {
	switch i {
	case IntentSetup:
		return "a setup question"
	case IntentConnection:
		return "a connection issue"
	case IntentBug:
		return "a bug report"
	}
	return "another kind of question"
}
