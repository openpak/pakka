package discord

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// MemberLister lists guild members page by page.
type MemberLister interface {
	MembersAfter(after string, limit int) ([]*discordgo.Member, error)
}

// guildLister lists members of one guild via the gateway session.
type guildLister struct {
	session *discordgo.Session
	guildID string
}

// NewGuildLister binds member listing to a guild.
func NewGuildLister(s *discordgo.Session, guildID string) MemberLister {
	return guildLister{session: s, guildID: guildID}
}

// MembersAfter implements MemberLister.
func (l guildLister) MembersAfter(after string, limit int) ([]*discordgo.Member, error) {
	return l.session.GuildMembers(l.guildID, after, limit)
}

// Welcomer tags newcomers in the welcome channel on an interval. Only
// members who joined since the previous pass are greeted; the first pass
// only baselines, so restarts never re-welcome the whole server.
type Welcomer struct {
	lister    MemberLister
	sender    Sender
	channelID string
	interval  time.Duration
	prefix    string
	lastCheck time.Time
	log       *slog.Logger
}

// NewWelcomer builds the welcomer.
func NewWelcomer(lister MemberLister, sender Sender, channelID string, interval time.Duration, prefix string, log *slog.Logger) *Welcomer {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	return &Welcomer{
		lister: lister, sender: sender, channelID: channelID,
		interval: interval, prefix: prefix, lastCheck: time.Now().UTC(), log: log,
	}
}

// Run greets newcomers until ctx ends.
func (w *Welcomer) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.Pass(ctx)
		}
	}
}

// Pass performs a single welcome pass (used by tests).
func (w *Welcomer) Pass(_ context.Context) {
	start := time.Now().UTC()
	var newcomers []*discordgo.Member
	after := ""
	for {
		page, err := w.lister.MembersAfter(after, 1000)
		if err != nil {
			w.log.Error("welcome member list failed", "err", err)
			return
		}
		if len(page) == 0 {
			break
		}
		for _, m := range page {
			if m == nil || m.User == nil || m.User.Bot {
				continue
			}
			if m.JoinedAt.After(w.lastCheck) {
				newcomers = append(newcomers, m)
			}
		}
		if len(page) < 1000 {
			break
		}
		after = page[len(page)-1].User.ID
	}
	w.lastCheck = start
	if len(newcomers) == 0 {
		return
	}
	sort.Slice(newcomers, func(i, j int) bool { return newcomers[i].JoinedAt.Before(newcomers[j].JoinedAt) })
	tags := make([]string, 0, len(newcomers))
	for _, m := range newcomers {
		tags = append(tags, "<@"+m.User.ID+">")
	}
	msg := fmt.Sprintf("Welcome %s to the server! :wave: If your connection misbehaves, try `%s help`.",
		strings.Join(tags, " "), w.prefix)
	if _, err := w.sender.ChannelMessageSend(w.channelID, truncate(msg)); err != nil {
		w.log.Error("welcome post failed", "err", err)
	}
}
