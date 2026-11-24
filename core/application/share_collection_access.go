// SPDX-License-Identifier: MIT
package application

import (
	"context"

	"github.com/mudler/LocalAI/core/services/addons/policy"
	lagi "github.com/mudler/LocalAI/core/services/collections/backend"
)

// CollectionSources lists the URLs a collection follows. It is the owner's
// alone: a source URL can carry a credential in its query, so a person the
// collection is shared with, whatever the role, gets ErrCollectionRole.
func (a *Application) CollectionSources(ctx context.Context, user, name string) ([]lagi.SourceInfo, error) {
	svc := a.CollectionsService()
	if svc == nil {
		return nil, ErrCollectionNotFound
	}
	var sources []lagi.SourceInfo
	err := a.WithCollection(ctx, user, name, policy.ActionSchema, func(_ context.Context, owner, coll string) error {
		var err error
		sources, err = svc.ListSourcesForUser(owner, coll)
		return err
	})
	return sources, err
}

// ServeCollectionEntryFile finds the file of an entry and runs serve with its
// path inside the admission of the share, the whole download included. A
// lowering or a revocation that takes the read away cancels the context of
// serve and waits until serve returned, so a revoked person gets no more
// bytes once the change answered. serve must stop writing when its context
// ends.
func (a *Application) ServeCollectionEntryFile(ctx context.Context, user, name, entry string, serve func(ctx context.Context, path string) error) error {
	svc := a.CollectionsService()
	if svc == nil {
		return ErrCollectionNotFound
	}
	return a.WithCollection(ctx, user, name, policy.ActionRead, func(ctx context.Context, owner, coll string) error {
		path, err := svc.EntryFilePathForUser(owner, coll, entry)
		if err != nil {
			return err
		}
		return serve(ctx, path)
	})
}
