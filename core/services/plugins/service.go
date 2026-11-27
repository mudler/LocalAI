// Package plugins is the Plugins service: it installs plugins in the Claude
// Code plugin format from marketplaces, pinned by commit SHA, and keeps
// their records.
//
// Mode (see .agents/distributed-state.md):
//   - marketplaces, snapshots, packages, installs and user state: stateless
//     per request, rows in the store;
//   - package files: shared and immutable, in the workspace store by digest;
//   - the marketplace refresh and the package garbage collection: single
//     runner under advisorylock.KeyPluginsRefresh, one pass per tick;
//   - fetch scratch folders: per instance, under the data path;
//   - the inspect rate limit: per instance, in memory (ratelimit.go).
package plugins

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/mudler/nib/plugin/claudeplugin"

	"github.com/mudler/LocalAI/core/services/plugins/content"
	"github.com/mudler/LocalAI/core/services/plugins/fetch"
	"github.com/mudler/LocalAI/core/services/plugins/store"
)

// DefaultRefreshInterval is how often the marketplaces are refreshed.
const DefaultRefreshInterval = 6 * time.Hour

// Limits of the design (spec 15.4).
const (
	MaxMarketplaces       = 20
	MaxEntries            = 1000
	MaxUserInstalls       = 50
	MaxInstanceInstalls   = 200
	DefaultMarketplaceID  = "localai"
	curatedMarketplaceRef = ""
)

var (
	// ErrForbidden means the actor may not do this.
	ErrForbidden = errors.New("plugins: forbidden")
	// ErrNotFound means the marketplace, entry or install does not exist.
	ErrNotFound = errors.New("plugins: not found")
	// ErrInvalid means the request is not valid.
	ErrInvalid = errors.New("plugins: invalid request")
	// ErrLimit means a limit of spec 15.4 is reached.
	ErrLimit = errors.New("plugins: limit reached")
)

// Actor is who makes a request.
type Actor struct {
	// ID is the user ID, the principal of a user install.
	ID    string
	Admin bool
	// HumanSurface is set for a request from a browser session that carries
	// the confirmation header; an API key or an MCP call is not one.
	HumanSurface bool
}

// Fetcher fetches marketplaces and plugins. *fetch.Fetcher implements it;
// tests give a fake.
type Fetcher interface {
	FetchMarketplace(ctx context.Context, kind, location, ref string) (body []byte, sha string, err error)
	FetchPlugin(ctx context.Context, src fetch.MarketplaceSource, m *claudeplugin.Marketplace, e *claudeplugin.Entry, sha string) (*fetch.Fetched, error)
	ResolveRef(ctx context.Context, gitURL, ref string) (string, error)
}

// DefaultMarketplace is a marketplace that a fresh store gets at first
// start.
type DefaultMarketplace struct {
	ID       string
	Kind     string
	Location string
	Ref      string
}

// Options configure a Service.
type Options struct {
	Store store.Store
	// DB is the shared database. Nil for a server without one: it has one
	// frontend, and the single-runner work runs without a lock.
	DB      *gorm.DB
	Fetcher Fetcher
	// Workspace and Root hold the package files (files.PluginsPath).
	Workspace content.Workspace
	Root      string
	// Limits returns the tree limits, with the configured
	// plugins_max_bytes. Nil uses content.DefaultLimits.
	Limits func() content.Limits
	// Official reports the host-wide "official libraries" switch. Off
	// suspends the default marketplaces. Nil means on.
	Official func() bool
	// Defaults replace the curated marketplace at first start (the
	// --plugin-marketplaces flag). Nil seeds the curated marketplace.
	Defaults []DefaultMarketplace
	// Version is the build version, for the curated marketplace URL.
	Version         string
	RefreshInterval time.Duration
	Now             func() time.Time
	// Queue runs install, update and rollback jobs. Nil runs them at once.
	Queue Queue
	// UserInstalls reports the plugins_user_installs setting. Nil means on.
	UserInstalls func() bool
	// Connectors keeps the MCP connectors that plugins own. Nil: plugins
	// get no connectors.
	Connectors ConnectorSync
	// Allowed reports whether a principal holds the plugins feature now. A
	// principal without it gets no plugin in its turns, its own installs
	// included, so an administrator who turns the feature off for a user
	// stops the plugins of that user at once. Nil allows everyone.
	Allowed func(ctx context.Context, principal string) bool
}

// Service is the Plugins service. It keeps no state across requests but the
// inspect rate limit of this frontend.
type Service struct {
	st           store.Store
	db           *gorm.DB
	fetcher      Fetcher
	ws           content.Workspace
	root         string
	limits       func() content.Limits
	official     func() bool
	defaults     []DefaultMarketplace
	interval     time.Duration
	now          func() time.Time
	queue        Queue
	userInstalls func() bool
	conns        ConnectorSync
	inspects     *inspectLimiter
	allowed      func(ctx context.Context, principal string) bool
	// curated is set when the defaults are the curated marketplace (no
	// --plugin-marketplaces); version is the build version.
	curated bool
	version string
}

// New returns a Service.
func New(o Options) (*Service, error) {
	if o.Store == nil || o.Fetcher == nil {
		return nil, errors.New("plugins: a store and a fetcher are required")
	}
	s := &Service{
		st: o.Store, db: o.DB, fetcher: o.Fetcher, ws: o.Workspace, root: o.Root,
		limits: o.Limits, official: o.Official, interval: o.RefreshInterval, now: o.Now,
		queue: o.Queue, userInstalls: o.UserInstalls, conns: o.Connectors,
		inspects: newInspectLimiter(), allowed: o.Allowed,
	}
	if s.queue == nil {
		s.queue = inlineQueue{}
	}
	if s.userInstalls == nil {
		s.userInstalls = func() bool { return true }
	}
	if s.limits == nil {
		s.limits = content.DefaultLimits
	}
	if s.official == nil {
		s.official = func() bool { return true }
	}
	if s.interval <= 0 {
		s.interval = DefaultRefreshInterval
	}
	if s.now == nil {
		s.now = time.Now
	}
	s.defaults, s.version = o.Defaults, o.Version
	if s.defaults == nil {
		s.curated = true
		s.defaults = []DefaultMarketplace{{ID: DefaultMarketplaceID, Kind: store.KindURL, Location: CuratedMarketplaceURL(o.Version), Ref: curatedMarketplaceRef}}
	}
	return s, nil
}

// Store returns the record store.
func (s *Service) Store() store.Store { return s.st }
