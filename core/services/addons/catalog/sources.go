// SPDX-License-Identifier: MIT
package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/httpclient"
	"github.com/mudler/xlog"
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
	// Kind says what the source provides: addon, backend or model. An empty
	// kind is an add-on source, and the store writes it out.
	Kind Kind `json:"kind,omitempty"`
	// Format is empty for a library index (the shared entry shape), or
	// FormatGallery for a gallery index that the model and backend gallery
	// readers fetch. A gallery-format source is configuration only: this
	// catalog never fetches it.
	Format string `json:"format,omitempty"`
	// Mirrors are fallback addresses of a model or backend source, tried in
	// order when URL cannot be fetched. They mean what config.Gallery.Mirrors
	// means. A file written before this field existed has none.
	Mirrors []string `json:"mirrors,omitempty"`
	// Default marks a source the host ships. A default loses an entry to a
	// source the user added, and removing it leaves a tombstone.
	Default bool `json:"default,omitempty"`
	// Removed is a delete command. For a default source it is also persisted
	// as a tombstone, so the default is not added again at the next start; a
	// tombstone is never enabled and never listed by the management view.
	Removed bool `json:"removed,omitempty"`
	// Suspended is runtime state, never stored: the host-wide "official
	// libraries" switch is off and this is a shipped default. The record keeps
	// its own Enabled value, so turning the switch back on restores it.
	Suspended bool `json:"-"`
}

// FormatGallery marks a source whose index is a gallery index, as the
// --galleries and --backend-galleries flags name. See Source.Format.
const FormatGallery = "gallery"

// readByCatalog reports whether the add-on catalog fetches and lists this
// source. Model sources and gallery-format sources are read by the gallery
// readers instead.
func (s Source) readByCatalog() bool {
	return s.Enabled && !s.Removed && !s.Suspended && s.Kind != KindModel && s.Format != FormatGallery
}

// IsTombstone says the record only remembers that a default was removed.
func (s Source) IsTombstone() bool { return s.Default && s.Removed }

type RefreshResult struct {
	SourceID string `json:"sourceId"`
	Count    int    `json:"count"`
	// Skipped counts the entries left out because their kind is not one this
	// host reads, and SkippedKinds names those kinds. A library that serves a
	// newer kind still refreshes; this is how the owner learns of the gap.
	Skipped      int       `json:"skipped,omitempty"`
	SkippedKinds []string  `json:"skippedKinds,omitempty"`
	CheckedAt    time.Time `json:"checkedAt"`
	LastSuccess  time.Time `json:"lastSuccess"`
}

// RefreshState is what the owner last learned from fetching one library. It is
// an observation of the fetch, never of the library's trust.
type RefreshState struct {
	CheckedAt   time.Time `json:"checkedAt"`
	LastSuccess time.Time `json:"lastSuccess"`
	Count       int       `json:"count"`
	// Skipped and SkippedKinds are as in RefreshResult.
	Skipped      int      `json:"skipped,omitempty"`
	SkippedKinds []string `json:"skippedKinds,omitempty"`
	// Failed is true when the latest fetch failed; the previous releases are
	// not served in that case.
	Failed bool `json:"failed"`
}

// SourceView is a library as the management view shows it: the configuration
// and, when the owner has fetched it, the state of the last fetch.
type SourceView struct {
	Source
	Refresh *RefreshState `json:"refresh,omitempty"`
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
	// fresh is true when no configuration file existed at construction. Only
	// then may SeedDefault add a library; a file the admin already has, even an
	// empty one, is never touched.
	fresh bool
	// officialOff is the official-libraries switch: when set, every shipped
	// default is suspended (see Source.Suspended).
	officialOff bool
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[a-z][a-z0-9.-]*)?$`)

func validSource(s Source) bool {
	if !idPattern.MatchString(s.ID) || len(s.Name) == 0 || len(s.Name) > 256 || !validPolicy(s.Policy) || ValidateMirrors(s.Mirrors) != nil {
		return false
	}
	if s.Kind != "" && s.Kind != KindAddon && s.Kind != KindBackend && s.Kind != KindModel {
		return false
	}
	switch s.Format {
	case "":
		_, e := safeURL(s.URL)
		return e == nil
	case FormatGallery:
		// A gallery URL is anything the gallery loader understands: https://,
		// github:, file://, oci:// or a path. Only the shape is checked here.
		return validGalleryURL(s.URL) && s.Kind != KindAddon && s.Kind != ""
	}
	return false
}

// MaxMirrors is the most mirrors one source carries.
const MaxMirrors = 8

// ValidateMirrors checks the mirrors of a source the way a gallery address is
// checked: http(s), github:, oci:// or file://, with no spaces or control
// characters. The error names the first bad mirror.
func ValidateMirrors(mirrors []string) error {
	if len(mirrors) > MaxMirrors {
		return fmt.Errorf("a source takes at most %d mirrors", MaxMirrors)
	}
	for _, m := range mirrors {
		lower := strings.ToLower(m)
		ok := false
		for _, p := range []string{"http://", "https://", "github:", "oci://", "file://"} {
			if strings.HasPrefix(lower, p) {
				ok = true
			}
		}
		if !ok || !validGalleryURL(m) {
			return fmt.Errorf("mirror %q must be an http(s), github:, oci:// or file:// address", m)
		}
	}
	return nil
}

func validGalleryURL(u string) bool {
	if u == "" || len(u) > 2048 {
		return false
	}
	for _, r := range u {
		if r < 0x20 || r == 0x7f || r == ' ' {
			return false
		}
	}
	return true
}

// validPolicy accepts a policy that asks for no signature, or one with an
// issuer and exactly one identity: an exact string or an anchored pattern.
// Without a signature requirement the identity fields must be empty, so a
// policy never looks stricter than it is.
func validPolicy(p VerificationPolicy) bool {
	if !p.RequireSignature {
		return p.IdentityRegex == ""
	}
	if p.Issuer == "" || (p.Identity == "") == (p.IdentityRegex == "") {
		return false
	}
	if p.IdentityRegex == "" {
		return true
	}
	return anchoredPattern(p.IdentityRegex)
}

// anchoredPattern reports whether the pattern is one expression that must match
// the whole identity: a concatenation that starts at the beginning of the text
// and ends at its end. A top-level alternation such as "^a|b$" is refused,
// because one of its branches would match a part of the identity only.
func anchoredPattern(p string) bool {
	if len(p) > 512 {
		return false
	}
	re, e := syntax.Parse(p, syntax.Perl)
	if e != nil || re.Op != syntax.OpConcat || len(re.Sub) < 2 {
		return false
	}
	return re.Sub[0].Op == syntax.OpBeginText && re.Sub[len(re.Sub)-1].Op == syntax.OpEndText
}
func NewSources(path string, admin AdminCheck, credentials CredentialResolver) (*Sources, error) {
	s := &Sources{path: path, admin: admin, credentials: credentials, sources: map[string]Source{}, cache: map[string]cached{}}
	// On Windows, use native paths (backslashes) for os.Lstat
	if runtime.GOOS == "windows" {
		path = filepath.FromSlash(path)
	}
	info, e := os.Lstat(path)
	if os.IsNotExist(e) {
		s.fresh = true
		return s, nil
	}
	if e != nil || !info.Mode().IsRegular() {
		return nil, ErrInvalid
	}
	// On Windows, skip the Unix-style permission check since Windows
	// doesn't use Unix-style file permissions (0600). The file is
	// already protected by being inside the user's data directory.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, ErrInvalid
	}
	data, e := os.ReadFile(path) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
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
		if id != src.ID || (src.Removed && (!src.Default || src.Enabled)) || !validSource(src) {
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
		if !src.Removed {
			sources = append(sources, src)
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
	return sources, nil
}

// Views returns the sources with their refresh state after authoritative
// administrator validation.
func (s *Sources) Views(ctx context.Context, actor contract.Actor) ([]SourceView, error) {
	list, e := s.List(ctx, actor)
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SourceView, 0, len(list))
	for _, src := range list {
		v := SourceView{Source: src}
		if c, ok := s.cache[src.ID]; ok {
			v.Refresh = &RefreshState{CheckedAt: c.result.CheckedAt, LastSuccess: c.result.LastSuccess, Count: c.result.Count, Skipped: c.result.Skipped, SkippedKinds: c.result.SkippedKinds, Failed: c.failed}
		}
		out = append(out, v)
	}
	return out, nil
}

// HasReference reports whether the owner configured a credential reference of
// this name. A source may name only such a reference, so a secret pasted where
// a reference belongs is never stored or shown back.
func (s *Sources) HasReference(reference string) bool {
	_, ok := s.credentials.References[reference]
	return ok
}

// State is what the owner last learned from fetching one library, without an
// administrator check: it is for the owner's own update check, never for a
// request. The bool is false when the library was never fetched.
func (s *Sources) State(sourceID string) (RefreshState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cache[sourceID]
	if !ok {
		return RefreshState{}, false
	}
	return RefreshState{CheckedAt: c.result.CheckedAt, LastSuccess: c.result.LastSuccess, Count: c.result.Count, Skipped: c.result.Skipped, SkippedKinds: c.result.SkippedKinds, Failed: c.failed}, true
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
	if !ok || src.Removed {
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
	next, e := s.apply(src)
	if e != nil {
		return e
	}
	if e := s.persist(next); e != nil {
		return e
	}
	s.sources = next
	delete(s.cache, src.ID)
	return nil
}

// apply returns the configuration after a save or a delete of src. The caller
// holds s.mu. What marks a source as shipped or as gallery-format is never taken
// from the caller: a new source is a user source, and an existing one keeps
// its flags. Deleting a default leaves a tombstone.
func (s *Sources) apply(src Source) (map[string]Source, error) {
	next := map[string]Source{}
	for id, v := range s.sources {
		next[id] = v
	}
	old, had := s.sources[src.ID]
	if src.Removed {
		if !had || old.Removed {
			return next, nil
		}
		if old.Default {
			old.Enabled, old.Removed = false, true
			next[src.ID] = old
		} else {
			delete(next, src.ID)
		}
		return next, nil
	}
	src.Default, src.Format = false, ""
	if had {
		src.Suspended = old.Suspended
		src.Default, src.Format = old.Default, old.Format
		if src.Mirrors == nil {
			src.Mirrors = old.Mirrors
		}
		if src.Kind == "" {
			src.Kind = old.Kind
		}
	}
	if src.Kind == "" {
		src.Kind = KindAddon
	}
	if !validSource(src) {
		return nil, ErrInvalid
	}
	next[src.ID] = src
	return next, nil
}

// persist writes the whole configuration atomically. The caller holds s.mu.
func (s *Sources) persist(next map[string]Source) error {
	data, e := json.Marshal(next)
	if e != nil || len(data) > 1<<20 {
		return ErrInvalid
	}
	f, e := os.CreateTemp(filepath.Dir(s.path), ".sources-*")
	if e != nil {
		return ErrUnavailable
	}
	defer func() { _ = os.Remove(f.Name()) }()
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
	return nil
}

// SeedDefault adds a default library; see SeedDefaults.
func (s *Sources) SeedDefault(src Source) (bool, error) { return s.SeedDefaults(src) }

// SeedDefaults adds the libraries the host ships. It runs at every start and
// is additive: on the first start, when there is no configuration file, it
// writes them; on later starts it adds only a default that has no record yet.
// A default the admin removed has a tombstone, so it stays removed, and a
// library the admin edited is not reset. A configuration file that holds no shipped
// default (an admin wrote it before defaults were tracked) is left alone, as
// it always was.
// It reports whether it added a record. It is trusted startup code and takes
// no actor; it never runs for a request.
func (s *Sources) SeedDefaults(srcs ...Source) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	managed := s.fresh
	for _, v := range s.sources {
		if v.Default {
			managed = true
		}
	}
	if !managed {
		return false, nil
	}
	next := map[string]Source{}
	for id, v := range s.sources {
		next[id] = v
	}
	added := false
	for _, src := range srcs {
		src.Default, src.Removed, src.Suspended = true, false, s.officialOff
		if src.Kind == "" {
			src.Kind = KindAddon
		}
		if !idPattern.MatchString(src.ID) || !validSource(src) {
			return false, ErrInvalid
		}
		if _, ok := next[src.ID]; ok {
			continue
		}
		next[src.ID] = src
		added = true
	}
	if !added && !s.fresh {
		return false, nil
	}
	if e := s.persist(next); e != nil {
		return false, e
	}
	s.sources = next
	s.fresh = false
	return added, nil
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
	if !ok || !src.readByCatalog() {
		return RefreshResult{}, ErrUnavailable
	}
	old := s.cache[sourceID]
	result := RefreshResult{SourceID: sourceID, CheckedAt: time.Now().UTC(), LastSuccess: old.result.LastSuccess}
	releases, skipped, e := s.fetch(ctx, src)
	if e != nil {
		s.cache[sourceID] = cached{result: result, failed: true}
		if errors.Is(e, ErrCredential) {
			return result, e
		}
		return result, ErrRefresh
	}
	result.Count = len(releases)
	result.Skipped = len(skipped.Skipped)
	if result.Skipped > 0 {
		result.SkippedKinds = skipped.SkippedKinds()
		xlog.Warn("library index has entries of a kind this host does not read; they were skipped",
			"source", sourceID, "skipped", result.Skipped, "kinds", result.SkippedKinds)
	}
	result.LastSuccess = result.CheckedAt
	s.cache[sourceID] = cached{releases: releases, result: result}
	return result, nil
}
func (s *Sources) fetch(ctx context.Context, src Source) ([]Release, IndexResult, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if e != nil {
		return nil, IndexResult{}, ErrRefresh
	}
	if src.AuthReference != "" {
		c, e := s.credentials.Resolve(ctx, src.AuthReference, src.URL)
		if e == nil {
			e = c.ApplyHeaders(req.Header)
		}
		if e != nil {
			// A refresh that failed for want of a credential is still a
			// failed refresh; the class lets a caller say which one.
			return nil, IndexResult{}, fmt.Errorf("%w: %w", ErrRefresh, ErrCredential)
		}
	}
	response, e := httpclient.NewWithTimeout(30 * time.Second).Do(req)
	if e != nil {
		return nil, IndexResult{}, ErrRefresh
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, IndexResult{}, ErrRefresh
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, MaxIndexBytes+1))
	if e != nil || len(data) > MaxIndexBytes {
		return nil, IndexResult{}, ErrRefresh
	}
	indexed, e := ReadIndex(data, ReadableKinds...)
	releases := indexed.Releases
	if e != nil || releases == nil {
		return nil, IndexResult{}, ErrRefresh
	}
	seen := map[string]bool{}
	for i := range releases {
		r := &releases[i]
		if !validFetchedRelease(*r, src.AllowLoopbackHTTP) || !validKindFields(*r) || !validEntryUI(r.UI) || !validRequirements(r.Requires) || !validEntryTools(*r) {
			return nil, IndexResult{}, ErrRefresh
		}
		r.Selection = ""
		r.SourceID = src.ID
		r.SourceURL = src.URL
		r.AllowLoopbackHTTP = src.AllowLoopbackHTTP
		r.Policy = src.Policy
		key := releaseKey(*r)
		if seen[key] {
			return nil, IndexResult{}, ErrRefresh
		}
		seen[key] = true
	}
	return releases, indexed, nil
}

// backendNamePattern is a backend name: it is also a directory name, so it
// holds no separator and cannot start with a dot.
var backendNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

// validEntryID applies the identifier rule of the entry's kind.
func validEntryID(r Release) bool {
	if r.EntryKind() == KindBackend {
		return backendNamePattern.MatchString(r.ID) && !strings.Contains(r.ID, "..")
	}
	return contract.ValidAddonID(r.ID)
}

// validKindFields refuses fields that belong to another kind. A backend is an
// image for the backends directory: it has no main page or icon, and it is
// always an OCI image, pinned by digest. Only a backend may set an alias.
func validKindFields(r Release) bool {
	if r.EntryKind() != KindBackend {
		return r.Alias == "" && !r.hasBackendOnlyFields()
	}
	if !validBackendFields(r) {
		return false
	}
	if r.IsMeta() {
		// A meta backend has no image, so it has no UI path or requirements
		// either; its alias follows the same rule as any backend.
		return r.UI == (EntryUI{}) && r.Requires == (Requirements{}) && r.ToolCount == 0 && len(r.Tools) == 0 && (r.Alias == "" || (backendNamePattern.MatchString(r.Alias) && !strings.Contains(r.Alias, "..")))
	}
	if !strings.HasPrefix(r.Artifact, "oci://") || r.UI.MainPage != "" || r.UI.Icon != "" || r.UI.LaunchMode != "" || r.Requires != (Requirements{}) || r.ToolCount != 0 || len(r.Tools) > 0 {
		return false
	}
	return r.Alias == "" || (backendNamePattern.MatchString(r.Alias) && !strings.Contains(r.Alias, ".."))
}

// validEntryUI applies to catalog display hints the same rules the manifest
// applies to the real values, so a hint can never promise what install would
// refuse.
func validEntryUI(u EntryUI) bool {
	return (u.Title == "" || contract.ValidPlainText(u.Title, contract.MaxTitleChars)) &&
		(u.Description == "" || contract.ValidPlainText(u.Description, contract.MaxDescriptionChars)) &&
		(u.MainPage == "" || contract.ValidName(u.MainPage)) &&
		(u.Icon == "" || contract.ValidIconPath(u.Icon)) &&
		(u.LaunchMode == "" || u.LaunchMode == contract.LaunchModeAlways)
}

// validRequirements accepts a zero range (no claim) or a well formed one.
func validRequirements(q Requirements) bool {
	for _, r := range []contract.VersionRange{q.Host, q.API, q.Protocol, q.DataFormat, q.SDK} {
		if r != (contract.VersionRange{}) && (r.Min == 0 || r.Max < r.Min) {
			return false
		}
	}
	return true
}

var channelPattern = regexp.MustCompile(`^[a-z][a-z0-9/_-]{0,127}$`)

func validArtifactScheme(u *url.URL, allow bool) bool {
	if u.Scheme == "oci" || u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && allow && ip != nil && ip.IsLoopback()
}

// RefreshEnabled repopulates the non-durable owner cache on startup. One
// deadline bounds the whole batch; disabled sources never cause network I/O.
func (s *Sources) RefreshEnabled(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s.mu.Lock()
	ids := make([]string, 0, len(s.sources))
	for id, src := range s.sources {
		if src.readByCatalog() {
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	sort.Strings(ids)
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := s.Refresh(ctx, id); err != nil {
			return fmt.Errorf("refresh addon source %s: %w", id, err)
		}
	}
	return ctx.Err()
}
