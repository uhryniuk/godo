package service

// Diff is the change-set between two snapshots of the services
// directory: which files appeared, which disappeared, and which had
// their content change since last load. Keyed by absolute Path —
// the file is the unit of identity, not the Name field.
type Diff struct {
	Added    []*Spec
	Removed  []string // absolute paths
	Modified []*Spec
}

// IsEmpty reports whether nothing changed.
func (d Diff) IsEmpty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Modified) == 0
}

// DiffSpecs compares old and new (both keyed by Path) and returns the
// add/remove/modified set. A spec whose Hash changed is reported under
// Modified with its NEW value; the old hash is what was implicit in old.
func DiffSpecs(old, new []*Spec) Diff {
	oldByPath := make(map[string]*Spec, len(old))
	for _, s := range old {
		oldByPath[s.Path] = s
	}
	newByPath := make(map[string]*Spec, len(new))
	for _, s := range new {
		newByPath[s.Path] = s
	}

	var d Diff
	for path, ns := range newByPath {
		os, ok := oldByPath[path]
		if !ok {
			d.Added = append(d.Added, ns)
			continue
		}
		if os.Hash != ns.Hash {
			d.Modified = append(d.Modified, ns)
		}
	}
	for path := range oldByPath {
		if _, ok := newByPath[path]; !ok {
			d.Removed = append(d.Removed, path)
		}
	}
	return d
}
