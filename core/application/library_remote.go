// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/library"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// remoteError is a refusal of the owner, mapped back to the sentinel the
// owner's own routes would have seen. The message is the owner's.
type remoteError struct {
	sentinel error
	message  string
}

func (e *remoteError) Error() string        { return e.message }
func (e *remoteError) Is(target error) bool { return target == e.sentinel }

func fromLibraryError(le *gateway.LibraryError) error {
	switch le.Type {
	case gateway.LibraryNotFound:
		return &remoteError{catalog.ErrNotFound, le.Message}
	case gateway.LibraryExists:
		return &remoteError{catalog.ErrExists, le.Message}
	case gateway.LibraryDuplicate:
		return &remoteError{library.ErrDuplicate, le.Message}
	case gateway.LibraryInvalid:
		return &remoteError{catalog.ErrInvalid, le.Message}
	case gateway.LibraryBadRequest:
		return &library.BadRequestError{Message: le.Message}
	case libraryBackendInvalid:
		return &remoteError{backendlib.ErrInvalid, le.Message}
	case libraryBackendNoCapability:
		return &remoteError{backendlib.ErrNoCapability, le.Message}
	case libraryBackendIntegrity:
		return &remoteError{backendlib.ErrIntegrity, le.Message}
	case libraryCatalogUnavailable:
		return &remoteError{catalog.ErrUnavailable, le.Message}
	}
	return errors.New(le.Message)
}

// hostPlatform is the OS and architecture this process runs on, as the add-on
// configuration says, else as the runtime says.
func (a *Application) hostPlatform() (string, string) {
	h := a.applicationConfig.Addons.Host
	os, arch := h.OS, h.Arch
	if os == "" {
		os = runtime.GOOS
	}
	if arch == "" {
		arch = runtime.GOARCH
	}
	return os, arch
}

// ownerCarrier returns the carrier to the owner, or says why there is none.
func (a *Application) ownerCarrier() (*gateway.Carrier, error) {
	g := a.addonGateway
	if g == nil || g.Forward == nil {
		return nil, &gateway.UnavailableError{Reason: gateway.ReasonAddonsOff}
	}
	return g.Forward, nil
}

// libraryOwnerReady checks that the owner serves library calls and runs on the
// platform of this instance. The owner resolves entries for its own OS and
// architecture, so a frontend on another one would install the wrong image.
func (a *Application) libraryOwnerReady(ctx context.Context, c *gateway.Carrier) error {
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return err
	}
	if caps.Protocol < 2 || !caps.Supports("library") {
		return &gateway.UnavailableError{Reason: gateway.ReasonLibraryUnsupported}
	}
	if os, arch := a.hostPlatform(); caps.OS != os || caps.Arch != arch {
		return &gateway.UnavailableError{Reason: gateway.ReasonPlatformMismatch}
	}
	return nil
}

// libraryActor is the actor of a library call. An HTTP request brings its own
// (library.WithActor). Work with no request, such as the update check of a
// background loop, runs as the one local owner when the deployment chose that
// mode, and otherwise has no authority.
func (a *Application) libraryActor(ctx context.Context) (contract.Actor, error) {
	if actor, ok := library.ActorFrom(ctx); ok {
		return actor, nil
	}
	if a.AddonOwnershipMode() == auth.AddonModeLocalOwner {
		return contract.Actor{UserID: auth.AddonLocalOwnerID, CredentialID: auth.AddonLocalOwnerID, Mode: auth.AddonModeLocalOwner}, nil
	}
	return contract.Actor{}, gateway.ErrForbidden
}

// forwardLibrary sends one library call to the owner.
func (a *Application) forwardLibrary(ctx context.Context, call gateway.LibraryCall) (json.RawMessage, error) {
	c, err := a.ownerCarrier()
	if err != nil {
		return nil, err
	}
	actor, err := a.libraryActor(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.libraryOwnerReady(ctx, c); err != nil {
		return nil, err
	}
	raw, err := c.Library(ctx, actor, call)
	var le *gateway.LibraryError
	if errors.As(err, &le) {
		return nil, fromLibraryError(le)
	}
	return raw, err
}

// releasesCall sends a backend-releases call. A call with a request actor is a
// user-facing call and goes the usual way. A call with none is the background
// update check: it uses the read-only system call, which the owner accepts from
// this pinned frontend without an actor, so it works in named-user mode.
func (a *Application) releasesCall(ctx context.Context, call gateway.LibraryCall) (json.RawMessage, error) {
	if _, ok := library.ActorFrom(ctx); ok {
		return a.forwardLibrary(ctx, call)
	}
	c, err := a.ownerCarrier()
	if err != nil {
		return nil, err
	}
	if err := a.libraryOwnerReady(ctx, c); err != nil {
		return nil, err
	}
	if caps, err := c.Capabilities(ctx); err != nil {
		return nil, err
	} else if !caps.Supports(gateway.CallLibrarySystem) {
		return nil, &gateway.UnavailableError{Reason: gateway.ReasonLibraryUnsupported}
	}
	raw, err := c.LibrarySystem(ctx, call)
	var le *gateway.LibraryError
	if errors.As(err, &le) {
		return nil, fromLibraryError(le)
	}
	return raw, err
}

// remoteLibrary is the library.Admin of a frontend: it keeps no store and
// forwards every call to the owner, which is the only writer.
type remoteLibrary struct{ a *Application }

func call[T any](r remoteLibrary, ctx context.Context, c gateway.LibraryCall) (T, error) {
	var out T
	raw, err := r.a.forwardLibrary(ctx, c)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, gateway.ErrUnavailable
	}
	return out, nil
}

// changed refreshes the gallery lists after a change this frontend forwarded,
// so the frontend does not wait for the next poll to follow its own change.
func (r remoteLibrary) changed(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = r.a.RefreshLibraries(ctx)
}

func (r remoteLibrary) List(ctx context.Context, known string) (library.Listing, error) {
	return call[library.Listing](r, ctx, gateway.LibraryCall{Op: gateway.LibraryList, Known: known})
}

func (r remoteLibrary) Add(ctx context.Context, req library.AddRequest) (library.Info, error) {
	in, err := call[library.Info](r, ctx, gateway.LibraryCall{Op: gateway.LibraryAdd, Kind: req.Kind, URL: req.URL, ID: req.ID, Name: req.Name, Format: req.Format, Mirrors: req.Mirrors})
	if err == nil {
		r.changed(ctx)
	}
	return in, err
}

func (r remoteLibrary) SetMirrors(ctx context.Context, id string, mirrors []string) (*library.Info, error) {
	in, err := call[*library.Info](r, ctx, gateway.LibraryCall{Op: gateway.LibraryUpdate, ID: id, Mirrors: mirrors})
	if err == nil {
		r.changed(ctx)
	}
	return in, err
}

func (r remoteLibrary) SetEnabled(ctx context.Context, id string, enabled bool) (*library.Info, error) {
	op := gateway.LibraryDisable
	if enabled {
		op = gateway.LibraryEnable
	}
	in, err := call[*library.Info](r, ctx, gateway.LibraryCall{Op: op, ID: id})
	if err == nil {
		r.changed(ctx)
	}
	return in, err
}

func (r remoteLibrary) Remove(ctx context.Context, id string) (library.RemoveResult, error) {
	res, err := call[library.RemoveResult](r, ctx, gateway.LibraryCall{Op: gateway.LibraryRemove, ID: id})
	if err == nil {
		r.changed(ctx)
	}
	return res, err
}

// LibraryAdmin returns the administration of the library sources for this
// process: the owner's store on a combined or owner process, and the carrier to
// the owner on a frontend. A frontend with add-ons off has neither, and the
// routes answer unavailable before they get here.
func (a *Application) LibraryAdmin() library.Admin {
	if a.AddonFrontend() {
		return remoteLibrary{a}
	}
	return library.LocalAdmin{
		Store:    a.LibrarySources,
		Official: func() bool { return a.applicationConfig.OfficialLibraries },
		Applied:  a.ApplyLibraries,
	}
}

// remoteBackends is the owner of the libraries for the backend installer of a
// frontend: it resolves and pins on the owner, and the frontend installs.
type remoteBackends struct{ a *Application }

func (r remoteBackends) Resolve(ctx context.Context, sourceID, backend, version, capability string) (backendlib.Plan, error) {
	raw, err := r.a.forwardLibrary(ctx, gateway.LibraryCall{Op: gateway.LibraryBackendResolve, SourceID: sourceID, Backend: backend, Version: version, Capability: capability})
	if err != nil {
		return backendlib.Plan{}, err
	}
	var p backendlib.Plan
	if json.Unmarshal(raw, &p) != nil {
		return backendlib.Plan{}, gateway.ErrUnavailable
	}
	return p, nil
}

func (r remoteBackends) Releases(ctx context.Context, refs []backendlib.Ref) (map[string][]catalog.Release, error) {
	out := map[string][]catalog.Release{}
	// The call takes at most 256 backends.
	for len(refs) > 0 {
		n := min(len(refs), 256)
		c := gateway.LibraryCall{Op: gateway.LibraryBackendReleases}
		for _, ref := range refs[:n] {
			c.Refs = append(c.Refs, gateway.LibraryRef{SourceID: ref.SourceID, Name: ref.Name})
		}
		refs = refs[n:]
		raw, err := r.a.releasesCall(ctx, c)
		if err != nil {
			return nil, err
		}
		var part map[string][]catalog.Release
		if json.Unmarshal(raw, &part) != nil {
			return nil, gateway.ErrUnavailable
		}
		for k, v := range part {
			out[k] = v
		}
	}
	return out, nil
}

// Target is the platform the owner resolves for. forwardLibrary refuses a
// frontend on another platform, so the answer is this instance's own.
func (r remoteBackends) Target(ctx context.Context) (string, string, error) {
	c, err := r.a.ownerCarrier()
	if err != nil {
		return "", "", err
	}
	if err := r.a.libraryOwnerReady(ctx, c); err != nil {
		return "", "", err
	}
	os, arch := r.a.hostPlatform()
	return os, arch, nil
}
