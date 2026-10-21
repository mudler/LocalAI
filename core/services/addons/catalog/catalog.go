// SPDX-License-Identifier: MIT
package catalog

import (
	"context"
	"errors"
	"runtime"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var (
	ErrForbidden   = errors.New("source administration denied")
	ErrInvalid     = errors.New("invalid catalog configuration")
	ErrCredential  = errors.New("catalog credential unavailable")
	ErrRefresh     = errors.New("catalog refresh failed")
	ErrUnavailable = errors.New("catalog unavailable")
	ErrNotFound    = errors.New("catalog release not found")
)

type VerificationPolicy struct {
	RequireSignature bool   `json:"requireSignature"`
	Issuer           string `json:"issuer,omitempty"`
	Identity         string `json:"identity,omitempty"`
}

// EntryUI is optional display metadata a catalog may offer for an entry. It is
// a hint for browsing before install, never authority: the manifest inside the
// verified package decides what an installed addon shows and can open. Every
// field is plain text or a path inside the package, never a URL, so no entry
// can make the host fetch an image from the catalog's own servers.
type EntryUI struct {
	// Title and Description are the default-language text. Localized text is
	// part of the package manifest and reaches users from the installed addon.
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	// MainPage is the route the addon opens as an app, if it has one.
	MainPage string `json:"mainPage,omitempty"`
	// Icon is a path inside the package. The host serves an icon only after
	// the package is installed and verified.
	Icon string `json:"icon,omitempty"`
	// LaunchMode is absent or "always".
	LaunchMode string `json:"launchMode,omitempty"`
}

// Requirements is what an entry says its package needs from the host. Like
// EntryUI it is a hint for the owner's update check, never authority: the
// manifest inside the verified package is checked again when the package is
// staged. A zero range means the entry makes no claim; the SDK range is zero
// for a package without a UI. It is comparable on purpose.
type Requirements struct {
	Host       contract.VersionRange `json:"host,omitzero"`
	API        contract.VersionRange `json:"api,omitzero"`
	Protocol   contract.VersionRange `json:"protocol,omitzero"`
	DataFormat contract.VersionRange `json:"dataFormat,omitzero"`
	SDK        contract.VersionRange `json:"sdk,omitzero"`
}

type Release struct {
	Channel           string `json:"channel,omitempty"`
	AllowLoopbackHTTP bool   `json:"allowLoopbackHTTP,omitempty"`
	// Selection is stamped by Resolve, not trusted from remote metadata.
	Selection   string             `json:"selection,omitempty"`
	SourceID    string             `json:"sourceId,omitempty"`
	SourceURL   string             `json:"sourceUrl,omitempty"`
	AddonID     string             `json:"addonId"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Version     string             `json:"version"`
	Artifact    string             `json:"artifact"`
	Digest      string             `json:"digest"`
	OS          string             `json:"os"`
	Arch        string             `json:"arch"`
	Policy      VerificationPolicy `json:"policy"`
	// UI is the optional display metadata of the entry. It is comparable on
	// purpose: a release is compared with its trusted resolution at staging.
	UI EntryUI `json:"ui,omitzero"`
	// Requires is the optional compatibility hint of the entry.
	Requires Requirements `json:"requires,omitzero"`
}
type Query struct {
	Text, SourceID string
	Offset, Limit  int
}
type Page struct {
	Releases []Release `json:"releases"`
	Total    int       `json:"total"`
}
type Catalog struct {
	Sources *Sources
	// TargetOS and TargetArch are fixed by the owner, never by the forwarding
	// frontend. Empty fields default to the platform of this process.
	TargetOS, TargetArch string
}

// target is the platform an install must match: the owner's configured one, or
// the platform of this process. Search and Resolve both use it, so an entry
// that is listed is an entry that can be resolved.
func (c Catalog) target() (string, string) {
	targetOS, targetArch := c.TargetOS, c.TargetArch
	if targetOS == "" {
		targetOS = runtime.GOOS
	}
	if targetArch == "" {
		targetArch = runtime.GOARCH
	}
	return targetOS, targetArch
}

// Search lists the releases of the enabled libraries that match the target
// platform. A release for another platform is never listed, and Total counts
// the listed set.
func (c Catalog) Search(ctx context.Context, q Query) (Page, error) {
	if e := ctx.Err(); e != nil {
		return Page{}, e
	}
	if c.Sources == nil {
		return Page{}, ErrUnavailable
	}
	if q.Offset < 0 || q.Limit < 0 || q.Limit > 100 {
		return Page{}, ErrInvalid
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	targetOS, targetArch := c.target()
	s := c.Sources
	s.mu.Lock()
	defer s.mu.Unlock()
	all := []Release{}
	for id, src := range s.sources {
		if !src.Enabled || (q.SourceID != "" && q.SourceID != id) {
			continue
		}
		cache, ok := s.cache[id]
		if !ok {
			return Page{}, ErrUnavailable
		}
		if cache.failed {
			return Page{}, ErrRefresh
		}
		for _, r := range cache.releases {
			if r.OS != targetOS || r.Arch != targetArch {
				continue
			}
			if strings.Contains(strings.ToLower(r.Name+" "+r.AddonID+" "+r.Description), strings.ToLower(q.Text)) {
				all = append(all, r)
			}
		}
	}
	sort.Slice(all, func(i, j int) bool { return releaseKey(all[i]) < releaseKey(all[j]) })
	p := Page{Total: len(all), Releases: []Release{}}
	if q.Offset < len(all) {
		p.Releases = all[q.Offset:min(len(all), q.Offset+q.Limit)]
	}
	return p, nil
}
func releaseKey(r Release) string {
	return r.SourceID + "/" + r.AddonID + "/" + r.Version + "/" + r.OS + "/" + r.Arch
}
func (c Catalog) Resolve(ctx context.Context, sourceID, addonID, version string) (Release, error) {
	if e := ctx.Err(); e != nil {
		return Release{}, e
	}
	if c.Sources == nil {
		return Release{}, ErrUnavailable
	}
	s := c.Sources
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.sources[sourceID]
	if !ok || !src.Enabled {
		return Release{}, ErrUnavailable
	}
	cache, ok := s.cache[sourceID]
	if !ok {
		return Release{}, ErrUnavailable
	}
	if cache.failed {
		return Release{}, ErrRefresh
	}
	targetOS, targetArch := c.target()
	var found *Release
	for _, r := range cache.releases {
		if r.AddonID == addonID && (r.Version == version || (r.Channel != "" && r.Channel == version)) && r.OS == targetOS && r.Arch == targetArch {
			if found != nil {
				return Release{}, ErrInvalid
			}
			copy := r
			copy.Selection = version
			found = &copy
		}
	}
	if found == nil {
		return Release{}, ErrNotFound
	}
	return *found, nil
}

// Releases lists every release of one add-on in one library, for any platform.
// It is a read of the cache a refresh filled and never touches the network.
// The update check uses it to tell an entry that exists for another platform
// from one that does not exist at all. The order is stable.
func (c Catalog) Releases(ctx context.Context, sourceID, addonID string) ([]Release, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if c.Sources == nil {
		return nil, ErrUnavailable
	}
	s := c.Sources
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.sources[sourceID]
	if !ok || !src.Enabled {
		return nil, ErrUnavailable
	}
	cache, ok := s.cache[sourceID]
	if !ok {
		return nil, ErrUnavailable
	}
	if cache.failed {
		return nil, ErrRefresh
	}
	out := []Release{}
	for _, r := range cache.releases {
		if r.AddonID == addonID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return releaseKey(out[i]) < releaseKey(out[j]) })
	return out, nil
}

// Target is the platform an install must match.
func (c Catalog) Target() (string, string) { return c.target() }
