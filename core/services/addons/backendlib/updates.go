// SPDX-License-Identifier: MIT

package backendlib

import (
	"context"
	"errors"
	"os"
	"sort"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
)

// Update says that the library lists a newer build of an installed library
// backend than the one on disk. It is information: nothing is installed until
// the administrator asks for it.
type Update struct {
	// Name is the backend name.
	Name string `json:"name"`
	// SourceID is the library the backend came from.
	SourceID string `json:"sourceId"`
	// InstalledVersion and InstalledDigest are what metadata.json pins.
	InstalledVersion string `json:"installedVersion,omitempty"`
	InstalledDigest  string `json:"installedDigest,omitempty"`
	// Version and Digest are the build the library lists now. Version is what
	// an install of the update names.
	Version string `json:"version"`
	Digest  string `json:"digest"`
	// Signed says the library requires a signature, so the install of the
	// update has a verifying step.
	Signed bool `json:"signed"`
}

// Updates compares each installed library backend with the library it came
// from. It reads the index the last refresh cached and does not use the
// network. A backend is reported only when the digest in its channel differs
// from the digest it pinned; a backend whose library is disabled, unreachable
// or no longer lists it is not reported, because there is nothing to compare.
// The channel is the one of the installed version. When the library no longer
// lists that version, the single channel of the entry is used; if there are
// several, the check says nothing rather than guess.
func (i *Installer) Updates(ctx context.Context) ([]Update, error) {
	backends, err := gallery.ListSystemBackends(i.SystemState)
	if errors.Is(err, os.ErrNotExist) {
		return []Update{}, nil
	}
	if err != nil {
		return nil, err
	}
	var target, arch string
	if i.Remote != nil {
		if target, arch, err = i.Remote.Target(ctx); err != nil {
			return nil, err
		}
	} else {
		target, arch = i.Catalog.Target()
	}
	var refs []Ref
	for name, b := range backends {
		if b.IsSystem || b.Metadata == nil || b.Metadata.Library == "" || b.Metadata.Digest == "" {
			continue
		}
		refs = append(refs, Ref{SourceID: b.Metadata.Library, Name: name})
	}
	var remote map[string][]catalog.Release
	if i.Remote != nil && len(refs) > 0 {
		if remote, err = i.Remote.Releases(ctx, refs); err != nil {
			return nil, err
		}
	}
	out := []Update{}
	for _, ref := range refs {
		b, name := backends[ref.Name], ref.Name
		var releases []catalog.Release
		if i.Remote != nil {
			releases = remote[RefKey(ref.SourceID, ref.Name)]
		} else if releases, err = i.Catalog.KindReleases(ctx, catalog.KindBackend, b.Metadata.Library, name); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		if u, ok := newer(b.Metadata, name, releases, target, arch); ok {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out, nil
}

func newer(have *gallery.BackendMetadata, name string, releases []catalog.Release, target, arch string) (Update, bool) {
	var mine []catalog.Release
	for _, r := range releases {
		if r.OS == target && r.Arch == arch && digestForm.MatchString(r.Digest) {
			mine = append(mine, r)
		}
	}
	channel, known := "", false
	for _, r := range mine {
		if r.Version == have.Version || r.Digest == have.Digest {
			channel, known = r.Channel, true
			break
		}
	}
	if !known {
		channels := map[string]bool{}
		for _, r := range mine {
			channels[r.Channel] = true
		}
		if len(channels) != 1 {
			return Update{}, false
		}
		for c := range channels {
			channel = c
		}
	}
	var latest []catalog.Release
	for _, r := range mine {
		if r.Channel == channel {
			latest = append(latest, r)
		}
	}
	// Two builds in one channel is a library that cannot name its latest.
	if len(latest) != 1 || latest[0].Digest == have.Digest {
		return Update{}, false
	}
	return Update{
		Name: name, SourceID: have.Library,
		InstalledVersion: have.Version, InstalledDigest: have.Digest,
		Version: latest[0].Version, Digest: latest[0].Digest, Signed: latest[0].Policy.RequireSignature,
	}, true
}
