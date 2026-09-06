package sync

import (
	"sort"
)

var auditFields = map[string]bool{
	"observed_at": true, "last_verified_at": true, "sync_run_id": true,
}

type CanonicalRow struct {
	NaturalKey string
	Fields     map[string]string
}

func CanonicalProjection(rows []CanonicalRow) map[string]map[string]string {
	out := make(map[string]map[string]string, len(rows))
	for _, r := range rows {
		f := make(map[string]string, len(r.Fields))
		for k, v := range r.Fields {
			if auditFields[k] {
				continue
			}
			f[k] = v
		}
		out[r.NaturalKey] = f
	}
	return out
}

type Diff struct {
	Added   []string
	Removed []string
	Changed []string
}

func DiffProjections(a, b map[string]map[string]string) Diff {
	var d Diff
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			d.Removed = append(d.Removed, k)
			continue
		}
		if !equalFields(av, bv) {
			d.Changed = append(d.Changed, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			d.Added = append(d.Added, k)
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Removed)
	sort.Strings(d.Changed)
	return d
}

func (d Diff) Empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

func equalFields(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
