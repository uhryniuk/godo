// Package service defines the on-disk service-file format that godo
// consumes from ~/.godo/services/*.toml. A Spec is a declarative job
// definition — once loaded, the daemon registers it, optionally
// autostarts it, and (Step 8) hooks any cron schedule.
package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	robfigcron "github.com/robfig/cron/v3"

	"github.com/uhryniuk/godo/internal/job"
)

// Spec is one declarative service file. Fields with `toml` tags map
// directly to the TOML keys; the rest are populated by Load.
type Spec struct {
	Path string `toml:"-"` // absolute path the spec was loaded from
	Hash string `toml:"-"` // SHA256 of the file bytes (for diff detection)

	Name       string            `toml:"name"`
	Command    string            `toml:"command"`
	Args       []string          `toml:"args"`
	WorkingDir string            `toml:"working_dir"`
	Env        map[string]string `toml:"env"`
	Autostart  bool              `toml:"autostart"`
	Restart    string            `toml:"restart"`
	Nice       int               `toml:"nice"`
	IOnice     string            `toml:"ionice"`

	Cron CronSpec `toml:"cron"`
}

// CronSpec is the optional [cron] table. Empty Schedule = no cron.
type CronSpec struct {
	Schedule string `toml:"schedule"`
	Overlap  bool   `toml:"overlap"`
}

// Load parses path and returns a validated Spec. The caller-visible name
// (Spec.Name) defaults to the basename of path if the TOML omits it.
func Load(path string) (*Spec, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("absolute path: %w", err)
	}
	body, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", abs, err)
	}
	var s Spec
	if _, err := toml.Decode(string(body), &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", abs, err)
	}
	s.Path = abs
	s.Hash = hashBytes(body)
	if s.Name == "" {
		s.Name = strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	}
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", abs, err)
	}
	return &s, nil
}

// LoadAll globs dir/*.toml, loads every match, and returns the slice
// sorted by Path for deterministic order. Files that fail to load are
// skipped — the returned error wraps every per-file failure so the
// caller can decide whether to fail-fast or continue.
func LoadAll(dir string) ([]*Spec, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.toml"))
	if err != nil {
		return nil, fmt.Errorf("glob: %w", err)
	}
	sort.Strings(matches)

	var specs []*Spec
	var errs []string
	for _, p := range matches {
		s, err := Load(p)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		specs = append(specs, s)
	}
	if len(errs) > 0 {
		return specs, fmt.Errorf("service load errors:\n  %s", strings.Join(errs, "\n  "))
	}
	return specs, nil
}

// Validate enforces the schema rules: command must be non-empty,
// restart (if set) must be a known policy, cron schedule (if set)
// must parse.
func (s *Spec) Validate() error {
	if s.Command == "" {
		return errors.New("command is required")
	}
	if s.Restart != "" {
		switch job.RestartPolicy(s.Restart) {
		case job.RestartNo, job.RestartOnFailure, job.RestartAlways:
			// ok
		default:
			return fmt.Errorf("restart: %q is not one of no, on-failure, always", s.Restart)
		}
	}
	if s.Cron.Schedule != "" {
		parser := robfigcron.NewParser(
			robfigcron.Minute | robfigcron.Hour | robfigcron.Dom | robfigcron.Month | robfigcron.Dow,
		)
		if _, err := parser.Parse(s.Cron.Schedule); err != nil {
			return fmt.Errorf("cron schedule %q: %w", s.Cron.Schedule, err)
		}
	}
	return nil
}

// JobOptions returns the job.Option slice that materializes this Spec
// into a Job. The daemon uses this when autostarting / cron-firing.
func (s *Spec) JobOptions() []job.Option {
	opts := []job.Option{
		job.WithName(s.Name),
		job.WithServiceFile(s.Path),
	}
	if s.WorkingDir != "" {
		opts = append(opts, job.WithWorkingDir(s.WorkingDir))
	}
	if len(s.Env) > 0 {
		opts = append(opts, job.WithEnv(s.Env))
	}
	if s.Nice != 0 {
		opts = append(opts, job.WithNice(s.Nice))
	}
	if s.IOnice != "" {
		opts = append(opts, job.WithIOnice(s.IOnice))
	}
	if s.Restart != "" {
		opts = append(opts, job.WithRestart(job.RestartPolicy(s.Restart)))
	}
	if s.Cron.Schedule != "" {
		opts = append(opts, job.WithCron(s.Cron.Schedule))
	}
	return opts
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
