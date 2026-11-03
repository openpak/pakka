package releases

import (
	"context"
	"io"
	"log/slog"
	"pakka/internal/store"
	"strings"
	"testing"
	"time"

	"pakka/internal/eshop"
)

type fakeLister struct {
	rels map[string]eshop.Release
	has  map[string]bool
}

func (f fakeLister) Latest(_ context.Context, slug string) (eshop.Release, bool, error) {
	return f.rels[slug], f.has[slug], nil
}

func rel(tag string, age time.Duration) eshop.Release {
	return eshop.Release{Tag: tag, PublishedAt: time.Now().Add(-age).UTC().Format(time.RFC3339)}
}

type fakeAnnouncer struct {
	posts []string
}

func (f *fakeAnnouncer) Announce(_ string, content string) error {
	f.posts = append(f.posts, content)
	return nil
}

func TestParseRepos(t *testing.T) {
	got := ParseRepos("pakka, openpak/news, ,tobagin/other")
	want := []string{"openpak/pakka", "openpak/news", "tobagin/other"}
	if len(got) != len(want) {
		t.Fatalf("repos = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("repos = %v", got)
		}
	}
}

func TestWatchBaselineSilent(t *testing.T) {
	lister := fakeLister{
		rels: map[string]eshop.Release{"openpak/pakka": rel("v0.8.0", 30*24*time.Hour)},
		has:  map[string]bool{"openpak/pakka": true},
	}
	staff := &fakeAnnouncer{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWatcher(lister, []string{"openpak/pakka"}, 72*time.Hour, staff, "staff1", nil, log)
	w.Check(context.Background())
	if len(staff.posts) != 0 {
		t.Fatalf("baseline nagged: %v", staff.posts)
	}
}

func TestWatchNagsAgedReleaseOnce(t *testing.T) {
	lister := fakeLister{
		rels: map[string]eshop.Release{"openpak/pakka": rel("v0.8.0", time.Hour)},
		has:  map[string]bool{"openpak/pakka": true},
	}
	staff := &fakeAnnouncer{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWatcher(lister, []string{"openpak/pakka"}, 72*time.Hour, staff, "staff1", nil, log)
	ctx := context.Background()
	w.Check(ctx) // baseline: silent
	lister.rels["openpak/pakka"] = rel("v0.9.0", time.Hour)
	w.Check(ctx) // new tag: silent, starts aging
	if len(staff.posts) != 0 {
		t.Fatalf("fresh release nagged: %v", staff.posts)
	}
	lister.rels["openpak/pakka"] = rel("v0.9.0", 96*time.Hour)
	w.Check(ctx) // aged past the window: nags
	if len(staff.posts) != 1 || !strings.Contains(staff.posts[0], "v0.9.0") ||
		!strings.Contains(staff.posts[0], "confirm it's deployed") {
		t.Fatalf("posts = %v", staff.posts)
	}
	w.Check(ctx) // no refire
	if len(staff.posts) != 1 {
		t.Fatalf("refired: %v", staff.posts)
	}
}

func TestWatchPersists(t *testing.T) {
	dir := t.TempDir()
	lister := fakeLister{
		rels: map[string]eshop.Release{"openpak/pakka": rel("v0.8.0", time.Hour)},
		has:  map[string]bool{"openpak/pakka": true},
	}
	staff := &fakeAnnouncer{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWatcher(lister, []string{"openpak/pakka"}, 72*time.Hour, staff, "staff1", store.MustFiles(dir), log)
	ctx := context.Background()
	w.Check(ctx)
	lister.rels["openpak/pakka"] = rel("v0.9.0", 96*time.Hour)
	w.Check(ctx) // new tag recorded, silent
	w2 := NewWatcher(lister, []string{"openpak/pakka"}, 72*time.Hour, staff, "staff1", store.MustFiles(dir), log)
	w2.Check(ctx) // restored state: aged v0.9.0 nags
	if len(staff.posts) != 1 || !strings.Contains(staff.posts[0], "v0.9.0") {
		t.Fatalf("posts = %v", staff.posts)
	}
}
