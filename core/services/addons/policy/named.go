// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"slices"
	"strings"
	"unicode"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// A service that already names its resources (a connector binding has an ID,
// a collection has a name) keeps that name and gets one policy record for it
// when the owner first shares it. The record carries the name in Local, so
// the grant is looked up through the record and nothing is copied or moved.

// maxLocal bounds the service's name of a resource.
const maxLocal = 256

// validLocal says whether a name can be kept on a record: 1 to 256 bytes,
// valid text, no control characters.
func validLocal(name string) bool {
	return name != "" && len(name) <= maxLocal && strings.ToValidUTF8(name, "") == name && !strings.ContainsFunc(name, unicode.IsControl)
}

// EnsureRef returns the record of the resource that the actor names local in
// the kind, and makes it when there is none. Only the owner names a resource:
// the record is the actor's. Repeating the call returns the same record. It
// needs named-user mode, a registered kind that is not an add-on and the
// kind's CreateAuthority.
func (p *Policy) EnsureRef(ctx context.Context, actor contract.Actor, kind, local string) (Ref, error) {
	spec, ok := p.spec(kind)
	if _, addon := addonOf(kind); !ok || addon || strings.HasPrefix(kind, addonKindPrefix) || spec.CreateAuthority == nil || !validLocal(local) || p.store.Mode() != ModeNamedUser {
		return Ref{}, ErrForbidden
	}
	if err := p.currentKind(ctx, actor, kind); err != nil {
		return Ref{}, err
	}
	if err := spec.CreateAuthority(ctx, actor); err != nil {
		if ctx.Err() != nil {
			return Ref{}, ctx.Err()
		}
		return Ref{}, p.classify(err)
	}
	fresh, err := randomResourceID()
	if err != nil {
		return Ref{}, err
	}
	owner := p.store.Mode().ownerOf(actor)
	id := ""
	err = p.store.mutate(func(d *document) error {
		for rid, r := range d.Resources {
			if r.Owner == owner && r.Kind == kind && r.Local == local && r.State == stateActive {
				id = rid
				return nil
			}
		}
		if err := p.room(d, owner, kind); err != nil {
			return err
		}
		id = fresh
		d.Resources[id] = &record{Kind: kind, Owner: owner, State: stateActive, Grants: map[string]Role{}, Local: local}
		return nil
	})
	if err != nil {
		return Ref{}, err
	}
	return Ref{Kind: kind, ResourceID: id}, nil
}

// FindRef returns the active record that the owner keeps under local in the
// kind. It decides nothing: the caller authorizes the actor on the Ref.
func (p *Policy) FindRef(kind, owner, local string) (Ref, bool) {
	if local == "" || owner == "" {
		return Ref{}, false
	}
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	for id, r := range p.store.doc.Resources {
		if r.Kind == kind && r.Owner == owner && r.Local == local && r.State == stateActive {
			return Ref{Kind: kind, ResourceID: id}, true
		}
	}
	return Ref{}, false
}

// LocalOf returns the owner and the service's name of an active record of the
// kind. It decides nothing, like FindRef.
func (p *Policy) LocalOf(ref Ref) (owner, local string, ok bool) {
	r, found := p.store.get(ref.ResourceID)
	if !found || r.Kind != ref.Kind || r.State != stateActive || r.Local == "" {
		return "", "", false
	}
	return r.Owner, r.Local, true
}

// ForgetRef drops the record of a resource that its service deleted. Only the
// owner may. Every grant is revoked first, so the work of every grantee has
// stopped when the record goes, and a later resource under the same name
// starts with no grant.
func (p *Policy) ForgetRef(ctx context.Context, actor contract.Actor, ref Ref) error {
	if p.store.Mode() != ModeNamedUser {
		return ErrForbidden
	}
	// A grant made while the others were revoked is revoked on the next
	// round; the bound only stops a loop against a writer that never stops.
	for range 4 {
		if err := p.AuthorizeRef(ctx, actor, ref, ActionDelete); err != nil {
			return err
		}
		r, _ := p.store.get(ref.ResourceID)
		principals := make([]string, 0, len(r.Grants))
		for u := range r.Grants {
			principals = append(principals, u)
		}
		// The grant to everyone first: it reaches the most work.
		slices.SortFunc(principals, func(a, b string) int {
			switch {
			case a == Everyone:
				return -1
			case b == Everyone:
				return 1
			}
			return strings.Compare(a, b)
		})
		for _, u := range principals {
			if err := p.RevokeRef(ctx, actor, ref, u); err != nil {
				return err
			}
		}
		g := p.gate(ref.ResourceID)
		g.mu.Lock()
		owner := p.store.Mode().ownerOf(actor)
		dropped := false
		err := p.store.mutate(func(d *document) error {
			rec := d.Resources[ref.ResourceID]
			if rec == nil || rec.Kind != ref.Kind || rec.Owner != owner || rec.State != stateActive {
				return ErrForbidden
			}
			if len(rec.Grants) > 0 || len(rec.Drain) > 0 {
				return nil
			}
			delete(d.Resources, ref.ResourceID)
			dropped = true
			return nil
		})
		g.mu.Unlock()
		if err != nil || dropped {
			return err
		}
	}
	return ErrUnavailable
}
