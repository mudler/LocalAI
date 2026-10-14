// SPDX-License-Identifier: MIT
package updates

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Catalog is what the checker needs of the libraries.
type Catalog interface {
	Resolve(ctx context.Context, sourceID, addonID, version string) (catalog.Release, error)
	// Releases lists every release of an add-on in a library, any platform.
	Releases(ctx context.Context, sourceID, addonID string) ([]catalog.Release, error)
	// Refresh fetches the library again, through its scoped credential
	// reference. It is the only call that uses the network.
	Refresh(ctx context.Context, sourceID string) error
	// State is the outcome of the last fetch of the library.
	State(sourceID string) (catalog.RefreshState, bool)
	// Target is the platform an install must match.
	Target() (os, arch string)
}

// FromCatalog adapts the owner's catalog.
func FromCatalog(c catalog.Catalog) Catalog { return ownerCatalog{c} }

type ownerCatalog struct{ catalog.Catalog }

func (o ownerCatalog) Refresh(ctx context.Context, sourceID string) error {
	if o.Sources == nil {
		return catalog.ErrUnavailable
	}
	_, e := o.Sources.Refresh(ctx, sourceID)
	return e
}

func (o ownerCatalog) State(sourceID string) (catalog.RefreshState, bool) {
	if o.Sources == nil {
		return catalog.RefreshState{}, false
	}
	return o.Sources.State(sourceID)
}

func (o ownerCatalog) Target() (string, string) { return o.Catalog.Target() }

// Installation is one installed add-on as the owner's journal holds it.
type Installation struct {
	AddonID    string
	Generation uint64
	Receipt    packages.PackageReceipt
}

// Reader reads the installed generation of an add-on from the owner. It
// reports false when the add-on is not installed.
type Reader interface {
	Installation(ctx context.Context, addonID string) (Installation, bool, error)
}

// Config bounds the checker. Zero values take the defaults.
type Config struct {
	Host contract.HostCompatibility
	// TTL is how long a cached answer, and the library data under it, is
	// fresh. Default 15 minutes.
	TTL time.Duration
	// MaxEntries bounds the cache. Default 256.
	MaxEntries int
	// MinRefresh is the least time between two network refreshes of one
	// add-on and of one library. Default 15 seconds.
	MinRefresh time.Duration
	// Timeout bounds one check. Default 45 seconds.
	Timeout time.Duration
	// Now is the clock. Default time.Now.
	Now func() time.Time
}

// Result is the status of an evaluation and the release it resolved.
type Result struct {
	Status Status
	// Release is the resolved candidate, valid for update-available (and for
	// incompatible and integrity-error, for display).
	Release catalog.Release
}

type entry struct {
	status   Status
	storedAt time.Time
}

type flight struct {
	done chan struct{}
	st   Status
	err  error
}

// Checker is the owner's update check. Its cache is bounded and per add-on,
// and it is an observation: nothing in it is durable or authoritative on its
// own. An upgrade re-evaluates at execution.
type Checker struct {
	cfg    Config
	cat    Catalog
	reader Reader

	// base ends the work of every check in flight; wg waits for it, so
	// nothing runs or writes after Close returns.
	base   context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	closed   bool
	entries  map[string]*entry
	flights  map[string]*flight
	lastAddr map[string]time.Time // last explicit refresh per add-on
	lastLib  map[string]time.Time // last network refresh per library
}

// New returns a checker. The reader and the catalog are the owner's.
func New(cfg Config, c Catalog, r Reader) *Checker {
	if cfg.TTL <= 0 {
		cfg.TTL = 15 * time.Minute
	}
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 256
	}
	if cfg.MinRefresh <= 0 {
		cfg.MinRefresh = 15 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 45 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	base, cancel := context.WithCancel(context.Background())
	return &Checker{cfg: cfg, cat: c, reader: r, base: base, cancel: cancel, entries: map[string]*entry{}, flights: map[string]*flight{}, lastAddr: map[string]time.Time{}, lastLib: map[string]time.Time{}}
}

// Close ends every check in flight and waits for it. A check asked for after
// Close fails with ErrUnavailable. It is safe to call more than once.
func (c *Checker) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.cancel()
	c.wg.Wait()
}

// Check returns the update status of an installed add-on. Without refresh it
// answers from the cache while it is fresh and otherwise evaluates the library
// data the owner already holds; it never uses the network. With refresh it
// fetches the library again first, at most once per interval, and never applies
// anything. A check that cannot tell returns the check-failed status and a nil
// error: only a missing add-on or a canceled caller is an error.
func (c *Checker) Check(ctx context.Context, addonID string, refresh bool) (Status, error) {
	if e := ctx.Err(); e != nil {
		return Status{}, e
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return Status{}, ErrUnavailable
	}
	in, ok, e := c.reader.Installation(ctx, addonID)
	if e != nil {
		return Status{}, e
	}
	if !ok {
		return Status{}, ErrNotInstalled
	}
	now := c.cfg.Now()
	if !refresh {
		if st, ok := c.cached(in, now); ok && !st.Stale {
			return st, nil
		}
	}
	return c.run(ctx, in, refresh)
}

// Cached returns the stored answer for the installed package without any
// evaluation or network call. The bool is false when none is held for exactly
// this installed digest and generation: the answer of a replaced package is
// never shown for the new one.
func (c *Checker) Cached(addonID, digest string, generation uint64) (Status, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[addonID]
	if !ok || e.status.InstalledDigest != digest || e.status.Generation != generation {
		return Status{}, false
	}
	st := e.status
	if c.cfg.Now().Sub(e.storedAt) > c.cfg.TTL {
		st.Stale = true
	}
	return st, true
}

// Status reads the cached answer of an installed add-on, or unknown. It never
// evaluates and never uses the network.
func (c *Checker) Status(ctx context.Context, addonID string) (Status, error) {
	in, ok, e := c.reader.Installation(ctx, addonID)
	if e != nil {
		return Status{}, e
	}
	if !ok {
		return Status{}, ErrNotInstalled
	}
	if st, ok := c.Cached(addonID, in.Receipt.Digest, in.Generation); ok {
		return st, nil
	}
	return Status{AddonID: addonID, State: Unknown, Generation: in.Generation, InstalledVersion: in.Receipt.Manifest.Version, InstalledDigest: in.Receipt.Digest}, nil
}

// Forget drops the cached answer of an add-on, as an uninstall or a new
// generation does.
func (c *Checker) Forget(addonID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, addonID)
	delete(c.lastAddr, addonID)
}

func (c *Checker) cached(in Installation, now time.Time) (Status, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[in.AddonID]
	if !ok || e.status.InstalledDigest != in.Receipt.Digest || e.status.Generation != in.Generation {
		return Status{}, false
	}
	st := e.status
	if now.Sub(e.storedAt) > c.cfg.TTL {
		st.Stale = true
	}
	return st, true
}

// run evaluates once per add-on at a time: concurrent callers share the
// answer of the check in flight. The work does not depend on one caller's
// context, so a canceled caller cannot spoil the answer of the others; only
// Close or the timeout ends it.
func (c *Checker) run(ctx context.Context, in Installation, refresh bool) (Status, error) {
	// A refresh never joins a plain evaluation: it must reach the library.
	key := in.AddonID
	if refresh {
		key += "\x00refresh"
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return Status{}, ErrUnavailable
	}
	if f, ok := c.flights[key]; ok {
		c.mu.Unlock()
		return c.wait(ctx, f)
	}
	f := &flight{done: make(chan struct{})}
	c.flights[key] = f
	c.wg.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.wg.Done()
		work, cancel := context.WithTimeout(c.base, c.cfg.Timeout)
		defer cancel()
		f.st, f.err = c.evaluateAndStore(work, in, refresh)
		c.mu.Lock()
		delete(c.flights, key)
		c.mu.Unlock()
		close(f.done)
	}()
	return c.wait(ctx, f)
}

func (c *Checker) wait(ctx context.Context, f *flight) (Status, error) {
	select {
	case <-f.done:
		return f.st, f.err
	case <-ctx.Done():
		return Status{}, ctx.Err()
	}
}

func (c *Checker) evaluateAndStore(ctx context.Context, in Installation, refresh bool) (Status, error) {
	now := c.cfg.Now()
	throttled := false
	if refresh {
		src := in.Receipt.SourceID
		c.mu.Lock()
		recentAddon := now.Sub(c.lastAddr[in.AddonID]) < c.cfg.MinRefresh && !c.lastAddr[in.AddonID].IsZero()
		recentLib := now.Sub(c.lastLib[src]) < c.cfg.MinRefresh && !c.lastLib[src].IsZero()
		if recentAddon || recentLib {
			throttled = true
		} else {
			c.lastAddr[in.AddonID] = now
			c.lastLib[src] = now
			c.prune(c.lastAddr)
			c.prune(c.lastLib)
		}
		c.mu.Unlock()
		if !throttled && src != "" {
			// A failed fetch is not fatal: the evaluation below reads the
			// catalog's own state and reports the failure as a status.
			_ = c.cat.Refresh(ctx, src)
		}
	}
	res := c.Evaluate(ctx, in)
	st := res.Status
	st.Throttled = throttled
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[in.AddonID] = &entry{status: st, storedAt: c.cfg.Now()}
	for len(c.entries) > c.cfg.MaxEntries {
		oldest, at := "", time.Time{}
		for id, e := range c.entries {
			if oldest == "" || e.storedAt.Before(at) || (e.storedAt.Equal(at) && id < oldest) {
				oldest, at = id, e.storedAt
			}
		}
		delete(c.entries, oldest)
	}
	return st, nil
}

// prune keeps a time map bounded by dropping its oldest entries.
func (c *Checker) prune(m map[string]time.Time) {
	for len(m) > c.cfg.MaxEntries {
		oldest, at := "", time.Time{}
		for k, v := range m {
			if oldest == "" || v.Before(at) || (v.Equal(at) && k < oldest) {
				oldest, at = k, v
			}
		}
		delete(m, oldest)
	}
}

// Evaluate computes the status of an installed package against the library
// data the owner holds now. It uses no network and stores nothing. The
// lifecycle manager calls it at execution of an upgrade, so the candidate and
// its compatibility are decided again at the moment of the change.
func (c *Checker) Evaluate(ctx context.Context, in Installation) Result {
	r := in.Receipt
	now := c.cfg.Now()
	selection := r.Selection
	if selection == "" {
		selection = r.Manifest.Version
	}
	st := Status{
		AddonID: in.AddonID, State: CheckFailed, Generation: in.Generation,
		InstalledVersion: r.Manifest.Version, InstalledDigest: r.Digest,
		SourceID: r.SourceID, Channel: r.Channel, Selection: selection, CheckedAt: now,
	}
	failed := func(class string) Result {
		st.State, st.Error = CheckFailed, class
		c.stamp(&st, now)
		return Result{Status: st}
	}
	if r.SourceID == "" || r.Digest == "" {
		return failed(ClassSource)
	}
	releases, e := c.cat.Releases(ctx, r.SourceID, in.AddonID)
	if e != nil {
		return failed(classOf(e))
	}
	targetOS, targetArch := c.cat.Target()
	var res Result
	if isVersion(selection) {
		res = c.immutable(ctx, in, st, selection, releases, targetOS, targetArch)
	} else {
		res = c.moving(ctx, in, st, selection, releases)
	}
	c.stamp(&res.Status, now)
	return res
}

// stamp sets the time of the last successful fetch of the library and marks
// a status that rests on data older than the time to live.
func (c *Checker) stamp(st *Status, now time.Time) {
	if s, ok := c.cat.State(st.SourceID); ok {
		st.LastSuccess = s.LastSuccess
		if !s.LastSuccess.IsZero() && now.Sub(s.LastSuccess) > c.cfg.TTL {
			st.Stale = true
		}
	}
	st.Available = st.State == UpdateAvailable
}

func classOf(e error) string {
	switch {
	case errors.Is(e, context.Canceled), errors.Is(e, context.DeadlineExceeded):
		return ClassCanceled
	case errors.Is(e, catalog.ErrCredential):
		return ClassCredential
	case errors.Is(e, catalog.ErrRefresh):
		return ClassRefresh
	case errors.Is(e, catalog.ErrUnavailable):
		return ClassSource
	case errors.Is(e, catalog.ErrNotFound):
		return ClassNotListed
	case errors.Is(e, catalog.ErrInvalid):
		return ClassAmbiguous
	}
	return ClassInternal
}

// moving handles a moving channel (development, branch-*, latest): the
// resolved digest is the whole truth. The same digest is up to date and any
// other digest is an update, even when the version label did not change.
func (c *Checker) moving(ctx context.Context, in Installation, st Status, selection string, releases []catalog.Release) Result {
	rel, e := c.cat.Resolve(ctx, st.SourceID, in.AddonID, selection)
	if e != nil {
		if errors.Is(e, catalog.ErrNotFound) {
			// The channel may exist for another platform only.
			for _, r := range releases {
				if r.Channel == selection {
					st.State, st.Reasons = Incompatible, []string{ReasonPlatform}
					return Result{Status: st}
				}
			}
		}
		st.State, st.Error = CheckFailed, classOf(e)
		return Result{Status: st}
	}
	st.CandidateVersion, st.CandidateDigest = rel.Version, rel.Digest
	if rel.Digest == in.Receipt.Digest {
		st.State = UpToDate
		return Result{Status: st, Release: rel}
	}
	return c.judge(in, st, rel)
}

// judge decides between update-available and incompatible for a candidate.
func (c *Checker) judge(in Installation, st Status, rel catalog.Release) Result {
	if reasons := Compatibility(c.cfg.Host, in.Receipt, rel); len(reasons) > 0 {
		st.State, st.Reasons = Incompatible, reasons
		return Result{Status: st, Release: rel}
	}
	st.State = UpdateAvailable
	return Result{Status: st, Release: rel}
}

// immutable handles an add-on installed from a version label. Within the same
// major version, the highest compatible greater version is the candidate. The
// installed version listed with another digest is an integrity error: an
// immutable version was rewritten and nothing is offered.
func (c *Checker) immutable(ctx context.Context, in Installation, st Status, selection string, releases []catalog.Release, targetOS, targetArch string) Result {
	installed, ok := parseVersion(in.Receipt.Manifest.Version)
	if !ok {
		st.State, st.Error = CheckFailed, ClassInternal
		return Result{Status: st}
	}
	same := func(r catalog.Release) bool {
		return r.OS == targetOS && r.Arch == targetArch && r.Channel == in.Receipt.Channel && isVersion(r.Version)
	}
	matching := 0
	var candidates []catalog.Release
	for _, r := range releases {
		if !same(r) {
			continue
		}
		v, ok := parseVersion(r.Version)
		if !ok {
			continue
		}
		matching++
		if r.Version == in.Receipt.Manifest.Version {
			if r.Digest != in.Receipt.Digest {
				st.State, st.Reasons = IntegrityError, []string{ReasonDigestChanged}
				st.CandidateVersion, st.CandidateDigest = r.Version, r.Digest
				return Result{Status: st}
			}
			continue
		}
		// Compatible range: the same major version. A stable install is
		// never moved to a pre-release.
		if v.major != installed.major || v.compare(installed) <= 0 || (len(installed.pre) == 0 && len(v.pre) > 0) {
			continue
		}
		candidates = append(candidates, r)
	}
	if len(candidates) == 0 {
		// Nothing newer. When the library has no entry of this add-on for
		// this platform and channel, it cannot say the install is current.
		switch {
		case matching > 0:
			st.State = UpToDate
		case len(releases) > 0 && !hasAny(releases, targetOS, targetArch):
			st.State, st.Reasons = Incompatible, []string{ReasonPlatform}
		default:
			st.State, st.Error = CheckFailed, ClassNotListed
		}
		return Result{Status: st}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, _ := parseVersion(candidates[i].Version)
		b, _ := parseVersion(candidates[j].Version)
		return a.compare(b) > 0
	})
	var first *catalog.Release
	for i := range candidates {
		rel, e := c.cat.Resolve(ctx, st.SourceID, in.AddonID, candidates[i].Version)
		if e != nil {
			st.State, st.Error = CheckFailed, classOf(e)
			return Result{Status: st}
		}
		if first == nil {
			first = &rel
		}
		if len(Compatibility(c.cfg.Host, in.Receipt, rel)) == 0 {
			st.CandidateVersion, st.CandidateDigest = rel.Version, rel.Digest
			st.State = UpdateAvailable
			return Result{Status: st, Release: rel}
		}
	}
	st.CandidateVersion, st.CandidateDigest = first.Version, first.Digest
	return c.judge(in, st, *first)
}

func hasAny(releases []catalog.Release, targetOS, targetArch string) bool {
	return slices.ContainsFunc(releases, func(r catalog.Release) bool { return r.OS == targetOS && r.Arch == targetArch })
}

func accepts(r contract.VersionRange, v uint64) bool {
	return r.Min > 0 && r.Max >= r.Min && v >= r.Min && v <= r.Max
}

// Compatibility returns the reasons a release cannot replace an installed
// package on this host. An empty result means none was found by what the entry
// declares; the manifest is checked again when the package is staged.
func Compatibility(host contract.HostCompatibility, installed packages.PackageReceipt, rel catalog.Release) []string {
	var out []string
	if rel.OS != host.OS || rel.Arch != host.Arch {
		out = append(out, ReasonPlatform)
	}
	q := rel.Requires
	zero := contract.VersionRange{}
	if q.Host != zero && !accepts(q.Host, host.Host) {
		out = append(out, ReasonHost)
	}
	if q.API != zero && !accepts(q.API, host.API) {
		out = append(out, ReasonAPI)
	}
	if q.Protocol != zero && !accepts(q.Protocol, host.Protocol) {
		out = append(out, ReasonProtocol)
	}
	if q.SDK != zero && !accepts(q.SDK, host.SDK) {
		out = append(out, ReasonSDK)
	}
	// A candidate whose data format ends below the installed one could not
	// read what the installed package wrote.
	if q.DataFormat != zero && (!accepts(q.DataFormat, host.DataFormat) || q.DataFormat.Max < installed.Manifest.DataFormat.Max) {
		out = append(out, ReasonDataFormat)
	}
	return out
}
