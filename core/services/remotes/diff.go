package remotes

import "sort"

// modelDiff is what a listing says against what a remote owns.
type modelDiff struct {
	// New are upstream models that are not an entry yet.
	New []UpstreamModel
	// Unavailable are entries whose upstream model is no longer listed.
	Unavailable []string
	// Kept are entries whose upstream model is still listed.
	Kept []string
}

// diffModels compares the models a remote lists with the entries a remote
// owns. It only reports: nothing is deleted or rewritten because of it. The
// result is sorted, so the same input always gives the same output.
func diffModels(upstream []UpstreamModel, owned []ownedEntry) modelDiff {
	listed := make(map[string]UpstreamModel, len(upstream))
	for _, m := range upstream {
		if m.ID == "" {
			continue
		}
		if _, dup := listed[m.ID]; !dup {
			listed[m.ID] = m
		}
	}
	adopted := make(map[string]bool, len(owned))
	d := modelDiff{New: []UpstreamModel{}, Unavailable: []string{}, Kept: []string{}}
	for _, o := range owned {
		adopted[o.UpstreamID] = true
		if _, ok := listed[o.UpstreamID]; ok {
			d.Kept = append(d.Kept, o.Name)
		} else {
			d.Unavailable = append(d.Unavailable, o.Name)
		}
	}
	for id, m := range listed {
		if !adopted[id] {
			d.New = append(d.New, m)
		}
	}
	sort.Slice(d.New, func(i, j int) bool { return d.New[i].ID < d.New[j].ID })
	sort.Strings(d.Unavailable)
	sort.Strings(d.Kept)
	return d
}
