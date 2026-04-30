package daemon

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/uhryniuk/godo/internal/job"
)

// ErrNotFound is returned when no job matches a target string.
var ErrNotFound = errors.New("registry: no such job")

// ErrAmbiguous is returned when a target string matches more than one job
// (e.g., a hash prefix that collides).
var ErrAmbiguous = errors.New("registry: target matches multiple jobs")

// ErrNameTaken is returned by Add when a job with the same Name already
// exists. Names are unique within a registry.
var ErrNameTaken = errors.New("registry: name already in use")

// Registry is the in-memory map of all jobs the daemon knows about. Safe
// for concurrent use.
type Registry struct {
	mu   sync.RWMutex
	jobs map[string]*job.Job // keyed by Hash
}

func NewRegistry() *Registry {
	return &Registry{jobs: make(map[string]*job.Job)}
}

// Add registers j. Fails if its Name is already taken by a different job.
func (r *Registry) Add(j *job.Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.jobs {
		if existing.Hash != j.Hash && existing.Name == j.Name {
			return fmt.Errorf("%w: %q", ErrNameTaken, j.Name)
		}
	}
	r.jobs[j.Hash] = j
	return nil
}

// Get returns the job with the given hash, or ErrNotFound. The returned
// pointer is shared with the registry — do NOT mutate. Use Update for
// mutations or GetCopy for a safe-to-keep value.
func (r *Registry) Get(hash string) (*job.Job, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if j, ok := r.jobs[hash]; ok {
		return j, nil
	}
	return nil, ErrNotFound
}

// GetCopy returns a value copy of the job with the given hash. The copy
// happens under the registry lock so it is safe against concurrent
// Update calls.
func (r *Registry) GetCopy(hash string) (job.Job, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	j, ok := r.jobs[hash]
	if !ok {
		return job.Job{}, ErrNotFound
	}
	return *j, nil
}

// Update applies fn to the job with the given hash while holding the
// registry's write lock. fn must not call other Registry methods (would
// deadlock).
func (r *Registry) Update(hash string, fn func(*job.Job)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[hash]
	if !ok {
		return ErrNotFound
	}
	fn(j)
	return nil
}

// Remove deletes the job with the given hash. No-op if absent.
func (r *Registry) Remove(hash string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.jobs, hash)
}

// All returns a snapshot slice of every job, ordered for stable display.
func (r *Registry) All() []*job.Job {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*job.Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		out = append(out, j)
	}
	// Sort by StartedAt then Hash for stable ordering.
	sortJobs(out)
	return out
}

// Resolve interprets target as either an exact name or a hash prefix.
// Names are checked first; if no exact-name match, falls back to hash
// prefix lookup. Returns ErrNotFound if nothing matches and ErrAmbiguous
// if multiple jobs share the prefix.
func (r *Registry) Resolve(target string) (*job.Job, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Exact name first.
	for _, j := range r.jobs {
		if j.Name == target {
			return j, nil
		}
	}

	// Hash prefix.
	var matches []*job.Job
	for hash, j := range r.jobs {
		if strings.HasPrefix(hash, target) {
			matches = append(matches, j)
		}
	}
	switch len(matches) {
	case 0:
		return nil, ErrNotFound
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("%w: %q matches %d jobs", ErrAmbiguous, target, len(matches))
	}
}

// Snapshot returns a deep copy of every job, suitable for serialization
// without holding the lock.
func (r *Registry) Snapshot() []job.Job {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]job.Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		out = append(out, *j)
	}
	sortJobsValue(out)
	return out
}

// LoadFrom replaces the registry contents with the given jobs.
func (r *Registry) LoadFrom(jobs []job.Job) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs = make(map[string]*job.Job, len(jobs))
	for i := range jobs {
		j := jobs[i]
		r.jobs[j.Hash] = &j
	}
}

// sortJobs orders newest-first by StartedAt, ties broken by Hash for stability.
func sortJobs(js []*job.Job) {
	sort.Slice(js, func(i, j int) bool {
		if js[i].StartedAt.Equal(js[j].StartedAt) {
			return js[i].Hash < js[j].Hash
		}
		return js[i].StartedAt.After(js[j].StartedAt)
	})
}

func sortJobsValue(js []job.Job) {
	sort.Slice(js, func(i, j int) bool {
		if js[i].StartedAt.Equal(js[j].StartedAt) {
			return js[i].Hash < js[j].Hash
		}
		return js[i].StartedAt.After(js[j].StartedAt)
	})
}
