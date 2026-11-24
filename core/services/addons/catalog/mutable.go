// SPDX-License-Identifier: MIT
package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/mudler/LocalAI/pkg/oci"
)

// DigestLookup resolves an image reference to the manifest digest for a
// platform. Tests replace it; the default asks the registry.
type DigestLookup func(ctx context.Context, image, platform string) (string, error)

// Pinned is an entry artifact fixed to one digest.
type Pinned struct {
	// Ref is the parsed artifact of the entry.
	Ref name.Reference
	// Digest is the sha256 digest the install uses. It is the digest of the
	// entry, or for a mutable entry the digest its tag named at install.
	Digest string
	// Version is the version to record. A mutable entry gets the first 12 hex
	// digits of the digest appended, so an update check can tell two builds of
	// one tag apart.
	Version string
}

// ByDigest is the image reference of the pin: the repository of the artifact
// with the digest, never the tag.
func (p Pinned) ByDigest() string { return p.Ref.Context().Digest(p.Digest).Name() }

// Pin fixes an OCI entry to a digest. This is the one place where a mutable
// entry (a development image) is turned into a pinned one, for backends and
// for add-ons alike: the tag is resolved to a digest here, at install, and the
// caller then verifies the signature against Pinned.Digest and records it, so
// the install goes through the same path as a release entry.
//
// An entry that is not mutable needs a well-formed digest. A mutable entry has
// none. A nil lookup asks the registry. What differs by kind (the entry kind,
// the platform, the label of the install) stays with the caller.
func Pin(ctx context.Context, r Release, lookup DigestLookup) (Pinned, error) {
	if !strings.HasPrefix(r.Artifact, "oci://") || r.Mutable != (r.Digest == "") || (!r.Mutable && !digestPattern.MatchString(r.Digest)) {
		return Pinned{}, fmt.Errorf("entry %q: not an OCI artifact pinned by a digest or marked mutable", r.ID)
	}
	ref, err := name.ParseReference(strings.TrimPrefix(r.Artifact, "oci://"))
	if err != nil {
		return Pinned{}, fmt.Errorf("entry %q: %w", r.ID, err)
	}
	p := Pinned{Ref: ref, Digest: r.Digest, Version: r.Version}
	if !r.Mutable {
		return p, nil
	}
	if lookup == nil {
		lookup = func(_ context.Context, image, platform string) (string, error) {
			return oci.GetImageDigest(image, platform, nil, nil)
		}
	}
	d, err := lookup(ctx, ref.Name(), r.OS+"/"+r.Arch)
	if err != nil || !digestPattern.MatchString(d) {
		return Pinned{}, fmt.Errorf("resolving %s: %v", ref.Name(), err)
	}
	p.Digest = d
	p.Version = r.Version + "." + strings.TrimPrefix(d, "sha256:")[:12]
	return p, nil
}
