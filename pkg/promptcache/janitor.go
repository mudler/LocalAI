// Package promptcache keeps the disk tier of the backend prompt cache in
// bounds. It deletes files past their time to live, over a per-namespace
// quota, over a global cap or under the free-space watermarks of the disk. It
// also purges files on request (all, one model, one namespace) and reports
// what the cache holds. Every file operation goes through a Store; DiskStore
// is the node-local one. The package holds no HTTP, auth or database code.
//
// Layout (written by the backend, see the llama.cpp backend):
//
//	<root>/<backend>/<pin16>-<model16>/<compat16>-<ns16>-<hash32>.cache
//	<root>/<backend>/<pin16>-<model16>/<compat16>-<ns16>-<hash32>.cache.tmp
//	<root>/<backend>/<pin16>-<model16>/{manifest.json,.speed,.lock}
//
// The mtime of a file is its last use: the backend touches a file when it
// loads it. Only names of this layout are touched; the metadata files go only
// with a whole-model or whole-cache purge.
package promptcache

import (
	"context"
	"errors"
	"sort"
	"time"
)

// StaleTmpAge is the age after which a partial write (.cache.tmp) is taken
// for the leftover of a crash and deleted.
const StaleTmpAge = time.Hour

// SharedNSID is the namespace id of the shared namespace. The per-namespace
// quota does not apply to it: it belongs to no user.
const SharedNSID = "0000000000000000"

// ErrBadID means a model or namespace id is not 16 lowercase hex characters.
var ErrBadID = errors.New("promptcache: id must be 16 lowercase hex characters")

// Policy is what a sweep enforces. A zero value field turns its rule off.
type Policy struct {
	// TTL is the time to live of an entry since its last use.
	TTL time.Duration
	// ModelTTL overrides TTL for the model with this model16 id.
	ModelTTL map[string]time.Duration
	// QuotaBytes is the most one namespace may keep, over all models. The
	// least recently used files of the namespace go first.
	QuotaBytes int64
	// MaxBytes is the most the whole store may keep. The least recently
	// used files go first, whatever their model or namespace.
	MaxBytes int64
	// LowFreeBytes and CriticalFreeBytes replace the free-space watermarks
	// of the Janitor (see Watermarks); 0 keeps the default rule. The
	// critical watermark never exceeds the low one.
	LowFreeBytes      uint64
	CriticalFreeBytes uint64
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

// Result counts what a sweep or a purge deleted.
type Result struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

func (r *Result) remove(ctx context.Context, s Store, e Entry) {
	if s.Delete(ctx, e.Key) == nil {
		r.Files++
		r.Bytes += e.Size
	}
}

// Sweep enforces p on the store, in this order: stale partial writes, time
// to live, per-namespace quota, global cap.
func Sweep(ctx context.Context, s Store, p Policy) (Result, error) {
	entries, err := s.List(ctx, "")
	if err != nil {
		return Result{}, err
	}
	capBytes := int64(-1)
	if p.MaxBytes > 0 {
		capBytes = p.MaxBytes
	}
	return sweepEntries(ctx, s, entries, p, func(int64, int64) int64 { return capBytes }), nil
}

// sweepEntries enforces p on entries. capOf gets the bytes of the live
// entries after the time to live and the quotas, and the bytes those rules
// already deleted, and gives the cap to enforce on them; a negative cap is
// none.
func sweepEntries(ctx context.Context, s Store, entries []Entry, p Policy, capOf func(live, removed int64) int64) Result {
	var res Result
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}

	var live []Entry
	for _, e := range entries {
		age := now.Sub(e.LastUse)
		if e.Partial {
			if age > StaleTmpAge {
				res.remove(ctx, s, e)
			}
			continue
		}
		ttl := p.TTL
		if t, ok := p.ModelTTL[e.Model16]; ok {
			ttl = t
		}
		if ttl > 0 && age > ttl {
			res.remove(ctx, s, e)
			continue
		}
		live = append(live, e)
	}

	// Oldest first: the head of the list is evicted first.
	sort.Slice(live, func(i, j int) bool { return live[i].LastUse.Before(live[j].LastUse) })
	gone := make([]bool, len(live))

	if p.QuotaBytes > 0 {
		used := map[string]int64{}
		for _, e := range live {
			used[e.NSID] += e.Size
		}
		for i, e := range live {
			if e.NSID == SharedNSID || used[e.NSID] <= p.QuotaBytes {
				continue
			}
			res.remove(ctx, s, e)
			used[e.NSID] -= e.Size
			gone[i] = true
		}
	}

	var total int64
	for i, e := range live {
		if !gone[i] {
			total += e.Size
		}
	}
	capBytes := capOf(total, res.Bytes)
	if capBytes < 0 {
		return res
	}
	for i, e := range live {
		if total <= capBytes {
			break
		}
		if gone[i] {
			continue
		}
		res.remove(ctx, s, e)
		total -= e.Size
		gone[i] = true
	}
	return res
}

func purge(ctx context.Context, s Store, nsid string, drop func(Entry) bool) (Result, error) {
	var res Result
	entries, err := s.List(ctx, nsid)
	if err != nil {
		return res, err
	}
	for _, e := range entries {
		if drop(e) {
			res.remove(ctx, s, e)
		}
	}
	return res, nil
}

// purgeMeta deletes the metadata files of the model directories drop picks.
func purgeMeta(ctx context.Context, s Store, drop func(modelDir string) bool) error {
	keys, err := s.Meta(ctx)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if drop(k.ModelDir) {
			if err := s.Delete(ctx, k); err != nil {
				return err
			}
		}
	}
	return nil
}

// PurgeAll deletes every cache file, partial writes and the metadata files of
// the model directories included.
func PurgeAll(ctx context.Context, s Store) (Result, error) {
	res, err := purge(ctx, s, "", func(Entry) bool { return true })
	if err != nil {
		return res, err
	}
	return res, purgeMeta(ctx, s, func(string) bool { return true })
}

// PurgeModel deletes the files of one model (the directories whose name ends
// in -<model16>, of every backend and build), metadata files included.
func PurgeModel(ctx context.Context, s Store, model16 string) (Result, error) {
	if !hex16RE.MatchString(model16) {
		return Result{}, ErrBadID
	}
	res, err := purge(ctx, s, "", func(e Entry) bool { return e.Model16 == model16 })
	if err != nil {
		return res, err
	}
	return res, purgeMeta(ctx, s, func(dir string) bool { return len(dir) == 33 && dir[17:] == model16 })
}

// PurgeAllBefore is PurgeAll for the files last used at or before before. A
// file used later stays, and so do the metadata files of a model directory
// that still holds a cache file. A purge that arrives twice, or late, then
// deletes only what existed when it was asked for (see promptcachesvc).
func PurgeAllBefore(ctx context.Context, s Store, before time.Time) (Result, error) {
	return purgeBefore(ctx, s, before, func(string) bool { return true })
}

// PurgeModelBefore is PurgeModel for the files last used at or before before,
// with the rules of PurgeAllBefore.
func PurgeModelBefore(ctx context.Context, s Store, model16 string, before time.Time) (Result, error) {
	if !hex16RE.MatchString(model16) {
		return Result{}, ErrBadID
	}
	return purgeBefore(ctx, s, before, func(model string) bool { return model == model16 })
}

func purgeBefore(ctx context.Context, s Store, before time.Time, model func(model16 string) bool) (Result, error) {
	res, err := purge(ctx, s, "", func(e Entry) bool { return model(e.Model16) && !e.LastUse.After(before) })
	if err != nil {
		return res, err
	}
	left, err := s.List(ctx, "")
	if err != nil {
		return res, err
	}
	busy := map[string]bool{}
	for _, e := range left {
		busy[e.ModelDir] = true
	}
	return res, purgeMeta(ctx, s, func(dir string) bool {
		return len(dir) == 33 && model(dir[17:]) && !busy[dir]
	})
}

// PurgeNamespace deletes the cache files of one namespace (the files with
// -<ns16>- in the name) in every model directory. Metadata files stay: other
// namespaces use them.
func PurgeNamespace(ctx context.Context, s Store, ns16 string) (Result, error) {
	if !hex16RE.MatchString(ns16) {
		return Result{}, ErrBadID
	}
	return purge(ctx, s, ns16, func(Entry) bool { return true })
}

// Usage is what a store holds.
type Usage struct {
	Bytes      int64            `json:"bytes"`
	Files      int              `json:"files"`
	Models     map[string]Usage `json:"models,omitempty"`
	Namespaces map[string]Usage `json:"namespaces,omitempty"`
}

// sum adds up entries, in total, per model16 and per ns16. Partial writes
// count: they use the disk too.
func sum(entries []Entry) Usage {
	u := Usage{Models: map[string]Usage{}, Namespaces: map[string]Usage{}}
	for _, e := range entries {
		u.Bytes += e.Size
		u.Files++
		m := u.Models[e.Model16]
		m.Bytes += e.Size
		m.Files++
		u.Models[e.Model16] = m
		n := u.Namespaces[e.NSID]
		n.Bytes += e.Size
		n.Files++
		u.Namespaces[e.NSID] = n
	}
	return u
}
