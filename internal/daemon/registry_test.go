package daemon

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uhryniuk/godo/internal/job"
)

func newTestJob(t *testing.T, name, command string) *job.Job {
	t.Helper()
	return job.New(command, nil, job.WithName(name))
}

func TestRegistryAddGet(t *testing.T) {
	r := NewRegistry()
	j := newTestJob(t, "alpha", "echo")
	if err := r.Add(j); err != nil {
		t.Fatalf("add: %v", err)
	}
	got, err := r.Get(j.Hash)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != j {
		t.Errorf("got pointer %p, want %p", got, j)
	}

	if _, err := r.Get("does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestRegistryRejectsDuplicateName(t *testing.T) {
	r := NewRegistry()
	if err := r.Add(newTestJob(t, "shared", "a")); err != nil {
		t.Fatalf("first add: %v", err)
	}
	err := r.Add(newTestJob(t, "shared", "b"))
	if !errors.Is(err, ErrNameTaken) {
		t.Errorf("expected ErrNameTaken, got %v", err)
	}
}

func TestRegistryResolveByName(t *testing.T) {
	r := NewRegistry()
	j := newTestJob(t, "web", "node")
	_ = r.Add(j)
	got, err := r.Resolve("web")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != j {
		t.Errorf("got %v, want %v", got, j)
	}
}

func TestRegistryResolveByHashPrefix(t *testing.T) {
	r := NewRegistry()
	j := newTestJob(t, "alpha", "x")
	_ = r.Add(j)
	prefix := j.Hash[:6]
	got, err := r.Resolve(prefix)
	if err != nil {
		t.Fatalf("resolve %q: %v", prefix, err)
	}
	if got != j {
		t.Errorf("got %v, want %v", got, j)
	}
}

func TestRegistryResolveAmbiguous(t *testing.T) {
	r := NewRegistry()
	// Construct two jobs with the same hash prefix by hand.
	a := &job.Job{Hash: "abcdef1111", Name: "a", State: job.Pending}
	b := &job.Job{Hash: "abcdef2222", Name: "b", State: job.Pending}
	_ = r.Add(a)
	_ = r.Add(b)
	_, err := r.Resolve("abcdef")
	if !errors.Is(err, ErrAmbiguous) {
		t.Errorf("expected ErrAmbiguous, got %v", err)
	}
}

func TestRegistryResolveNotFound(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Resolve("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestRegistryAllSortedNewestFirst(t *testing.T) {
	r := NewRegistry()
	older := &job.Job{Hash: "h1", Name: "older", StartedAt: time.Unix(100, 0)}
	newer := &job.Job{Hash: "h2", Name: "newer", StartedAt: time.Unix(200, 0)}
	_ = r.Add(older)
	_ = r.Add(newer)
	all := r.All()
	if len(all) != 2 {
		t.Fatalf("len: %d", len(all))
	}
	if all[0] != newer {
		t.Errorf("expected newer first, got %s", all[0].Name)
	}
}

func TestRegistryConcurrentSafety(t *testing.T) {
	// Hammer the registry from many goroutines with -race.
	r := NewRegistry()
	const N = 200
	var wg sync.WaitGroup
	var added int32

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			j := job.New("cmd", []string{"-n", string(rune('a' + i%26))})
			j.Name = "" // disable name uniqueness for this load test
			if err := r.Add(j); err == nil {
				atomic.AddInt32(&added, 1)
			}
		}(i)
	}

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.All()
		}()
	}

	wg.Wait()
	if got := r.All(); len(got) != int(atomic.LoadInt32(&added)) {
		t.Errorf("registry size %d, expected %d", len(got), added)
	}
}
