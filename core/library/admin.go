// SPDX-License-Identifier: MIT
package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/xlog"
)

// ErrStoreUnavailable says the source store cannot be opened or reached.
var ErrStoreUnavailable = errors.New("the library sources are not available")

// BadRequestError is a request the admin refuses as invalid. Message is shown
// to the caller as written.
type BadRequestError struct{ Message string }

func (e *BadRequestError) Error() string { return e.Message }

func badRequest(format string, args ...any) error {
	return &BadRequestError{Message: fmt.Sprintf(format, args...)}
}

// Listing is the answer of List: every source with its state and the
// official-libraries switch of the process that owns the store.
type Listing struct {
	Sources           []Info `json:"sources"`
	OfficialLibraries bool   `json:"officialLibraries"`
	// Version identifies the settings and the source list as far as they
	// decide the galleries a server reads. A caller that holds a version asks
	// for the list again only when it changed.
	Version string `json:"version"`
	// Unchanged is true when the caller's version is the current one; Sources
	// is then empty.
	Unchanged bool `json:"unchanged,omitempty"`
}

// AddRequest is a source to add.
type AddRequest struct {
	Kind    string   `json:"kind"`
	URL     string   `json:"url"`
	ID      string   `json:"id,omitempty"`
	Name    string   `json:"name,omitempty"`
	Format  string   `json:"format,omitempty"`
	Mirrors []string `json:"mirrors,omitempty"`
}

// RemoveResult says what a removal did.
type RemoveResult struct {
	ID        string `json:"id"`
	Default   bool   `json:"default"`
	Tombstone bool   `json:"tombstone"`
}

// Admin is the administration of the library sources. The process that owns
// the store implements it on the store (LocalAdmin); a frontend implements it
// by forwarding to the owner. The HTTP routes work on this interface only, so
// they answer the same on both.
type Admin interface {
	// List returns the sources. A non-empty known that equals the current
	// version gives an Unchanged listing without sources.
	List(ctx context.Context, known string) (Listing, error)
	Add(ctx context.Context, req AddRequest) (Info, error)
	// SetMirrors replaces the mirrors of a source. A nil Info means the
	// change was stored but the source is not listed any more.
	SetMirrors(ctx context.Context, id string, mirrors []string) (*Info, error)
	SetEnabled(ctx context.Context, id string, enabled bool) (*Info, error)
	Remove(ctx context.Context, id string) (RemoveResult, error)
}

type actorKey struct{}

// WithActor returns a context that carries the actor of an HTTP request, for
// the Admin of a frontend, which forwards it to the owner.
func WithActor(ctx context.Context, a contract.Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

// ActorFrom returns the actor WithActor stored.
func ActorFrom(ctx context.Context) (contract.Actor, bool) {
	a, ok := ctx.Value(actorKey{}).(contract.Actor)
	return a, ok
}

// Version is the identity of a source list and the official-libraries switch.
// It covers what decides the galleries a server reads and leaves out the fetch
// state, which changes on every refresh.
func Version(infos []Info, official bool) string {
	type row struct {
		Kind                                 string
		ID, URL, Format                      string
		Enabled, Default, Removed, Suspended bool
		Mirrors                              []string
	}
	rows := make([]row, 0, len(infos))
	for _, in := range infos {
		rows = append(rows, row{string(in.Kind), in.ID, in.URL, in.Format, in.Enabled, in.Default, in.Removed, in.Suspended, in.Mirrors})
	}
	b, _ := json.Marshal(struct {
		Official bool
		Rows     []row
	}{official, rows})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

// CleanMirrors trims the mirrors and checks them as a gallery address is
// checked. An add-on source takes none.
func CleanMirrors(kind string, in []string) ([]string, error) {
	var out []string
	for _, m := range in {
		if m = strings.TrimSpace(m); m != "" {
			out = append(out, m)
		}
	}
	if len(out) > 0 && kind == string(catalog.KindAddon) {
		return nil, badRequest("an add-on source takes no mirrors")
	}
	if err := catalog.ValidateMirrors(out); err != nil {
		return nil, &BadRequestError{Message: err.Error()}
	}
	return out, nil
}

// plainLoopbackHTTP reports an http address of this machine.
func plainLoopbackHTTP(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// LocalAdmin is the Admin of the process that holds the store.
type LocalAdmin struct {
	// Store returns the one store of the running server.
	Store func() (*catalog.Sources, error)
	// Official says whether the official libraries are on.
	Official func() bool
	// Applied runs after a change, so the server reads the new state.
	Applied func(dropped ...string) error
}

func (a LocalAdmin) open() (*catalog.Sources, error) {
	store, err := a.Store()
	if err != nil {
		xlog.Warn("library sources are not available", "error", err)
		return nil, ErrStoreUnavailable
	}
	return store, nil
}

func (a LocalAdmin) applied(dropped ...string) {
	if a.Applied == nil {
		return
	}
	if err := a.Applied(dropped...); err != nil {
		xlog.Warn("could not apply the library sources", "error", err)
	}
}

func (a LocalAdmin) official() bool {
	if a.Official == nil {
		return true
	}
	return a.Official()
}

func info(store *catalog.Sources, id string) *Info {
	for _, in := range List(store) {
		if in.ID == id {
			return &in
		}
	}
	return nil
}

func (a LocalAdmin) List(_ context.Context, known string) (Listing, error) {
	store, err := a.open()
	if err != nil {
		return Listing{}, err
	}
	infos, official := List(store), a.official()
	v := Version(infos, official)
	if known != "" && known == v {
		return Listing{OfficialLibraries: official, Version: v, Unchanged: true}, nil
	}
	return Listing{Sources: infos, OfficialLibraries: official, Version: v}, nil
}

func (a LocalAdmin) Add(_ context.Context, req AddRequest) (Info, error) {
	req.URL, req.ID, req.Name = strings.TrimSpace(req.URL), strings.TrimSpace(req.ID), strings.TrimSpace(req.Name)
	switch req.Kind {
	case string(catalog.KindAddon), string(catalog.KindBackend), string(catalog.KindModel):
	default:
		return Info{}, badRequest("kind must be addon, backend or model")
	}
	format := ""
	switch req.Format {
	case "", "library":
	case "gallery":
		format = catalog.FormatGallery
	default:
		return Info{}, badRequest("format must be library or gallery")
	}
	if req.URL == "" {
		return Info{}, badRequest("url is required")
	}
	if req.Kind == string(catalog.KindAddon) && format == "" && plainLoopbackHTTP(req.URL) {
		// The owner drops every package that does not come over https, and a
		// browser session cannot turn that off, so such a library would be
		// stored and then never list anything.
		return Info{}, badRequest("an add-on library on this machine over plain http cannot be used: its packages are refused unless they come over https. Serve it over https")
	}
	mirrors, err := CleanMirrors(req.Kind, req.Mirrors)
	if err != nil {
		return Info{}, err
	}
	store, err := a.open()
	if err != nil {
		return Info{}, err
	}
	src, err := AddWithMirrors(store, catalog.Kind(req.Kind), req.URL, req.ID, req.Name, format, mirrors)
	if err != nil {
		return Info{}, err
	}
	a.applied()
	if in := info(store, src.ID); in != nil {
		return *in, nil
	}
	return Info{Kind: src.Kind, ID: src.ID, Name: src.Name, URL: src.URL, Enabled: true}, nil
}

func (a LocalAdmin) SetMirrors(_ context.Context, id string, mirrors []string) (*Info, error) {
	store, err := a.open()
	if err != nil {
		return nil, err
	}
	kind := ""
	if in := info(store, id); in != nil {
		kind = string(in.Kind)
	}
	clean, err := CleanMirrors(kind, mirrors)
	if err != nil {
		return nil, err
	}
	if err := store.SetMirrors(id, clean); err != nil {
		return nil, err
	}
	a.applied()
	return info(store, id), nil
}

func (a LocalAdmin) SetEnabled(_ context.Context, id string, enabled bool) (*Info, error) {
	store, err := a.open()
	if err != nil {
		return nil, err
	}
	if err := store.SetEnabled(id, enabled); err != nil {
		return nil, err
	}
	a.applied()
	return info(store, id), nil
}

func (a LocalAdmin) Remove(_ context.Context, id string) (RemoveResult, error) {
	store, err := a.open()
	if err != nil {
		return RemoveResult{}, err
	}
	url := URLOf(store, id)
	wasDefault, err := Remove(store, id)
	if err != nil {
		return RemoveResult{}, err
	}
	// A tombstone keeps its record, so only a deleted source is dropped by address.
	if wasDefault {
		a.applied()
	} else {
		a.applied(url)
	}
	return RemoveResult{ID: id, Default: wasDefault, Tombstone: wasDefault}, nil
}
