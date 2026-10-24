// SPDX-License-Identifier: MIT

// Package backendlib installs a backend from a library entry of kind backend.
//
// A library entry is one more way to see and install a backend. The backend
// gallery stays the source of truth and its install path is not changed: this
// package resolves the entry from the library the owner trusts, pins the image
// by digest, takes the signature policy from the library and then hands the
// install to the same code the gallery uses, so the backend ends up in the
// same directory with the same metadata plus the library it came from.
package backendlib

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/oci"
	"github.com/mudler/LocalAI/pkg/system"
)

var (
	// ErrCollision is returned when the name is already installed from another
	// origin. Nothing is changed.
	ErrCollision = errors.New("backend is already installed from another origin")
	// ErrIntegrity is returned when strict integrity mode needs a signature
	// policy and the library has none.
	ErrIntegrity = errors.New("strict integrity: the library does not require a signature")
	// ErrInvalid is returned for an entry that cannot be installed as given.
	ErrInvalid = errors.New("invalid backend library entry")
	// ErrNoCapability is returned when a meta backend has no backend for the
	// capability of this host.
	ErrNoCapability = errors.New("the meta backend has no backend for this host")
)

// Plan is what the owner of the libraries decides for one install: the entry
// resolved, a meta backend replaced by the backend that serves the capability
// of the asking host, and the image pinned by digest with the library's
// signature policy. Whoever installs it runs the gallery install on its own
// node and applies its own size limit and strict mode. A Plan crosses the
// carrier between a frontend and the owner.
type Plan struct {
	// Name and Library are the backend ID and the library it came from.
	Name    string `json:"name"`
	Library string `json:"library"`
	Alias   string `json:"alias,omitempty"`
	// URI and Mirrors name the image by digest.
	URI     string   `json:"uri"`
	Mirrors []string `json:"mirrors,omitempty"`
	Version string   `json:"version"`
	// SourceURL is the address of the library index.
	SourceURL string `json:"sourceUrl,omitempty"`
	// Verification is set when the library requires a signature.
	Verification *config.GalleryVerification `json:"verification,omitempty"`
}

// Ref names one library backend whose releases a caller asks for.
type Ref struct {
	SourceID string `json:"sourceId"`
	Name     string `json:"name"`
}

// Remote is the owner of the libraries, reached over the carrier. A frontend
// has no library index: it asks the owner to resolve an entry and to list the
// releases of the backends it has installed.
type Remote interface {
	// Resolve returns the plan of an entry. capability is the capability
	// the asking host reports, used to pick the backend of a meta backend.
	Resolve(ctx context.Context, sourceID, backend, version, capability string) (Plan, error)
	// Releases returns the releases of each ref, by "<source>/<name>".
	Releases(ctx context.Context, refs []Ref) (map[string][]catalog.Release, error)
	// Target is the OS and architecture the owner resolves for.
	Target(ctx context.Context) (string, string, error)
}

// RefKey is the key of a ref in the map that Remote.Releases returns.
func RefKey(sourceID, name string) string { return sourceID + "/" + name }

// Installer installs library backends on the local node.
type Installer struct {
	Catalog catalog.Catalog
	// Remote, when set, replaces Catalog: the owner resolves the entry and
	// this node installs it.
	Remote      Remote
	SystemState *system.SystemState
	ModelLoader *model.ModelLoader
	// MaxBytes bounds the compressed size of the image. Zero means the default
	// for the backend kind, catalog.DefaultMaxBytes(catalog.KindBackend).
	MaxBytes int64
	// RequireIntegrity is the strict mode of the gallery install
	// (--require-backend-integrity): an entry whose library does not require a
	// signature is refused.
	RequireIntegrity bool
	// install is the gallery install; tests replace it.
	install func(context.Context, *system.SystemState, *model.ModelLoader, *gallery.GalleryBackend, func(string, string, string, float64), bool) error
	// lookup resolves an image reference to the manifest digest for a platform.
	// It pins a mutable entry at install; tests replace it.
	lookup func(ctx context.Context, image, platform string) (string, error)
}

func (i *Installer) digestOf(ctx context.Context, image, platform string) (string, error) {
	if i.lookup != nil {
		return i.lookup(ctx, image, platform)
	}
	return oci.GetImageDigest(image, platform, nil, nil)
}

func (i *Installer) limit() int64 {
	if i.MaxBytes > 0 {
		return i.MaxBytes
	}
	return catalog.DefaultMaxBytes(catalog.KindBackend)
}

// The steps of an install, in the order they run. A step is reported when it
// starts. StepVerifying is reported only when the library requires a signature;
// the others always run.
const (
	StepResolving = "resolving"
	StepVerifying = downloader.StageVerifying
	StepSize      = downloader.StageSize
	StepFetching  = downloader.StageFetching
	StepWriting   = gallery.StageWriting
)

// Steps lists the steps of an install in order.
var Steps = []string{StepResolving, StepVerifying, StepSize, StepFetching, StepWriting}

// Progress receives what an install reports. Both fields are optional.
type Progress struct {
	// Step is called with the name of each step as it starts.
	Step func(step string)
	// Bytes is the byte progress of the image fetch, as the gallery reports it.
	Bytes func(fileName, current, total string, percentage float64)
}

func (p Progress) step(s string) {
	if p.Step != nil {
		p.Step(s)
	}
}

// Install resolves the backend entry and installs it. version is the version
// or the channel of the entry, as for an add-on.
func (i *Installer) Install(ctx context.Context, sourceID, backend, version string, progress Progress) error {
	progress.step(StepResolving)
	var plan Plan
	var err error
	if i.Remote != nil {
		capability := ""
		if i.SystemState != nil {
			capability = i.SystemState.ReportedCapability()
		}
		plan, err = i.Remote.Resolve(ctx, sourceID, backend, version, capability)
	} else {
		plan, err = i.Resolve(ctx, sourceID, backend, version, "")
	}
	if err != nil {
		return err
	}
	return i.installPlan(ctx, plan, progress)
}

// Resolve is the owner's half of an install: it resolves the entry, replaces a
// meta backend by the backend that serves capability, and pins the image. An
// empty capability means the capability of this host. It installs nothing and
// checks no collision, because the node that installs may be another one.
func (i *Installer) Resolve(ctx context.Context, sourceID, backend, version, capability string) (Plan, error) {
	r, err := i.Catalog.ResolveKind(ctx, catalog.KindBackend, sourceID, backend, version)
	if err != nil {
		return Plan{}, err
	}
	if r.IsMeta() {
		// A meta backend installs nothing itself: the host picks the backend
		// that serves its capability and that one is installed, pinned and
		// verified like any other.
		if r, err = i.concrete(ctx, sourceID, r, capability); err != nil {
			return Plan{}, err
		}
	}
	gb, err := i.plan(ctx, r)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Name: gb.Name, Library: gb.Library, Alias: gb.Alias, URI: gb.URI, Mirrors: gb.Mirrors, Version: gb.Version, SourceURL: gb.Gallery.URL, Verification: gb.Gallery.Verification}
	return p, nil
}

// installPlan is the node's half: collision check and the gallery install, with
// this node's strict mode and size limit.
func (i *Installer) installPlan(ctx context.Context, p Plan, progress Progress) error {
	if p.Name == "" || !strings.HasPrefix(p.URI, "oci://") {
		return ErrInvalid
	}
	if i.RequireIntegrity && p.Verification == nil {
		return fmt.Errorf("%w (library %q)", ErrIntegrity, p.Library)
	}
	if err := i.checkCollision(p.Name, p.Library); err != nil {
		return err
	}
	install := i.install
	if install == nil {
		install = gallery.InstallBackend
	}
	gb := &gallery.GalleryBackend{URI: p.URI, Version: p.Version, Alias: p.Alias, Library: p.Library, MaxBytes: i.limit(), Mirrors: p.Mirrors}
	gb.Name = p.Name
	gb.Gallery = config.Gallery{Name: "library:" + p.Library, URL: p.SourceURL, Verification: p.Verification}
	// With a verification policy the gallery install verifies the signature of
	// the pinned image before it fetches any layer, then applies the size limit.
	gb.Stage = progress.Step
	return install(ctx, i.SystemState, i.ModelLoader, gb, progress.Bytes, i.RequireIntegrity)
}

// concrete returns the release a meta backend names for a host that reports
// capability (this host when it is empty), in the same library and channel.
func (i *Installer) concrete(ctx context.Context, sourceID string, meta catalog.Release, capability string) (catalog.Release, error) {
	if meta.Channel == "" || (capability == "" && i.SystemState == nil) {
		return catalog.Release{}, ErrInvalid
	}
	var pick string
	if capability == "" {
		pick = i.SystemState.Capability(meta.Capabilities)
	} else {
		pick = system.PickCapability(capability, meta.Capabilities)
	}
	id := meta.Capabilities[pick]
	if id == "" || id == meta.ID {
		return catalog.Release{}, fmt.Errorf("%w: %q", ErrNoCapability, meta.ID)
	}
	r, err := i.Catalog.ResolveKind(ctx, catalog.KindBackend, sourceID, id, meta.Channel)
	if err != nil {
		return catalog.Release{}, fmt.Errorf("meta backend %q names %q: %w", meta.ID, id, err)
	}
	if r.IsMeta() {
		return catalog.Release{}, ErrInvalid
	}
	return r, nil
}

var digestForm = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// plan turns a trusted resolution into the backend the gallery install takes.
// The image is named by digest, never by tag.
//
// A mutable entry (a development image) has no digest in the library. Its tag
// is resolved to a digest here, at install, and the install then goes through
// the same pinned path: the signature is verified against that digest and the
// metadata records it, so an update check can compare it later.
func (i *Installer) plan(ctx context.Context, r catalog.Release) (*gallery.GalleryBackend, error) {
	if r.EntryKind() != catalog.KindBackend || !strings.HasPrefix(r.Artifact, "oci://") || (r.Mutable != (r.Digest == "")) || (!r.Mutable && !digestForm.MatchString(r.Digest)) {
		return nil, ErrInvalid
	}
	target, arch := i.Catalog.Target()
	if r.OS != target || r.Arch != arch {
		return nil, ErrInvalid
	}
	if i.RequireIntegrity && !r.Policy.RequireSignature {
		return nil, fmt.Errorf("%w (library %q)", ErrIntegrity, r.SourceID)
	}
	ref, err := name.ParseReference(strings.TrimPrefix(r.Artifact, "oci://"))
	if err != nil {
		return nil, ErrInvalid
	}
	digest, version := r.Digest, r.Version
	if r.Mutable {
		d, err := i.digestOf(ctx, ref.Name(), r.OS+"/"+r.Arch)
		if err != nil || !digestForm.MatchString(d) {
			return nil, fmt.Errorf("%w: resolving %s: %v", ErrInvalid, ref.Name(), err)
		}
		digest = d
		version = r.Version + "." + strings.TrimPrefix(d, "sha256:")[:12]
	}
	gb := &gallery.GalleryBackend{
		URI:      "oci://" + ref.Context().Digest(digest).Name(),
		Version:  version,
		Alias:    r.Alias,
		Library:  r.SourceID,
		MaxBytes: i.limit(),
	}
	// A mirror is a copy of the same image in another registry, so the digest
	// that pins the artifact pins the mirror too.
	for _, m := range r.Mirrors {
		mref, err := name.ParseReference(strings.TrimPrefix(m, "oci://"))
		if err != nil {
			return nil, ErrInvalid
		}
		gb.Mirrors = append(gb.Mirrors, "oci://"+mref.Context().Digest(digest).Name())
	}
	gb.Name = r.ID
	gb.Gallery = config.Gallery{Name: "library:" + r.SourceID, URL: r.SourceURL}
	if r.Policy.RequireSignature {
		gb.Gallery.Verification = &config.GalleryVerification{
			Issuer:        r.Policy.Issuer,
			Identity:      r.Policy.Identity,
			IdentityRegex: r.Policy.IdentityRegex,
		}
	}
	return gb, nil
}

// checkCollision refuses to replace a backend that did not come from this
// library: one installed from a gallery, from another library or by hand.
// Installing again from the same library is a reinstall.
func (i *Installer) checkCollision(name, library string) error {
	backends, err := gallery.ListSystemBackends(i.SystemState)
	if errors.Is(err, os.ErrNotExist) {
		// No backends directory yet: nothing can collide.
		return nil
	}
	if err != nil {
		return err
	}
	have, ok := backends.Get(name)
	if !ok || have.IsSystem {
		return nil
	}
	if have.Metadata == nil || have.Metadata.Library != library {
		return fmt.Errorf("%w: %q", ErrCollision, name)
	}
	return nil
}
