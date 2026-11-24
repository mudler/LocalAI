// SPDX-License-Identifier: MIT
package artifacts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
)

// Swept counts what one sweep removed.
type Swept struct {
	// Expired artifacts whose lifetime ended, with their bytes.
	Expired int
	// Abandoned transfers that no worker owns any more.
	Abandoned int
	// Uninstalled artifacts of an addon that is no longer installed.
	Uninstalled int
	// Orphans are files that no record names.
	Orphans int
}

// recover settles what a stop left behind. It runs before the store is used,
// so no worker owns any transfer yet: every transferring record is interrupted,
// and every file that is not the bytes of a complete record is removed.
func (a *Artifacts) recover() error {
	var interrupted []string
	for _, r := range a.meta.all() {
		if r.State == gateway.ArtifactTransferring {
			interrupted = append(interrupted, r.ID)
		}
	}
	if len(interrupted) > 0 {
		if err := a.meta.mutate(func(d *document) error {
			for _, id := range interrupted {
				if r := d.Artifacts[id]; r != nil && r.State == gateway.ArtifactTransferring {
					r.State, r.Reason = gateway.ArtifactFailed, "interrupted by a restart"
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	a.collectOrphans(context.Background())
	return nil
}

// collectOrphans removes files that no live record or live transfer explains:
// partial files of transfers that are not running, bytes of records that are
// not complete, bytes with no record, and anything that is not a store file.
func (a *Artifacts) collectOrphans(_ context.Context) int {
	// The order matters. A worker registers itself before it creates a file, so
	// a file seen in the listing belongs to a worker that is in the live set
	// taken afterwards, or that has finished and written its final record,
	// which the record snapshot taken last then shows.
	type file struct {
		dir, name string
		part      bool
	}
	var files []file
	for _, dir := range []string{a.blobs.blobs, a.blobs.staging} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			files = append(files, file{dir: dir, name: e.Name(), part: dir == a.blobs.staging})
		}
	}
	a.mu.Lock()
	live := make(map[string]bool, len(a.jobs))
	for id := range a.jobs {
		live[id] = true
	}
	a.mu.Unlock()
	records := map[string]record{}
	for _, r := range a.meta.all() {
		records[r.ID] = r
	}
	removed := 0
	for _, f := range files {
		id := f.name
		if f.part {
			id = strings.TrimSuffix(f.name, ".part")
		}
		keep := false
		if idPattern.MatchString(id) && f.part == strings.HasSuffix(f.name, ".part") {
			if live[id] {
				keep = true
			} else if r, ok := records[id]; ok && !f.part && r.State == gateway.ArtifactComplete {
				keep = true
			}
		}
		if keep {
			continue
		}
		if os.Remove(filepath.Join(f.dir, f.name)) == nil {
			removed++
		}
	}
	return removed
}

// Sweep is one pass of expiry cleanup. It removes artifacts at their expiry,
// transfers that no worker owns, artifacts of an addon that is no longer
// installed, and files that no record names. A disabled addon's complete
// artifacts are kept until their normal expiry.
func (a *Artifacts) Sweep(ctx context.Context) (Swept, error) {
	var out Swept
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return out, ErrClosed
	}
	a.mu.Unlock()
	now := a.now()
	gone := map[string]*int{}
	installed := map[string]bool{}
	check := func(addonID string) bool {
		if v, ok := installed[addonID]; ok {
			return v
		}
		ok := true
		if a.opts.Addon != nil {
			// A lookup that cannot answer is not proof of an uninstall.
			if st, err := a.opts.Addon(ctx, addonID); err == nil && !st.Installed {
				ok = false
			}
		}
		installed[addonID] = ok
		return ok
	}
	var drop []string
	abandoned := []string{}
	for _, r := range a.meta.all() {
		a.mu.Lock()
		_, live := a.jobs[r.ID]
		a.mu.Unlock()
		switch {
		case !now.Before(r.ExpiresAt):
			drop = append(drop, r.ID)
			gone[r.ID] = &out.Expired
		case r.State == gateway.ArtifactTransferring && !live:
			abandoned = append(abandoned, r.ID)
		case !check(r.AddonID):
			drop = append(drop, r.ID)
			gone[r.ID] = &out.Uninstalled
		}
	}
	// The snapshot above is stale by now: a transfer that looked abandoned can
	// have completed since. What is dropped, and which bytes go with it, is
	// decided again under the metadata lock, and only the records this
	// mutation really changed lose their bytes. a.mu is held across it, as in
	// reserve, so a record that is transferring and has no worker is
	// abandoned for certain.
	var removed, settled []string
	if len(drop) > 0 || len(abandoned) > 0 {
		a.mu.Lock()
		err := a.meta.mutate(func(d *document) error {
			removed, settled = nil, nil
			for _, id := range drop {
				if d.Artifacts[id] != nil {
					delete(d.Artifacts, id)
					removed = append(removed, id)
				}
			}
			for _, id := range abandoned {
				r := d.Artifacts[id]
				if r == nil || r.State != gateway.ArtifactTransferring {
					continue
				}
				if _, live := a.jobs[id]; live {
					continue
				}
				r.State, r.Reason = gateway.ArtifactFailed, "transfer abandoned"
				settled = append(settled, id)
			}
			return nil
		})
		a.mu.Unlock()
		if err != nil {
			return out, err
		}
		for _, id := range removed {
			*gone[id]++
		}
		out.Abandoned = len(settled)
	}
	// The record is gone or settled first, so bytes are never reachable while
	// they are being removed. Whatever removal misses, the orphan pass finds.
	var errs []error
	for _, id := range append(removed, settled...) {
		if err := a.objects.Delete(ctx, id); err != nil {
			errs = append(errs, err)
		}
	}
	out.Orphans = a.collectOrphans(ctx)
	return out, errors.Join(errs...)
}

// Run sweeps on every tick until ctx ends or the store closes. It is the
// background loop; a second Run is not needed and returns at once.
func (a *Artifacts) Run(ctx context.Context) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.wg.Add(1)
	a.mu.Unlock()
	defer a.wg.Done()
	tick := a.opts.Tick
	if tick == nil {
		t := time.NewTicker(a.limits.SweepInterval)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.ctx.Done():
			return
		case <-tick:
			_, _ = a.Sweep(ctx)
		}
	}
}

// Close stops the sweep, cancels running transfers and streams, waits for
// them, and releases the store. Nothing is written after it returns. Transfers
// that were cut short are recorded as failed and their bytes removed.
func (a *Artifacts) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	a.mu.Unlock()
	a.cancel()
	a.wg.Wait()
	return a.meta.Close()
}
