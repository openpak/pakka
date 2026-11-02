package discord

import (
	"context"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"
)

// MessageStore reads and deletes channel messages over REST.
type MessageStore interface {
	ChannelMessages(channelID string, limit int, beforeID, afterID, aroundID string, options ...discordgo.RequestOption) ([]*discordgo.Message, error)
	ChannelMessageDelete(channelID, messageID string, options ...discordgo.RequestOption) error
}

var _ MessageStore = (*discordgo.Session)(nil)

// sweepPageSize caps one channel-history page; sweepMaxPages bounds a pass
// so a flood never turns one sweep into a full-channel purge loop.
const (
	sweepPageSize = 100
	sweepMaxPages = 5
)

// Sweeper expires stale matchmaking posts. The single matchmaking channel
// only works if old looking-for-game posts disappear; anything older than
// maxAge goes, except pins (staff notices, rules).
type Sweeper struct {
	store     MessageStore
	channelID string
	maxAge    time.Duration
	interval  time.Duration
	log       *slog.Logger
}

// NewSweeper builds the sweeper.
func NewSweeper(store MessageStore, channelID string, maxAge, interval time.Duration, log *slog.Logger) *Sweeper {
	if maxAge <= 0 {
		maxAge = 24 * time.Hour
	}
	if interval <= 0 {
		interval = time.Hour
	}
	if log == nil {
		log = slog.Default()
	}
	return &Sweeper{store: store, channelID: channelID, maxAge: maxAge, interval: interval, log: log}
}

// Run sweeps until ctx ends.
func (s *Sweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			deleted, kept := s.Sweep(ctx)
			s.log.Info("matchmaking sweep", "deleted", deleted, "kept", kept)
		}
	}
}

// Sweep deletes one pass of expired posts, returning counts.
func (s *Sweeper) Sweep(_ context.Context) (deleted, kept int) {
	cutoff := time.Now().Add(-s.maxAge)
	before := ""
	for range sweepMaxPages {
		page, err := s.store.ChannelMessages(s.channelID, sweepPageSize, before, "", "")
		if err != nil {
			s.log.Error("matchmaking list failed", "err", err)
			return deleted, kept
		}
		if len(page) == 0 {
			return deleted, kept
		}
		for _, m := range page {
			if m == nil {
				continue
			}
			if m.Pinned || m.Timestamp.After(cutoff) {
				kept++
				continue
			}
			if err := s.store.ChannelMessageDelete(s.channelID, m.ID); err != nil {
				s.log.Warn("matchmaking delete failed", "id", m.ID, "err", err)
				kept++
				continue
			}
			deleted++
		}
		if len(page) < sweepPageSize {
			return deleted, kept
		}
		before = page[len(page)-1].ID
	}
	return deleted, kept
}
