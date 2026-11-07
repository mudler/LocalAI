// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"sort"
	"time"
)

// SweepReport counts what one SweepOrphans pass did.
type SweepReport struct {
	// Dropped is the number of records whose resource was gone.
	Dropped int
	// Kept is the number of records whose backing could not be checked now.
	Kept int
}

// SweepOrphans drops the active share records whose resource is gone: a
// saved agent that was deleted while the owner could not be reached, or by a
// version of LocalAI that did not drop the record. Each record of a
// registered kind that is not an add-on is checked with the Backing of its
// kind. Only a definite answer that the backing is gone drops a record; an
// error keeps it for a later pass. A dropped record loses every grant (each
// one is logged as a removal) and its admitted work is cancelled, so a share
// never outlives its resource and never holds quota for good.
//
// The host runs it once at startup, before the policy is served, so no
// creation of this process can race it. A record that this process is
// creating is left alone all the same.
func (p *Policy) SweepOrphans(ctx context.Context) (SweepReport, error) {
	var rep SweepReport
	for _, ref := range p.activeShareRecords() {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		spec, ok := p.spec(ref.Kind)
		if !ok || spec.Backing == nil || p.busy(ref.ResourceID) {
			continue
		}
		found, err := spec.Backing(ctx, ref)
		if err != nil {
			rep.Kept++
			continue
		}
		if found {
			continue
		}
		dropped, err := p.dropOrphan(ctx, ref)
		if err != nil {
			return rep, err
		}
		if dropped {
			rep.Dropped++
		}
	}
	return rep, nil
}

// activeShareRecords lists the active records of the kinds that are not
// add-ons, in ID order.
func (p *Policy) activeShareRecords() []Ref {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	var out []Ref
	for id, r := range p.store.doc.Resources {
		if r.State == stateActive && shareKind(r.Kind) {
			out = append(out, Ref{Kind: r.Kind, ResourceID: id})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ResourceID < out[j].ResourceID })
	return out
}

// dropOrphan removes the record under its gate, logs a removal for every
// grant, and cancels and waits for its admitted work within the drain bound.
func (p *Policy) dropOrphan(ctx context.Context, ref Ref) (bool, error) {
	g := p.gate(ref.ResourceID)
	g.mu.Lock()
	dropped := false
	err := p.store.mutate(func(d *document) error {
		rec := d.Resources[ref.ResourceID]
		if rec == nil || rec.Kind != ref.Kind || rec.State != stateActive {
			return nil
		}
		now := time.Now().UTC()
		principals := make([]string, 0, len(rec.Grants))
		for u := range rec.Grants {
			principals = append(principals, u)
		}
		sort.Strings(principals)
		for _, u := range principals {
			d.addRevocation(revocation{Kind: rec.Kind, ResourceID: ref.ResourceID, UserID: u, At: now})
		}
		delete(d.Resources, ref.ResourceID)
		dropped = true
		return nil
	})
	if err != nil || !dropped {
		g.mu.Unlock()
		return false, err
	}
	victims := make([]*admitted, 0, len(g.active))
	for a := range g.active {
		a.cancel()
		victims = append(victims, a)
	}
	g.mu.Unlock()
	timer := time.NewTimer(p.opts.DrainTimeout)
	defer timer.Stop()
	for _, a := range victims {
		select {
		case <-a.done:
		case <-timer.C:
			// The record is gone, so the work can do nothing more under it:
			// every later check of the work denies.
			return true, nil
		case <-ctx.Done():
			return true, ctx.Err()
		}
	}
	return true, nil
}
