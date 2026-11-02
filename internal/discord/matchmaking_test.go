package discord

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

type fakeStore struct {
	pages   [][]*discordgo.Message
	deleted []string
	err     error
}

func (f *fakeStore) ChannelMessages(_ string, _ int, beforeID, _, _ string, _ ...discordgo.RequestOption) ([]*discordgo.Message, error) {
	if f.err != nil {
		return nil, f.err
	}
	if beforeID == "" {
		return f.pages[0], nil
	}
	for i, page := range f.pages {
		if len(page) > 0 && page[len(page)-1].ID == beforeID && i+1 < len(f.pages) {
			return f.pages[i+1], nil
		}
	}
	return nil, nil
}

func (f *fakeStore) ChannelMessageDelete(_ string, id string, _ ...discordgo.RequestOption) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func lfMsg(id string, age time.Duration, pinned bool) *discordgo.Message {
	return &discordgo.Message{ID: id, Timestamp: time.Now().Add(-age), Pinned: pinned}
}

func TestSweepExpiresOldKeepsRest(t *testing.T) {
	store := &fakeStore{pages: [][]*discordgo.Message{{
		lfMsg("old", 48*time.Hour, false),
		lfMsg("fresh", time.Hour, false),
		lfMsg("pin", 72*time.Hour, true),
	}}}
	s := NewSweeper(store, "match1", 24*time.Hour, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	deleted, kept := s.Sweep(context.Background())
	if deleted != 1 || kept != 2 {
		t.Fatalf("deleted=%d kept=%d, want 1/2", deleted, kept)
	}
	if len(store.deleted) != 1 || store.deleted[0] != "old" {
		t.Fatalf("deleted ids = %v", store.deleted)
	}
}

func TestSweepListErrorKeepsAll(t *testing.T) {
	store := &fakeStore{err: errors.New("boom")}
	s := NewSweeper(store, "match1", 24*time.Hour, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if deleted, kept := s.Sweep(context.Background()); deleted != 0 || kept != 0 {
		t.Fatalf("deleted=%d kept=%d, want 0/0", deleted, kept)
	}
}
