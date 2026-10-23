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

// Installer installs library backends on the local node.
type Installer struct {
	Catalog     catalog.Catalog
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
	r, err := i.Catalog.ResolveKind(ctx, catalog.KindBackend, sourceID, backend, version)
	if err != nil {
		return err
	}
	if r.IsMeta() {
		// A meta backend installs nothing itself: the host picks the backend
		// that serves its capability and that one is installed, pinned and
		// verified like any other.
		if r, err = i.concrete(ctx, sourceID, r); err != nil {
			return err
		}
	}
	gb, err := i.plan(ctx, r)
	if err != nil {
		return err
	}
	if err := i.checkCollision(r); err != nil {
		return err
	}
	install := i.install
	if install == nil {
		install = gallery.InstallBackend
	}
	// With a verification policy the gallery install verifies the signature of
	// the pinned image before it fetches any layer, then applies the size limit.
	gb.Stage = progress.Step
	return install(ctx, i.SystemState, i.ModelLoader, gb, progress.Bytes, i.RequireIntegrity)
}

// concrete returns the release a meta backend names for this host, in the same
// library and the same channel.
func (i *Installer) concrete(ctx context.Context, sourceID string, meta catalog.Release) (catalog.Release, error) {
	if i.SystemState == nil || meta.Channel == "" {
		return catalog.Release{}, ErrInvalid
	}
	id := meta.Capabilities[i.SystemState.Capability(meta.Capabilities)]
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
func (i *Installer) checkCollision(r catalog.Release) error {
	backends, err := gallery.ListSystemBackends(i.SystemState)
	if errors.Is(err, os.ErrNotExist) {
		// No backends directory yet: nothing can collide.
		return nil
	}
	if err != nil {
		return err
	}
	have, ok := backends.Get(r.ID)
	if !ok || have.IsSystem {
		return nil
	}
	if have.Metadata == nil || have.Metadata.Library != r.SourceID {
		return fmt.Errorf("%w: %q", ErrCollision, r.ID)
	}
	return nil
}
