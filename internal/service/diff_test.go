package service

import (
	"sort"
	"testing"
)

func sortedPaths(specs []*Spec) []string {
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = s.Path
	}
	sort.Strings(out)
	return out
}

func TestDiffEmptyVsEmpty(t *testing.T) {
	d := DiffSpecs(nil, nil)
	if !d.IsEmpty() {
		t.Errorf("expected empty diff, got %+v", d)
	}
}

func TestDiffOnlyAdded(t *testing.T) {
	new := []*Spec{
		{Path: "/a.toml", Hash: "h1"},
		{Path: "/b.toml", Hash: "h2"},
	}
	d := DiffSpecs(nil, new)
	if got := sortedPaths(d.Added); len(got) != 2 || got[0] != "/a.toml" || got[1] != "/b.toml" {
		t.Errorf("Added: %v", got)
	}
	if len(d.Removed) != 0 || len(d.Modified) != 0 {
		t.Errorf("expected only Added: %+v", d)
	}
}

func TestDiffOnlyRemoved(t *testing.T) {
	old := []*Spec{
		{Path: "/a.toml", Hash: "h1"},
		{Path: "/b.toml", Hash: "h2"},
	}
	d := DiffSpecs(old, nil)
	sort.Strings(d.Removed)
	if len(d.Removed) != 2 || d.Removed[0] != "/a.toml" || d.Removed[1] != "/b.toml" {
		t.Errorf("Removed: %v", d.Removed)
	}
	if len(d.Added) != 0 || len(d.Modified) != 0 {
		t.Errorf("expected only Removed: %+v", d)
	}
}

func TestDiffModifiedDetectedByHash(t *testing.T) {
	old := []*Spec{{Path: "/a.toml", Hash: "h1"}}
	new := []*Spec{{Path: "/a.toml", Hash: "h2"}}
	d := DiffSpecs(old, new)
	if len(d.Modified) != 1 || d.Modified[0].Path != "/a.toml" {
		t.Errorf("Modified: %+v", d.Modified)
	}
	if d.Modified[0].Hash != "h2" {
		t.Errorf("Modified should carry NEW hash, got %q", d.Modified[0].Hash)
	}
	if len(d.Added) != 0 || len(d.Removed) != 0 {
		t.Errorf("expected only Modified: %+v", d)
	}
}

func TestDiffSameHashIsEmpty(t *testing.T) {
	specs := []*Spec{{Path: "/a.toml", Hash: "h1"}}
	d := DiffSpecs(specs, specs)
	if !d.IsEmpty() {
		t.Errorf("identical snapshots should diff empty: %+v", d)
	}
}

func TestDiffMixedAddRemoveModify(t *testing.T) {
	old := []*Spec{
		{Path: "/a.toml", Hash: "h1"}, // unchanged
		{Path: "/b.toml", Hash: "h2"}, // modified
		{Path: "/c.toml", Hash: "h3"}, // removed
	}
	new := []*Spec{
		{Path: "/a.toml", Hash: "h1"},
		{Path: "/b.toml", Hash: "h2-new"},
		{Path: "/d.toml", Hash: "h4"}, // added
	}
	d := DiffSpecs(old, new)
	if len(d.Added) != 1 || d.Added[0].Path != "/d.toml" {
		t.Errorf("Added: %v", d.Added)
	}
	if len(d.Removed) != 1 || d.Removed[0] != "/c.toml" {
		t.Errorf("Removed: %v", d.Removed)
	}
	if len(d.Modified) != 1 || d.Modified[0].Path != "/b.toml" {
		t.Errorf("Modified: %v", d.Modified)
	}
}
