// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/recovery"
)

// errHeld is the answer of an admission while an operation that needs the
// add-on's data to stand still (a backup, an upgrade, a restore or a purge)
// holds it. It is unavailability, never a denial: nothing about the caller's
// authority changed.
var errHeld = fmt.Errorf("addon is being backed up or recovered: %w", gateway.ErrUnavailable)

func (p *Policy) isHeld(addonID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.held[addonID] > 0
}

// resourcesOf lists the resource IDs of an add-on in any state.
func (s *Store) resourcesOf(addonID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id, r := range s.doc.Resources {
		if r.AddonID == addonID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// HoldAddon stops new admission for every resource of the add-on and waits,
// within the drain bound, for the work that was admitted. Admissions made
// while it is held are unavailable. The release function lifts the hold and
// can be called more than once.
func (p *Policy) HoldAddon(ctx context.Context, addonID string) (func(), error) {
	if addonID == "" {
		return func() {}, ErrForbidden
	}
	p.mu.Lock()
	if p.held == nil {
		p.held = map[string]int{}
	}
	p.held[addonID]++
	p.mu.Unlock()
	released := false
	release := func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if released {
			return
		}
		released = true
		if p.held[addonID] <= 1 {
			delete(p.held, addonID)
		} else {
			p.held[addonID]--
		}
	}
	var victims []*admitted
	for _, id := range p.store.resourcesOf(addonID) {
		g := p.gate(id)
		g.mu.Lock()
		for a := range g.active {
			a.cancel()
			victims = append(victims, a)
		}
		g.mu.Unlock()
	}
	timer := time.NewTimer(p.opts.DrainTimeout)
	defer timer.Stop()
	for _, a := range victims {
		select {
		case <-a.done:
		case <-timer.C:
			release()
			return func() {}, ErrDrainPending
		case <-ctx.Done():
			release()
			return func() {}, ErrDrainPending
		}
	}
	return release, nil
}

// addonSnapshot is the owner's access state of one add-on at the time of a
// backup. Only active resources are part of it: a pending reservation and a
// deletion in progress are not data that can be restored.
type addonSnapshot struct {
	Version   int                         `json:"version"`
	AddonID   string                      `json:"addonId"`
	Mode      Mode                        `json:"mode"`
	Created   time.Time                   `json:"created"`
	Resources map[string]snapshotResource `json:"resources"`
}

type snapshotResource struct {
	Owner  string          `json:"owner"`
	Grants map[string]Role `json:"grants,omitempty"`
}

// exportAddon reads the add-on's access state. The time of the snapshot is
// read under the same lock as the state: every lowering that is not in it is
// committed after that time, and a restore applies the lowerings after it.
func (s *Store) exportAddon(addonID string, now func() time.Time) addonSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := addonSnapshot{Version: 1, AddonID: addonID, Mode: s.mode, Created: now(), Resources: map[string]snapshotResource{}}
	for id, r := range s.doc.Resources {
		if r.AddonID != addonID || r.State != stateActive {
			continue
		}
		g := make(map[string]Role, len(r.Grants))
		for u, role := range r.Grants {
			g[u] = role
		}
		out.Resources[id] = snapshotResource{Owner: r.Owner, Grants: g}
	}
	return out
}

func lowerRole(a, b Role) Role {
	if a.rank() <= b.rank() {
		return a
	}
	return b
}

// restoreAddon replaces the add-on's resource records with the snapshot after
// checking every owner and grant against the present: the account directory
// and the lowerings of access recorded since the backup. Nothing a person
// lost since the backup is given back.
func (s *Store) restoreAddon(ctx context.Context, snap addonSnapshot, o recovery.RestoreOptions) (recovery.RestoreReport, error) {
	var rep recovery.RestoreReport
	if snap.Version != 1 || snap.AddonID == "" || (snap.Mode != ModeNamedUser && snap.Mode != ModeLocalOwner) {
		return rep, recovery.ErrBackupCorrupt
	}
	now := s.mode
	if now == ModeLocalOwner && snap.Mode == ModeNamedUser && len(snap.Resources) > 0 {
		return rep, recovery.ErrDowngradeUnsupported
	}
	account := func(user string) bool {
		if now == ModeLocalOwner {
			return true
		}
		return o.Account != nil && o.Account(ctx, user) == nil
	}
	mapped := func(user string) string {
		if to, ok := o.Mapping[user]; ok {
			return to
		}
		return user
	}
	// The cutoff is the time of the snapshot itself, never later than the
	// time the caller gives: the manifest of a backup is written after the
	// state was read, and a lowering committed in between is not in it.
	since := o.Since
	if !snap.Created.IsZero() && (since.IsZero() || snap.Created.Before(since)) {
		since = snap.Created
	}
	err := s.mutate(func(d *document) error {
		rep = recovery.RestoreReport{}
		// Lowerings older than the floor may have been dropped from the log.
		// Nothing in the backup can then be shown to be still allowed: the
		// data comes back and the access does not.
		truncated := !d.RevokedFloor.IsZero() && since.Before(d.RevokedFloor)
		rep.HistoryTruncated = truncated
		for id, r := range d.Resources {
			if r.AddonID == snap.AddonID {
				delete(d.Resources, id)
			}
		}
		ids := make([]string, 0, len(snap.Resources))
		for id := range snap.Resources {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			sr := snap.Resources[id]
			if other, taken := d.Resources[id]; taken && other.AddonID != snap.AddonID {
				rep.ResourcesSkipped = append(rep.ResourcesSkipped, id)
				continue
			}
			owner := LocalOwnerID
			if now == ModeNamedUser {
				owner = mapped(sr.Owner)
				if snap.Mode == ModeLocalOwner {
					owner = o.Mapping[LocalOwnerID]
				}
				if !validUserID(owner) || !account(owner) {
					rep.ResourcesSkipped = append(rep.ResourcesSkipped, id)
					continue
				}
			}
			grants := map[string]Role{}
			if now == ModeNamedUser {
				users := make([]string, 0, len(sr.Grants))
				for u := range sr.Grants {
					users = append(users, u)
				}
				sort.Strings(users)
				for _, u := range users {
					role := sr.Grants[u]
					target := mapped(u)
					if truncated {
						rep.GrantsDropped++
						continue
					}
					if (role != RoleViewer && role != RoleEditor) || !validUserID(target) || target == owner || !account(target) {
						rep.GrantsDropped++
						continue
					}
					// A lowering after the backup is applied again, and so is
					// a removal: the strictest one stands.
					drop := false
					for _, v := range d.Revoked {
						if v.AddonID != snap.AddonID || v.ResourceID != id || v.UserID != target || v.At.Before(since) {
							continue
						}
						if v.Ceiling == "" {
							drop = true
							break
						}
						role = lowerRole(role, v.Ceiling)
					}
					if drop {
						rep.GrantsDropped++
						rep.Revoked++
						continue
					}
					if role != sr.Grants[u] {
						rep.Revoked++
					}
					grants[target] = role
					rep.GrantsRestored++
				}
			}
			d.Resources[id] = &record{AddonID: snap.AddonID, Owner: owner, State: stateActive, Grants: grants}
			rep.ResourcesRestored++
		}
		return nil
	})
	return rep, err
}

// forgetAddon removes the add-on's records, grants, tombstones and
// revocations.
func (s *Store) forgetAddon(addonID string) (int, error) {
	n := 0
	err := s.mutate(func(d *document) error {
		n = 0
		for id, r := range d.Resources {
			if r.AddonID == addonID {
				delete(d.Resources, id)
				n++
			}
		}
		for id, t := range d.Deleted {
			if t.AddonID == addonID {
				delete(d.Deleted, id)
			}
		}
		kept := d.Revoked[:0:0]
		for _, v := range d.Revoked {
			if v.AddonID != addonID {
				kept = append(kept, v)
			}
		}
		d.Revoked = kept
		return nil
	})
	return n, err
}

// proposalExport is the proposals of one add-on as a backup keeps them. They
// are kept for audit and for reconciling outcomes; a restore never makes one
// of them confirmable again.
type proposalExport struct {
	Version   int              `json:"version"`
	AddonID   string           `json:"addonId"`
	Proposals []proposalRecord `json:"proposals"`
}

func (b *Broker) exportAddon(addonID string) proposalExport {
	out := proposalExport{Version: 1, AddonID: addonID, Proposals: []proposalRecord{}}
	for _, r := range b.store.all() {
		if r.AddonID == addonID {
			out.Proposals = append(out.Proposals, r)
		}
	}
	return out
}

// afterRestore settles the proposals of an add-on whose data went back in
// time. A proposal that was pending is cancelled: it was made against data
// that no longer exists. One that was executing, or whose outcome was not
// known, becomes outcome-unknown, and the receipt lookup of the restored
// add-on decides; it is never applied again.
func (b *Broker) afterRestore(addonID string) (cancelled, unknown int, err error) {
	err = b.store.mutate(func(d *proposalDocument) error {
		cancelled, unknown = 0, 0
		now := b.store.now()
		for _, r := range d.Proposals {
			if r.AddonID != addonID {
				continue
			}
			switch r.State {
			case gateway.ProposalPending:
				r.State, r.Reason, r.UpdatedAt = gateway.ProposalCancelled, "the add-on was restored from a backup", now
				cancelled++
			case gateway.ProposalExecuting:
				r.State, r.Reason, r.UpdatedAt = gateway.ProposalOutcomeUnknown, "the add-on was restored from a backup", now
				r.UnknownSince = now
				unknown++
			case gateway.ProposalOutcomeUnknown:
				unknown++
			}
		}
		return nil
	})
	return cancelled, unknown, err
}

func (b *Broker) forgetAddon(addonID string) error {
	return b.store.mutate(func(d *proposalDocument) error {
		for id, r := range d.Proposals {
			if r.AddonID == addonID {
				delete(d.Proposals, id)
			}
		}
		return nil
	})
}

func (b *Broker) appliedSince(addonID string, since time.Time) int {
	n := 0
	for _, r := range b.store.all() {
		if r.AddonID == addonID && r.State == gateway.ProposalApplied && r.UpdatedAt.After(since) {
			n++
		}
	}
	return n
}

// RecoveryPort is the policy and the proposal broker as the recovery package
// sees them.
type RecoveryPort struct {
	Policy *Policy
	Broker *Broker
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

var _ recovery.PolicyPort = (*RecoveryPort)(nil)

func (r *RecoveryPort) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// Mode is the ownership mode of the store.
func (r *RecoveryPort) Mode() string { return string(r.Policy.Mode()) }

// Hold stops admission, invalidates the pending proposals of every resource
// of the add-on and waits for admitted work.
func (r *RecoveryPort) Hold(ctx context.Context, addonID string) (func(), error) {
	release, err := r.Policy.HoldAddon(ctx, addonID)
	if err != nil {
		return func() {}, err
	}
	if r.Broker != nil {
		for _, id := range r.Policy.store.resourcesOf(addonID) {
			if err = r.Broker.InvalidateResource(ctx, ResourceKey{AddonID: addonID, ResourceID: id}); err != nil {
				release()
				return func() {}, ErrUnavailable
			}
		}
	}
	return release, nil
}

// Export returns the access state and the proposals of the add-on.
func (r *RecoveryPort) Export(_ context.Context, addonID string) (recovery.PolicyExport, error) {
	pol, err := json.Marshal(r.Policy.store.exportAddon(addonID, r.now))
	if err != nil {
		return recovery.PolicyExport{}, err
	}
	props := []byte(`{"version":1,"proposals":[]}`)
	if r.Broker != nil {
		if props, err = json.Marshal(r.Broker.exportAddon(addonID)); err != nil {
			return recovery.PolicyExport{}, err
		}
	}
	return recovery.PolicyExport{Policy: pol, Proposals: props}, nil
}

// Restore replaces the add-on's records with the exported ones, checked
// against the present, and settles its proposals.
func (r *RecoveryPort) Restore(ctx context.Context, addonID string, state recovery.PolicyExport, o recovery.RestoreOptions) (recovery.RestoreReport, error) {
	var snap addonSnapshot
	if err := json.Unmarshal(state.Policy, &snap); err != nil || snap.AddonID != addonID {
		return recovery.RestoreReport{}, recovery.ErrBackupCorrupt
	}
	rep, err := r.Policy.store.restoreAddon(ctx, snap, o)
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			return rep, err
		}
		return rep, err
	}
	if r.Broker != nil {
		c, u, err := r.Broker.afterRestore(addonID)
		if err != nil {
			return rep, ErrUnavailable
		}
		rep.ProposalsCancelled, rep.ProposalsUnknown = c, u
	}
	return rep, nil
}

// LostWrites counts the operations applied through proposals after since.
func (r *RecoveryPort) LostWrites(_ context.Context, addonID string, since time.Time) (int, error) {
	if r.Broker == nil {
		return 0, nil
	}
	return r.Broker.appliedSince(addonID, since), nil
}

// Forget removes every record the owner keeps for the add-on.
func (r *RecoveryPort) Forget(_ context.Context, addonID string) (int, error) {
	n, err := r.Policy.store.forgetAddon(addonID)
	if err != nil {
		return 0, err
	}
	if r.Broker != nil {
		if err = r.Broker.forgetAddon(addonID); err != nil {
			return n, ErrUnavailable
		}
	}
	return n, nil
}

// MigrateOwnership changes the ownership model of the owner's policy store
// under root. The owner must be stopped: the store lock is taken without
// waiting. A backup of the store is written first under root/backups. Moving
// to named-user mode needs the mapping of the single local-owner principal to
// the chosen account; moving to local-owner mode is refused as soon as the
// store holds any resource, because the owners would be merged into one.
// It returns the directory of the backup.
func MigrateOwnership(root string, to Mode, mapping recovery.OwnershipMapping, now time.Time) (string, error) {
	if !to.valid() || !filepath.IsAbs(root) {
		return "", ErrMigrationRequired
	}
	dir := filepath.Join(root, "policy")
	d, err := peek(dir)
	if err != nil {
		return "", err
	}
	if d.Mode == to {
		return "", nil
	}
	named := ""
	switch to {
	case ModeLocalOwner:
		if len(d.Resources) > 0 {
			return "", recovery.ErrDowngradeUnsupported
		}
		if len(mapping) != 0 {
			return "", recovery.ErrMappingInvalid
		}
	case ModeNamedUser:
		for from, target := range mapping {
			if from != LocalOwnerID || !validUserID(target) {
				return "", recovery.ErrMappingInvalid
			}
		}
		named = mapping[LocalOwnerID]
		if len(d.Resources) > 0 && named == "" {
			return "", recovery.ErrMappingRequired
		}
	}
	backup := filepath.Join(root, "backups", "ownership", now.UTC().Format("20060102t150405z")+"-"+string(d.Mode)+"-to-"+string(to))
	if err = MigrateMode(dir, to, backup, named); err != nil {
		return "", err
	}
	return backup, nil
}

// peek reads the store document without keeping the lock.
func peek(dir string) (*document, error) {
	lock, err := lockDir(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Close() }()
	if _, err = os.Stat(filepath.Join(dir, fileName)); errors.Is(err, os.ErrNotExist) {
		return &document{Version: 1, Resources: map[string]*record{}}, nil
	}
	return load(dir)
}
