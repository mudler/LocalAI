// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mudler/LocalAI/core/library"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The error types of the backend verbs that the frontend maps back to the
// errors of backendlib. The source verbs use the types of the gateway.
const (
	libraryBackendInvalid      = "backend_invalid"
	libraryBackendNoCapability = "backend_no_capability"
	libraryBackendIntegrity    = "backend_integrity"
	libraryCatalogUnavailable  = "catalog_unavailable"
)

// libraryOwner serves the library calls of the carrier on the owner. It is the
// owner's only writer of the library sources: every change goes through the
// same LocalAdmin as the owner's own HTTP routes, and the same administrator
// check as the owner's other administrator-only routes runs before it.
type libraryOwner struct {
	admin     library.Admin
	adminOnly func(context.Context, contract.Actor) error
	installer func() *backendlib.Installer
}

// newLibraryOwner builds the owner's side. The sources are the owner's store
// and ApplyLibraries brings the owner's own galleries in line after a change.
func (a *Application) newLibraryOwner() *libraryOwner {
	return &libraryOwner{
		admin: library.LocalAdmin{
			Store:    a.LibrarySources,
			Official: func() bool { return a.applicationConfig.OfficialLibraries },
			Applied:  a.ApplyLibraries,
		},
		adminOnly: a.addonAdminCheck,
		installer: a.BackendLibrary,
	}
}

// Library runs one verb. The administrator check comes first; a missing check
// denies everything.
func (o *libraryOwner) Library(ctx context.Context, actor contract.Actor, call gateway.LibraryCall) (json.RawMessage, error) {
	if o.adminOnly == nil {
		return nil, gateway.ErrForbidden
	}
	if err := o.adminOnly(ctx, actor); err != nil {
		return nil, gateway.ClassifyCheck(err)
	}
	v, err := o.run(ctx, call)
	if err != nil {
		return nil, ownerLibraryError(err)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, gateway.ErrUnavailable
	}
	return raw, nil
}

func (o *libraryOwner) run(ctx context.Context, c gateway.LibraryCall) (any, error) {
	switch c.Op {
	case gateway.LibraryList:
		return o.admin.List(ctx, c.Known)
	case gateway.LibraryAdd:
		return o.admin.Add(ctx, library.AddRequest{Kind: c.Kind, URL: c.URL, ID: c.ID, Name: c.Name, Format: c.Format, Mirrors: c.Mirrors})
	case gateway.LibraryUpdate:
		return o.admin.SetMirrors(ctx, c.ID, c.Mirrors)
	case gateway.LibraryEnable:
		return o.admin.SetEnabled(ctx, c.ID, true)
	case gateway.LibraryDisable:
		return o.admin.SetEnabled(ctx, c.ID, false)
	case gateway.LibraryRemove:
		return o.admin.Remove(ctx, c.ID)
	case gateway.LibraryBackendResolve:
		in := o.installer()
		if in == nil {
			return nil, gateway.ErrUnavailable
		}
		if c.SourceID == "" || c.Backend == "" || c.Version == "" {
			return nil, &library.BadRequestError{Message: "sourceId, backend and version are required"}
		}
		return in.Resolve(ctx, c.SourceID, c.Backend, c.Version, c.Capability)
	case gateway.LibraryBackendReleases:
		in := o.installer()
		if in == nil {
			return nil, gateway.ErrUnavailable
		}
		out := map[string][]catalog.Release{}
		for _, ref := range c.Refs {
			// A backend whose library is off or does not list it has nothing to
			// compare, as in the local update check.
			rel, err := in.Catalog.KindReleases(ctx, catalog.KindBackend, ref.SourceID, ref.Name)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				continue
			}
			out[backendlib.RefKey(ref.SourceID, ref.Name)] = rel
		}
		return out, nil
	}
	return nil, gateway.ErrForbidden
}

// ownerLibraryError turns a failure of a verb into the owner's typed refusal,
// so the frontend answers with the status the owner's own routes give. An
// error that is not a decision of the owner passes through and is typed as
// unavailable by the carrier.
func ownerLibraryError(err error) error {
	var bad *library.BadRequestError
	switch {
	case errors.Is(err, library.ErrStoreUnavailable):
		return gateway.ErrUnavailable
	case errors.Is(err, gateway.ErrForbidden), errors.Is(err, gateway.ErrUnavailable):
		return err
	case errors.As(err, &bad):
		return &gateway.LibraryError{Type: gateway.LibraryBadRequest, Message: bad.Message}
	case errors.Is(err, catalog.ErrNotFound):
		return &gateway.LibraryError{Type: gateway.LibraryNotFound, Message: err.Error()}
	case errors.Is(err, library.ErrDuplicate):
		return &gateway.LibraryError{Type: gateway.LibraryDuplicate, Message: err.Error()}
	case errors.Is(err, catalog.ErrExists):
		return &gateway.LibraryError{Type: gateway.LibraryExists, Message: err.Error()}
	case errors.Is(err, catalog.ErrInvalid):
		return &gateway.LibraryError{Type: gateway.LibraryInvalid, Message: err.Error()}
	case errors.Is(err, backendlib.ErrNoCapability):
		return &gateway.LibraryError{Type: libraryBackendNoCapability, Message: err.Error()}
	case errors.Is(err, backendlib.ErrIntegrity):
		return &gateway.LibraryError{Type: libraryBackendIntegrity, Message: err.Error()}
	case errors.Is(err, backendlib.ErrInvalid):
		return &gateway.LibraryError{Type: libraryBackendInvalid, Message: err.Error()}
	case errors.Is(err, catalog.ErrUnavailable), errors.Is(err, catalog.ErrRefresh):
		return &gateway.LibraryError{Type: libraryCatalogUnavailable, Message: err.Error()}
	}
	return &gateway.LibraryError{Type: gateway.LibraryInternal, Message: "the library operation failed"}
}

// Snapshot is the owner's answer to the frontend's poll: the sources that
// decide the galleries and the official-libraries switch, without the fetch
// state, the policies or the credential references. It needs no actor: the
// frontend holds the pinned carrier identity, and the list is what its own
// gallery lists contain.
func (o *libraryOwner) Snapshot(ctx context.Context, known string) (json.RawMessage, error) {
	l, err := o.admin.List(ctx, known)
	if err != nil {
		return nil, gateway.ErrUnavailable
	}
	for i := range l.Sources {
		l.Sources[i].Refresh, l.Sources[i].Policy, l.Sources[i].AuthReference = nil, nil, ""
	}
	raw, err := json.Marshal(l)
	if err != nil {
		return nil, gateway.ErrUnavailable
	}
	return raw, nil
}
