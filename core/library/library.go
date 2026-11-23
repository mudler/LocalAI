// SPDX-License-Identifier: MIT

// Package library is the one list of library sources: the places LocalAI reads
// add-ons, backends and models from. The list lives in the source store of the
// add-on owner (see catalog.StorePath), so the server, the startup flags and
// `local-ai library` all work on the same file, with or without a running
// server.
package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
)

const (
	// DefaultModelSourceID and DefaultBackendSourceID name the shipped model
	// and backend gallery indexes as sources.
	DefaultModelSourceID   = "localai"
	DefaultBackendSourceID = "localai-backends"
)

// ModelSource says which model source a build reads by default: ModelSourceLibrary
// or ModelSourceGallery. The value is set at build time with
//
//	-ldflags "-X github.com/mudler/LocalAI/core/library.ModelSource=gallery"
//
// A build from master, and a plain `go build` or `go test`, leaves it
// unchanged, so it reads the model library. A tagged build reads the model
// gallery until the release workflow cuts over (see the cutover step in
// .github/workflows/release.yaml and MODEL_SOURCE in the Makefile).
var ModelSource = ModelSourceLibrary

const (
	// ModelSourceLibrary reads the model library (official-models) by default.
	ModelSourceLibrary = "library"
	// ModelSourceGallery reads the gallery named localai by default.
	ModelSourceGallery = "gallery"
)

// UseModelLibrary reports whether this build reads the model library by
// default. Only the exact value "gallery" selects the gallery, so a mistyped
// build value cannot leave a build with no model source.
func UseModelLibrary() bool { return ModelSource != ModelSourceGallery }

// BackendSource says which backend source a build reads by default:
// BackendSourceLibrary or BackendSourceGallery. It is set at build time like
// ModelSource, with
//
//	-ldflags "-X github.com/mudler/LocalAI/core/library.BackendSource=gallery"
//
// A build from master, and a plain `go build` or `go test`, leaves it
// unchanged, so it reads the backend library. A tagged build reads the backend
// gallery until the release workflow cuts over (see the cutover step in
// .github/workflows/release.yaml and LOCALAI_BACKEND_SOURCE in the Makefile).
var BackendSource = BackendSourceLibrary

const (
	// BackendSourceLibrary reads the backend library (official-backends) by
	// default.
	BackendSourceLibrary = "library"
	// BackendSourceGallery reads the gallery named localai-backends by default.
	BackendSourceGallery = "gallery"
)

// UseBackendLibrary reports whether this build reads the backend library by
// default. Only the exact value "gallery" selects the gallery, so a mistyped
// build value cannot leave a build with no backend source.
func UseBackendLibrary() bool { return BackendSource != BackendSourceGallery }

// Defaults returns the sources LocalAI ships: the official add-on library, the
// backend library, the model library, the model gallery index and the backend
// gallery index. The add-on library is left out when official is false. The
// backend library is left out when backendLibrary is false, and then the
// backend gallery is the backend source whatever the build says. A default can
// be removed; the removal is remembered.
//
// Both sources of a kind are shipped, but only one is enabled, the one
// ModelSource or BackendSource names, so every model and every backend is
// listed once. The other can be turned on with `local-ai library enable`.
func Defaults(official, backendLibrary bool) []catalog.Source {
	return defaults(official, backendLibrary, UseModelLibrary(), UseBackendLibrary())
}

// backendLibrarySource is the shipped backend library, enabled when the build
// reads it by default.
func backendLibrarySource(enabled bool) catalog.Source {
	lib := catalog.OfficialBackendLibrary()
	lib.Enabled = enabled
	return lib
}

// DefaultBackendLibrary is the backend library as this build ships it: enabled
// when the build reads the backend library by default, listed but off when it
// reads the backend gallery.
func DefaultBackendLibrary() catalog.Source { return backendLibrarySource(UseBackendLibrary()) }

func defaults(official, backendLibrary, modelLibrary, backendSourceLibrary bool) []catalog.Source {
	var out []catalog.Source
	if official {
		out = append(out, catalog.OfficialLibrary())
	}
	readBackendLibrary := backendLibrary && backendSourceLibrary
	if backendLibrary {
		out = append(out, backendLibrarySource(backendSourceLibrary))
	}
	lib := catalog.OfficialModelLibrary()
	lib.Enabled = modelLibrary
	gal := defaultGallery(catalog.KindModel, DefaultModelSourceID, "Official models", config.DefaultGalleriesJSON)
	gal.Enabled = !modelLibrary
	backends := defaultGallery(catalog.KindBackend, DefaultBackendSourceID, "Official backends", config.DefaultBackendGalleriesJSON)
	backends.Enabled = !readBackendLibrary
	out = append(out, lib, gal, backends)
	return out
}

// defaultGallery turns the first gallery of a shipped JSON list into a source.
// The lists are compile-time constants, so a parse error is a bug.
func defaultGallery(kind catalog.Kind, id, name, shipped string) catalog.Source {
	var g []config.Gallery
	if err := json.Unmarshal([]byte(shipped), &g); err != nil || len(g) == 0 {
		panic("invalid built-in gallery JSON for " + id)
	}
	return catalog.Source{ID: id, Name: name, URL: g[0].URL, Enabled: true, Kind: kind, Format: catalog.FormatGallery}
}

// Open opens the store under dataPath and adds the shipped defaults that have
// no record yet.
func Open(dataPath string, official, backendLibrary bool) (*catalog.Sources, error) {
	store, err := catalog.OpenStore(catalog.StorePath(dataPath))
	if err != nil {
		return nil, err
	}
	if _, err = store.SeedDefaults(Defaults(official, backendLibrary)...); err != nil {
		return nil, fmt.Errorf("library defaults: %w", err)
	}
	return store, nil
}

var idChars = regexp.MustCompile(`[^a-z0-9]+`)

// SourceID derives a source id from a name or, failing that, a URL. The id is
// lowercase letters, digits and dashes, and starts with a letter.
func SourceID(name, url string) string {
	base := strings.TrimSpace(name)
	if base == "" {
		base = url
		for _, p := range []string{"https://", "http://", "oci://", "file://", "github:"} {
			base = strings.TrimPrefix(base, p)
		}
	}
	id := strings.Trim(idChars.ReplaceAllString(strings.ToLower(base), "-"), "-")
	if id == "" || id[0] < 'a' || id[0] > 'z' {
		id = "src-" + id
	}
	id = strings.TrimRight(id, "-")
	if len(id) > 48 {
		id = strings.TrimRight(id[:48], "-")
	}
	return id
}

// freeID returns id, or id with a number when another record has it.
// ErrDuplicate is returned when a source of the same kind already lists the URL.
var ErrDuplicate = errors.New("already a library source")

func freeID(taken map[string]bool, id string) string {
	if !taken[id] {
		return id
	}
	for n := 2; ; n++ {
		if c := fmt.Sprintf("%s-%d", id, n); !taken[c] {
			return c
		}
	}
}

// Add adds a user source. When id is empty one is derived from the name or
// URL. A source with the same kind and URL is refused, so the same index is
// not listed twice.
func Add(store *catalog.Sources, kind catalog.Kind, url, id, name, format string) (catalog.Source, error) {
	return AddWithMirrors(store, kind, url, id, name, format, nil)
}

// AddWithMirrors is Add for a source that also lists mirrors.
func AddWithMirrors(store *catalog.Sources, kind catalog.Kind, url, id, name, format string, mirrors []string) (catalog.Source, error) {
	return AddWithPolicy(store, kind, url, id, name, format, mirrors, catalog.VerificationPolicy{})
}

// checkPolicy accepts the policy a caller may set when it adds a source: none,
// or an issuer with a signer on an oci:// add-on library. The signer is an
// exact identity, or an identity pattern together with a source repository.
// The signature is then required. A pattern alone is refused: a shared
// signing workflow matches it for every repository that calls the workflow,
// so an add-on install refuses it and the source would never verify. The
// messages name the fields of the REST request; the CLI flags have the same
// names with dashes.
func checkPolicy(kind catalog.Kind, url, format string, p catalog.VerificationPolicy) (catalog.VerificationPolicy, error) {
	issuer, identity := strings.TrimSpace(p.Issuer), strings.TrimSpace(p.Identity)
	pattern, repo := strings.TrimSpace(p.IdentityRegex), strings.TrimSpace(p.SourceRepository)
	if issuer == "" && identity == "" && pattern == "" && repo == "" && !p.RequireSignature {
		return catalog.VerificationPolicy{}, nil
	}
	if identity != "" && pattern != "" {
		return p, badRequest("set identity or identity_regex, not both")
	}
	if issuer == "" || (identity == "" && pattern == "") {
		return p, badRequest("issuer and identity (or identity_regex) must be set together")
	}
	if kind != catalog.KindAddon || format != "" || !catalog.IsOCIAddress(url) {
		return p, badRequest("issuer and identity apply to an oci:// add-on library only")
	}
	out := catalog.VerificationPolicy{RequireSignature: true, Issuer: issuer, Identity: identity, IdentityRegex: pattern, SourceRepository: repo}
	if !out.ValidForAddon() {
		return p, badRequest("identity_regex needs source_repository: a pattern alone admits a signature made for any repository that uses the same signing workflow")
	}
	if err := out.Check(); err != nil {
		return p, badRequest("%s", strings.TrimPrefix(err.Error(), catalog.ErrInvalid.Error()+": "))
	}
	return out, nil
}

// AddWithPolicy is AddWithMirrors for a source that pins its signer: an
// oci:// add-on library whose artifact must be signed by the given issuer and
// identity, or identity pattern and source repository. An empty policy adds the source as AddWithMirrors does.
func AddWithPolicy(store *catalog.Sources, kind catalog.Kind, url, id, name, format string, mirrors []string, p catalog.VerificationPolicy) (catalog.Source, error) {
	p, err := checkPolicy(kind, url, format, p)
	if err != nil {
		return catalog.Source{}, err
	}
	for _, e := range store.Entries() {
		if e.Kind == kind && e.URL == url && !e.IsTombstone() {
			return catalog.Source{}, fmt.Errorf("%w: %s is already a %s source (%s)", ErrDuplicate, url, kind, e.ID)
		}
	}
	if id == "" {
		taken := map[string]bool{}
		for _, e := range store.Entries() {
			taken[e.ID] = true
		}
		id = freeID(taken, SourceID(name, url))
	}
	if name == "" {
		name = id
	}
	src := catalog.Source{ID: id, Name: name, URL: url, Enabled: true, Kind: kind, Format: format, Mirrors: mirrors, Policy: p}
	if err := store.AddUser(src); err != nil {
		return catalog.Source{}, err
	}
	return src, nil
}
