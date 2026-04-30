package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/uhryniuk/godo/internal/job"
)

// snapshotFile is the basename used for the registry snapshot inside the
// state directory.
const snapshotFile = "registry.json"

// SaveRegistry writes a snapshot of r to dir/registry.json atomically
// (write-temp-then-rename).
func SaveRegistry(dir string, r *Registry) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	jobs := r.Snapshot()
	body, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	final := filepath.Join(dir, snapshotFile)
	tmp, err := os.CreateTemp(dir, snapshotFile+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, final); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// LoadRegistry reads dir/registry.json into r. A missing file returns nil
// (empty registry). A corrupt file is logged and treated as empty so the
// daemon never refuses to start because of bad on-disk state.
func LoadRegistry(dir string, r *Registry) error {
	path := filepath.Join(dir, snapshotFile)
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read snapshot: %w", err)
	}
	var jobs []job.Job
	if err := json.Unmarshal(body, &jobs); err != nil {
		slog.Warn("registry snapshot is corrupt; starting empty",
			"path", path, "err", err)
		return nil
	}
	r.LoadFrom(jobs)
	return nil
}
