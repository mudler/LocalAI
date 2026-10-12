// SPDX-License-Identifier: MIT
package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// AdminCheck must revalidate current identity, credential and administrator role
// against host authority. Actor fields alone never convey administration rights.
type AdminCheck func(context.Context, contract.Actor) error
type Source struct {
	// AllowLoopbackHTTP is an owner-only development opt-in for package downloads.
	AllowLoopbackHTTP bool               `json:"allowLoopbackHTTP,omitempty"`
	ID                string             `json:"id"`
	Name              string             `json:"name"`
	URL               string             `json:"url"`
	Enabled           bool               `json:"enabled"`
	AuthReference     string             `json:"authReference,omitempty"`
	Policy            VerificationPolicy `json:"policy"`
	// Removed is an explicit delete command; persisted records never contain it.
	Removed bool `json:"removed,omitempty"`
}
type RefreshResult struct {
	SourceID    string    `json:"sourceId"`
	Count       int       `json:"count"`
	CheckedAt   time.Time `json:"checkedAt"`
	LastSuccess time.Time `json:"lastSuccess"`
}
type cached struct {
	releases []Release
	result   RefreshResult
	failed   bool
}

// Sources is designated-owner state, not a replica-local fallback. Configuration
// must live in a protected owner directory; caches are deliberately not durable.
type Sources struct {
	mu          sync.Mutex
	path        string
	admin       AdminCheck
	credentials CredentialResolver
	sources     map[string]Source
	cache       map[string]cached
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[a-z][a-z0-9.-]*)?$`)

func validSource(s Source) bool {
	_, e := safeURL(s.URL)
	return idPattern.MatchString(s.ID) && len(s.Name) > 0 && len(s.Name) <= 256 && e == nil && (!s.Policy.RequireSignature || (s.Policy.Identity != "" && s.Policy.Issuer != ""))
}
func NewSources(path string, admin AdminCheck, credentials CredentialResolver) (*Sources, error) {
	s := &Sources{path: path, admin: admin, credentials: credentials, sources: map[string]Source{}, cache: map[string]cached{}}
	info, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return s, nil
	}
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, ErrInvalid
	}
	data, e := os.ReadFile(path)
	if e != nil || len(data) > 1<<20 {
		return nil, ErrInvalid
	}
	if e = decode(data, &s.sources); e != nil {
		return nil, ErrInvalid
	}
	if s.sources == nil {
		return nil, ErrInvalid
	}
	for id, src := range s.sources {
		if id != src.ID || src.Removed || !validSource(src) {
			return nil, ErrInvalid
		}
	}
	return s, nil
}

// List returns configuration independently of refresh state, including disabled
// and empty libraries. Source and Policy contain only values, not secret stores.
func (s *Sources) List(ctx context.Context, actor contract.Actor) ([]Source, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if s.admin == nil || s.admin(ctx, actor) != nil {
		return nil, ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sources := make([]Source, 0, len(s.sources))
	for _, src := range s.sources {
		sources = append(sources, src)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
	return sources, nil
}

// Get returns a value copy only after authoritative administrator validation.
func (s *Sources) Get(ctx context.Context, actor contract.Actor, sourceID string) (Source, error) {
	if e := ctx.Err(); e != nil {
		return Source{}, e
	}
	if s.admin == nil || s.admin(ctx, actor) != nil {
		return Source{}, ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.sources[sourceID]
	if !ok {
		return Source{}, ErrNotFound
	}
	return src, nil
}

func (s *Sources) Save(ctx context.Context, actor contract.Actor, src Source) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if s.admin == nil || s.admin(ctx, actor) != nil {
		return ErrForbidden
	}
	if !idPattern.MatchString(src.ID) || (!src.Removed && !validSource(src)) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := map[string]Source{}
	for id, v := range s.sources {
		next[id] = v
	}
	if src.Removed {
		delete(next, src.ID)
	} else {
		next[src.ID] = src
	}
	data, e := json.Marshal(next)
	if e != nil || len(data) > 1<<20 {
		return ErrInvalid
	}
	f, e := os.CreateTemp(filepath.Dir(s.path), ".sources-*")
	if e != nil {
		return ErrUnavailable
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(data); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil || closeErr != nil {
		return ErrUnavailable
	}
	if e = os.Rename(f.Name(), s.path); e != nil {
		return ErrUnavailable
	}
	s.sources = next
	delete(s.cache, src.ID)
	return nil
}
func decode(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}
func (s *Sources) Refresh(ctx context.Context, sourceID string) (RefreshResult, error) {
	// Serializing edits with refresh prevents publication under a replaced source
	// identity. The finite HTTP timeout bounds this owner-local critical section.
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.sources[sourceID]
	if !ok || !src.Enabled {
		return RefreshResult{}, ErrUnavailable
	}
	old := s.cache[sourceID]
	result := RefreshResult{SourceID: sourceID, CheckedAt: time.Now().UTC(), LastSuccess: old.result.LastSuccess}
	releases, e := s.fetch(ctx, src)
	if e != nil {
		s.cache[sourceID] = cached{result: result, failed: true}
		return result, ErrRefresh
	}
	result.Count = len(releases)
	result.LastSuccess = result.CheckedAt
	s.cache[sourceID] = cached{releases: releases, result: result}
	return result, nil
}
func (s *Sources) fetch(ctx context.Context, src Source) ([]Release, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if e != nil {
		return nil, ErrRefresh
	}
	if src.AuthReference != "" {
		c, e := s.credentials.Resolve(ctx, src.AuthReference, src.URL)
		if e != nil {
			return nil, ErrRefresh
		}
		if e = c.ApplyHeaders(req.Header); e != nil {
			return nil, ErrRefresh
		}
	}
	response, e := httpclient.NewWithTimeout(30 * time.Second).Do(req)
	if e != nil {
		return nil, ErrRefresh
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, ErrRefresh
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if e != nil || len(data) > 1<<20 {
		return nil, ErrRefresh
	}
	var releases []Release
	if e = decode(data, &releases); e != nil || releases == nil || len(releases) > 1000 {
		return nil, ErrRefresh
	}
	seen := map[string]bool{}
	for i := range releases {
		r := &releases[i]
		u, e := url.Parse(r.Artifact)
		if e != nil || !validArtifactScheme(u, src.AllowLoopbackHTTP) || u.Host == "" || u.Path == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !idPattern.MatchString(r.AddonID) || !versionPattern.MatchString(r.Version) || !digestPattern.MatchString(r.Digest) || r.Name == "" || !idPattern.MatchString(r.OS) || !idPattern.MatchString(r.Arch) {
			return nil, ErrRefresh
		}
		if r.Channel != "" && (!channelPattern.MatchString(r.Channel) || versionPattern.MatchString(r.Channel)) {
			return nil, ErrRefresh
		}
		r.Selection = ""
		r.SourceID = src.ID
		r.SourceURL = src.URL
		r.AllowLoopbackHTTP = src.AllowLoopbackHTTP
		r.Policy = src.Policy
		key := releaseKey(*r)
		if seen[key] {
			return nil, ErrRefresh
		}
		seen[key] = true
	}
	return releases, nil
}

var channelPattern = regexp.MustCompile(`^[a-z][a-z0-9/_-]{0,127}$`)

func validArtifactScheme(u *url.URL, allow bool) bool {
	if u.Scheme == "oci" || u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && allow && ip != nil && ip.IsLoopback()
}
