package daemon

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/uhryniuk/godo/internal/job"
)

func TestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry()

	// Two jobs with non-trivial fields to verify nothing gets dropped.
	a := &job.Job{
		Hash:      "aaa",
		Name:      "alpha",
		Command:   "echo",
		Args:      []string{"hi"},
		PID:       1234,
		State:     job.Running,
		StartedAt: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
		ExitCode:  0,
		Restart:   job.RestartOnFailure,
		LogDir:    "/tmp/aaa",
	}
	b := &job.Job{
		Hash:     "bbb",
		Name:     "beta",
		State:    job.Completed,
		ExitCode: 0,
		ExitedAt: time.Date(2026, 4, 30, 13, 0, 0, 0, time.UTC),
	}
	_ = r.Add(a)
	_ = r.Add(b)

	if err := SaveRegistry(dir, r); err != nil {
		t.Fatalf("save: %v", err)
	}

	r2 := NewRegistry()
	if err := LoadRegistry(dir, r2); err != nil {
		t.Fatalf("load: %v", err)
	}

	want := r.Snapshot()
	got := r2.Snapshot()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roundtrip mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestLoadMissingFileReturnsEmpty(t *testing.T) {
	r := NewRegistry()
	if err := LoadRegistry(t.TempDir(), r); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := r.All(); len(got) != 0 {
		t.Errorf("expected empty registry, got %d jobs", len(got))
	}
}

func TestLoadCorruptFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, snapshotFile), []byte("not json{{"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	r := NewRegistry()
	if err := LoadRegistry(dir, r); err != nil {
		t.Fatalf("load returned error on corrupt file: %v", err)
	}
	if got := r.All(); len(got) != 0 {
		t.Errorf("expected empty registry after corrupt load, got %d", len(got))
	}
}

func TestSaveIsAtomic(t *testing.T) {
	// Verify no temp files leak in the steady state and no partial registry.json
	// is observable. We can't truly test interrupt, but we can check that after
	// Save returns, only registry.json exists (no .tmp leftovers).
	dir := t.TempDir()
	r := NewRegistry()
	_ = r.Add(&job.Job{Hash: "x", Name: "x", State: job.Pending})
	if err := SaveRegistry(dir, r); err != nil {
		t.Fatalf("save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != snapshotFile {
		t.Errorf("expected only %s in dir, got %v", snapshotFile, entryNames(entries))
	}
}

func entryNames(es []os.DirEntry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Name()
	}
	return out
}
