package eshop

import (
	"io"
	"log/slog"
	"pakka/internal/store"
	"strings"
	"testing"
	"time"
)

func testTracker(after time.Duration, stateDir string) (*BrokenTracker, *fakeAnnouncer) {
	staff := &fakeAnnouncer{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewBrokenTracker(staff, "staff1", after, store.MustFiles(stateDir), log), staff
}

func backdate(t *testing.T, b *BrokenTracker, pkg string, ago time.Duration) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.state[pkg]
	if e.Since.IsZero() {
		t.Fatalf("no state for %s", pkg)
	}
	e.Since = time.Now().Add(-ago)
	b.state[pkg] = e
}

func TestBrokenSilentWhenFresh(t *testing.T) {
	b, staff := testTracker(time.Hour, "")
	b.Report(map[string]string{"ChunkIntercept": "27/10/2026"})
	b.Report(map[string]string{"ChunkIntercept": "27/10/2026"})
	if len(staff.posts) != 0 {
		t.Fatalf("fresh breakage alerted: %v", staff.posts)
	}
}

func TestBrokenAlertsOncePastGrace(t *testing.T) {
	b, staff := testTracker(time.Hour, "")
	b.Report(map[string]string{"ChunkIntercept": "27/10/2026"})
	backdate(t, b, "ChunkIntercept", 2*time.Hour)
	b.Report(map[string]string{"ChunkIntercept": "27/10/2026"})
	if len(staff.posts) != 1 || !strings.Contains(staff.posts[0], "ChunkIntercept") ||
		!strings.Contains(staff.posts[0], "failing to build") {
		t.Fatalf("breach posts = %v", staff.posts)
	}
	b.Report(map[string]string{"ChunkIntercept": "27/10/2026"})
	if len(staff.posts) != 1 {
		t.Fatalf("breach refired: %v", staff.posts)
	}
}

func TestBrokenBreachesBatch(t *testing.T) {
	b, staff := testTracker(time.Hour, "")
	b.Report(map[string]string{"Beta": "x", "Alpha": "y"})
	backdate(t, b, "Beta", 2*time.Hour)
	backdate(t, b, "Alpha", 3*time.Hour)
	b.Report(map[string]string{"Beta": "x", "Alpha": "y"})
	if len(staff.posts) != 1 || !strings.Contains(staff.posts[0], "2 packages failing") ||
		!strings.Contains(staff.posts[0], "Alpha") || !strings.Contains(staff.posts[0], "Beta") {
		t.Fatalf("batch posts = %v", staff.posts)
	}
}

func TestBrokenClearNotesAlertedOnly(t *testing.T) {
	b, staff := testTracker(time.Hour, "")
	b.Report(map[string]string{"Old": "x", "New": "y"})
	backdate(t, b, "Old", 2*time.Hour)
	b.Report(map[string]string{"Old": "x", "New": "y"}) // Old breaches
	b.Report(map[string]string{})                       // both clear
	if len(staff.posts) != 2 || !strings.Contains(staff.posts[1], "building again") ||
		!strings.Contains(staff.posts[1], "Old") || strings.Contains(staff.posts[1], "New") {
		t.Fatalf("clear posts = %v", staff.posts)
	}
}

func TestBrokenPersistsAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	b, _ := testTracker(time.Hour, dir)
	b.Report(map[string]string{"ChunkIntercept": "27/10/2026"})
	backdate(t, b, "ChunkIntercept", 2*time.Hour)
	b.Report(map[string]string{"ChunkIntercept": "27/10/2026"}) // breach + alerted persisted
	b2, staff2 := testTracker(time.Hour, dir)
	b2.Report(map[string]string{"ChunkIntercept": "27/10/2026"})
	if len(staff2.posts) != 0 {
		t.Fatalf("restored tracker refired: %v", staff2.posts)
	}
	if len(b2.state) != 1 {
		t.Fatalf("restored state = %v", b2.state)
	}
}
