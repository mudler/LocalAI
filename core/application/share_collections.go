// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/policy"
)

// A collection shared with a person is found by name, like their own, and is
// read and written where it is: under its owner. The person's own collection
// wins a name. A shared collection whose name the person also uses, or that
// two owners share under one name, is addressed as "shared:<id>", where <id>
// is its ID in the sharing API.

// SharedCollectionPrefix starts the unambiguous name of a shared collection.
const SharedCollectionPrefix = "shared:"

var (
	// ErrCollectionNotFound is the answer for a collection the caller does not
	// have and that is not shared with the caller: the two look the same.
	ErrCollectionNotFound = errors.New("collection not found")
	// ErrCollectionRole means the collection is shared with the caller, with a
	// role that does not allow the action.
	ErrCollectionRole = errors.New("your role on this shared collection does not allow this")
)

// SharedCollection is a collection of another person shared with the caller.
type SharedCollection struct {
	// Address is the name the caller uses for it: its own name when that is
	// free, else SharedCollectionPrefix and its ID.
	Address string
	// Name is its name under its owner.
	Name string
	// ID is its ID in the sharing API.
	ID        string
	OwnerName string
	// Role is viewer or editor.
	Role     string
	Everyone bool
	// owner is the account the collection is stored under. It stays here.
	owner string
	ref   policy.Ref
}

// CollectionShare is the share of one of the caller's own collections.
type CollectionShare struct {
	ID         string
	SharedWith int
}

// CollectionSharing says whether this process shares collections.
func (a *Application) CollectionSharing() bool { return a.hostSharing() != nil }

// SharedCollections lists the collections shared with user, each with the
// name the user addresses it by. own is the list of the user's own names.
func (a *Application) SharedCollections(ctx context.Context, user string, own []string) []SharedCollection {
	if user == "" {
		return nil
	}
	refs, err := a.sharedRefs(ctx, KindCollection, user)
	if err != nil {
		return nil
	}
	count := map[string]int{}
	for _, r := range refs {
		count[r.Name]++
	}
	out := make([]SharedCollection, 0, len(refs))
	for _, r := range refs {
		addr := r.Name
		if slices.Contains(own, r.Name) || count[r.Name] > 1 || strings.HasPrefix(r.Name, SharedCollectionPrefix) {
			addr = SharedCollectionPrefix + r.Ref.ResourceID
		}
		out = append(out, SharedCollection{Address: addr, Name: r.Name, ID: r.Ref.ResourceID, OwnerName: r.OwnerName, Role: string(r.Role),
			Everyone: r.Everyone, owner: r.Owner, ref: r.Ref})
	}
	slices.SortFunc(out, func(x, y SharedCollection) int { return strings.Compare(x.Address, y.Address) })
	return out
}

// CollectionShares returns the shares of the user's own collections, by name.
func (a *Application) CollectionShares(ctx context.Context, user string, own []string) map[string]CollectionShare {
	out := map[string]CollectionShare{}
	if user == "" {
		return out
	}
	records := a.ownedRefs(ctx, KindCollection, user)
	for _, name := range own {
		if v, ok := records[name]; ok {
			out[name] = CollectionShare{ID: v.Ref.ResourceID, SharedWith: v.Shared}
		}
	}
	return out
}

// EnsureCollectionShare makes a collection of the user shareable and returns
// its ID in the sharing API. A collection the user does not own is
// ErrCollectionNotFound.
func (a *Application) EnsureCollectionShare(ctx context.Context, user, name string) (string, error) {
	svc := a.CollectionsService()
	p := a.hostSharing()
	if svc == nil || p == nil || user == "" {
		return "", errSharingOff
	}
	own, err := svc.ListForUser(user)
	if err != nil {
		return "", err
	}
	if !slices.Contains(own, name) {
		return "", ErrCollectionNotFound
	}
	ref, err := p.EnsureRef(ctx, hostActor(user), KindCollection, name)
	if err != nil {
		return "", err
	}
	return ref.ResourceID, nil
}

var errSharingOff = errors.New("sharing is not available on this server")

// ErrSharingOff is the answer when this process does not share collections.
var ErrSharingOff = errSharingOff

// WithCollection runs run on the collection that user names, with the account
// it is stored under and its name there. The user's own collection runs as
// before. A collection shared with the user runs as admitted work of its
// share: a lowering or a revocation cancels the context of run, and the next
// call answers as for a collection that does not exist. action is
// policy.ActionRead for a search or a read, policy.ActionWrite to add or
// remove entries, and policy.ActionSchema for what only the owner does.
func (a *Application) WithCollection(ctx context.Context, user, name string, action policy.Action, run func(ctx context.Context, owner, name string) error) error {
	svc := a.CollectionsService()
	if svc == nil {
		return ErrCollectionNotFound
	}
	own, err := svc.ListForUser(user)
	if err != nil {
		return err
	}
	if slices.Contains(own, name) || user == "" {
		return run(ctx, user, name)
	}
	target, ok := a.sharedCollection(ctx, user, own, name)
	if !ok {
		return ErrCollectionNotFound
	}
	allowed := false
	for _, r := range collectionRoles(action) {
		allowed = allowed || string(r) == target.Role
	}
	if !allowed {
		return ErrCollectionRole
	}
	p := a.hostSharing()
	if p == nil {
		return ErrCollectionNotFound
	}
	err = p.AdmitRef(ctx, hostActor(user), target.ref, action, func(ctx context.Context) error {
		return run(ctx, target.owner, target.Name)
	})
	if denied(err) {
		return ErrCollectionNotFound
	}
	return err
}

// sharedCollection finds the shared collection that the user names: by its
// unambiguous name, or by its own name when that names exactly one.
func (a *Application) sharedCollection(ctx context.Context, user string, own []string, name string) (SharedCollection, bool) {
	var found []SharedCollection
	for _, s := range a.SharedCollections(ctx, user, own) {
		if s.Address == name || SharedCollectionPrefix+s.ID == name {
			return s, true
		}
		if s.Name == name {
			found = append(found, s)
		}
	}
	if len(found) == 1 && !slices.Contains(own, name) {
		return found[0], true
	}
	return SharedCollection{}, false
}
