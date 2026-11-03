// SPDX-License-Identifier: MIT
package hostgrants

import (
	"slices"
	"sort"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
)

// AuthorityConfig builds the hostops.Authority of the owner.
type AuthorityConfig struct {
	// Floor is the startup policy of the file and flags.
	Floor Floor
	// Store is the UI document. Nil gives a static authority over Floor.
	Store *Store
	// Context returns what the evaluator needs for one add-on: the trusted
	// identity of the installed package, the journal's view of completed
	// operations and the host registry. Nil gives an empty Context, which
	// ignores every UI entry (unknown identity) and accepts any name.
	Context func(addonID string) Context
}

type authority struct {
	floor Floor
	store *Store
	ctx   func(string) Context
}

// NewAuthority returns an Authority that reads the store on every call, so a
// grant, revoke or deny applies to the next call and never to one that runs.
func NewAuthority(cfg AuthorityConfig) hostops.Authority {
	return &authority{floor: cfg.Floor, store: cfg.Store, ctx: cfg.Context}
}

func (a *authority) view(addonID string) (Snapshot, Context) {
	doc := emptySnapshot()
	if a.store != nil {
		doc = a.store.View()
	}
	var c Context
	if a.ctx != nil {
		c = a.ctx(addonID)
	}
	return doc, c
}

func (a *authority) Allows(addonID, op string) (bool, hostops.Reason) {
	doc, c := a.view(addonID)
	d := a.floor.Decide(doc, c, addonID, op)
	return d.Allowed, hostops.Reason(d.Reason)
}

func (a *authority) Entitled(addonID string) bool {
	doc, c := a.view(addonID)
	return a.floor.Entitled(doc, c, addonID)
}

func (a *authority) Snapshot(addonID string) hostops.EffectiveSet {
	doc, c := a.view(addonID)
	set := hostops.EffectiveSet{Revision: doc.Revision, Prefixes: slices.Clone(a.floor.Grants[addonID])}
	for _, g := range doc.Grants[addonID].Ops {
		if d := a.floor.Decide(doc, c, addonID, g.Name); d.Allowed && d.Source != SourceDeployment {
			set.Operations = append(set.Operations, g.Name)
		}
	}
	for _, rules := range []map[string][]string{a.floor.Deny, doc.Deny} {
		for _, key := range [...]string{addonID, "*"} {
			for _, e := range rules[key] {
				if !slices.Contains(set.Denied, e) {
					set.Denied = append(set.Denied, e)
				}
			}
		}
	}
	sort.Strings(set.Prefixes)
	sort.Strings(set.Operations)
	sort.Strings(set.Denied)
	return set
}
