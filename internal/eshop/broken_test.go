package eshop

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"pakka/internal/shopcore"
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

// fakeBackend is an in-memory shop-core stand-in.
type fakeBackend struct {
	set     map[string]shopcore.BrokenEntry
	failing bool
	saves   int
}

func (f *fakeBackend) Load(context.Context) (map[string]shopcore.BrokenEntry, error) {
	if f.failing {
		return nil, errors.New("down")
	}
	out := map[string]shopcore.BrokenEntry{}
	for k, v := range f.set {
		out[k] = v
	}
	return out, nil
}

func (f *fakeBackend) Save(_ context.Context, set map[string]shopcore.BrokenEntry) error {
	if f.failing {
		return errors.New("down")
	}
	f.saves++
	f.set = set
	return nil
}

func TestBrokenBackendSurvivesRestartAndLabelsConsole(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	be := &fakeBackend{}
	staff := &fakeAnnouncer{}
	b := NewConsoleBrokenTracker("wiiu", ConsoleLabel("wiiu", "Wii U"), staff, "s", time.Hour, be, log)
	b.Report(map[string]string{"App": "v1"})
	if e, ok := be.set["App"]; !ok || e.Context != "v1" || e.Since.IsZero() {
		t.Fatalf("not persisted: %+v", be.set)
	}
	// A restarted Pakka keeps the first-seen time, so the grace clock does not reset.
	e := be.set["App"]
	e.Since = time.Now().Add(-2 * time.Hour)
	be.set["App"] = e
	b2 := NewConsoleBrokenTracker("wiiu", ConsoleLabel("wiiu", "Wii U"), staff, "s", time.Hour, be, log)
	b2.Report(map[string]string{"App": "v1"})
	if len(staff.posts) != 1 || !strings.Contains(staff.posts[0], "[Wii U - shop]") {
		t.Fatalf("posts = %v", staff.posts)
	}
	if !be.set["App"].Alerted {
		t.Fatal("alerted flag not persisted")
	}
	sw := &fakeAnnouncer{}
	s := NewConsoleBrokenTracker("switch", ConsoleLabel("switch", ""), sw, "s", time.Hour, nil, log)
	s.Report(map[string]string{"X": "y"})
	s.mu.Lock()
	x := s.state["X"]
	x.Since = time.Now().Add(-2 * time.Hour)
	s.state["X"] = x
	s.mu.Unlock()
	s.Report(map[string]string{"X": "y"})
	if len(sw.posts) != 1 || !strings.HasPrefix(sw.posts[0], "[Switch - eshop] **1 packages failing to build**") {
		t.Fatalf("switch wording changed: %v", sw.posts)
	}
}

func TestBrokenBackendDownAtStartDoesNotWipe(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	since := time.Now().Add(-5 * time.Hour)
	be := &fakeBackend{set: map[string]shopcore.BrokenEntry{"Old": {Since: since, Context: "k"}}, failing: true}
	b := NewConsoleBrokenTracker("switch", "Switch - eshop", &fakeAnnouncer{}, "s", time.Hour, be, log)
	b.Report(map[string]string{"New": "n"}) // still down: memory only, nothing saved
	if be.saves != 0 {
		t.Fatal("saved over an unread backend")
	}
	be.failing = false
	b.Report(map[string]string{"New": "n"}) // back: merges, then saves the union minus cleared
	if _, ok := be.set["New"]; !ok || be.saves != 1 {
		t.Fatalf("after recovery: %+v saves=%d", be.set, be.saves)
	}
	if _, ok := be.set["Old"]; ok {
		t.Fatal("Old should be cleared by the report that lacks it")
	}
}
