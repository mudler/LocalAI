// SPDX-License-Identifier: MIT
package catalog

import (
	"context"
	"errors"
	"runtime"
	"sort"
	"strings"
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
type Release struct {
	Channel string `json:"channel,omitempty"`
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
	targetOS, targetArch := c.TargetOS, c.TargetArch
	if targetOS == "" {
		targetOS = runtime.GOOS
	}
	if targetArch == "" {
		targetArch = runtime.GOARCH
	}
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
