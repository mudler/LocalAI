// Package promptcachesvc runs the disk tier of the llama.cpp prompt cache on
// a node: it chooses the root, measures its transfer speed, gives the backend
// processes their environment, and runs the janitor of pkg/promptcache on a
// timer. It also answers purge and usage requests through the Nodes seam.
//
// Distributed mode: each node owns its files. They live on its own local disk,
// its backend processes write them and its own janitor keeps them in bounds;
// a frontend never touches them. The frontends keep the settings and the
// purges once, in the database (Ledger), and bring every worker up to date
// over the node control channel (Distributed on the frontend, Worker on the
// worker). Local acts on the node that answers, for single mode.
package promptcachesvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/promptcache"
	pc "github.com/mudler/LocalAI/pkg/promptcache"
	"github.com/mudler/xlog"
)

// SweepInterval is the time between two janitor sweeps.
const SweepInterval = 5 * time.Minute

// Node is the disk tier of one node.
type Node struct {
	root       string
	modelsPath string
	// settings gives the live global settings. A worker without the
	// settings of the frontend uses the defaults.
	settings func() config.PromptCacheConfig
	// modelTTLs gives the time to live a model sets, by model16 id.
	modelTTLs func() map[string]time.Duration
	// users gives whether authentication is on and how many users it has,
	// for the automatic per-user quota.
	users func() (authOn bool, n int)

	store   pc.Store
	janitor *pc.Janitor
	stats   pc.StatProvider
	// writable checks that the node can write under the root.
	writable func(root string) error

	mu sync.Mutex
	// freeAtStart is the free space of the root at start; the automatic cap
	// is a tenth of it, at most 20 GiB.
	freeAtStart uint64
	measured    bool
	// offReason is set by CheckRoot when the root cannot hold the tier. It
	// turns the tier off on this node whatever the settings say.
	offReason string
}

// The reasons the tier is off on a node, in its usage report.
const (
	// OffNoPath: the node has no root (no data path and no
	// --prompt-cache-path).
	OffNoPath = "no_path"
	// OffSwitchedOff: the setting of the cluster or the switch of the node.
	OffSwitchedOff = "switched_off"
	// OffNetworkDisk: the root is on a network or FUSE filesystem.
	OffNetworkDisk = "network_disk"
	// OffNotWritable: the node cannot create or write the root.
	OffNotWritable = "not_writable"
)

// Option configures a Node.
type Option func(*Node)

// WithModelTTLs sets the source of per-model time to live overrides.
func WithModelTTLs(fn func() map[string]time.Duration) Option {
	return func(n *Node) { n.modelTTLs = fn }
}

// WithUsers sets the source of the user count for the automatic quota.
func WithUsers(fn func() (authOn bool, n int)) Option {
	return func(n *Node) { n.users = fn }
}

// WithStats replaces the filesystem reader, for tests.
func WithStats(s pc.StatProvider) Option {
	return func(n *Node) { n.stats = s }
}

// WithWritableCheck replaces the check that the root is writable, for tests.
func WithWritableCheck(fn func(root string) error) Option {
	return func(n *Node) { n.writable = fn }
}

// NewNode returns the disk tier rooted at root. An empty root means the node
// has no place for it (no data path): the tier is off. settings may be nil
// (defaults).
func NewNode(root, modelsPath string, settings func() config.PromptCacheConfig, opts ...Option) *Node {
	if settings == nil {
		settings = config.DefaultPromptCacheConfig
	}
	n := &Node{root: root, modelsPath: modelsPath, settings: settings, stats: pc.OSStats{}, writable: checkWritable}
	for _, o := range opts {
		o(n)
	}
	n.store = pc.NewDiskStore(root)
	n.janitor = &pc.Janitor{Store: n.store, Root: root, ModelsPath: modelsPath, Stats: n.stats}
	return n
}

// Root is the root of the tier, empty when the node has none.
func (n *Node) Root() string { return n.root }

// Enabled reports whether backends get the tier: a usable root and the
// global switch on.
func (n *Node) Enabled() bool {
	return n.OffReason() == ""
}

// OffReason says why the tier is off on this node (one of the Off
// constants), or is empty when it is on.
func (n *Node) OffReason() string {
	switch {
	case n == nil || n.root == "":
		return OffNoPath
	case n.rootOff() != "":
		return n.rootOff()
	case n.settings().DiskDisabled:
		return OffSwitchedOff
	}
	return ""
}

func (n *Node) rootOff() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.offReason
}

// CheckRoot checks once, at start, that the root can hold the tier: it must
// be on a local filesystem and writable. When it is not, the tier is off on
// this node, with one WARN that says how to set a local root. It returns the
// reason, empty when the root is fine. The transfer speed probe is not enough
// for this: a fast network share passes it, and then several nodes or a lost
// lock can corrupt the files.
func (n *Node) CheckRoot() string {
	if n == nil || n.root == "" {
		return ""
	}
	reason, detail := "", ""
	if n.stats != nil {
		if st, err := n.stats.Stat(pc.ExistingDir(n.root)); err == nil && st.Network {
			reason, detail = OffNetworkDisk, fmt.Sprintf("the directory is on a network filesystem (%s)", st.FSType)
		}
	}
	if reason == "" && n.writable != nil {
		if err := n.writable(n.root); err != nil {
			reason, detail = OffNotWritable, "the directory is not writable: "+err.Error()
		}
	}
	if reason == "" {
		return ""
	}
	n.mu.Lock()
	n.offReason = reason
	n.mu.Unlock()
	xlog.Warn("The disk prompt cache is off on this node: "+detail+
		". Set a directory on a local disk with --prompt-cache-path or LOCALAI_PROMPT_CACHE_PATH",
		"path", n.root)
	return reason
}

// checkWritable creates the root and writes and removes a file in it.
func checkWritable(root string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(root, ".write-check-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_, werr := f.Write([]byte{0})
	cerr := f.Close()
	rerr := os.Remove(name)
	return errors.Join(werr, cerr, rerr)
}

// BackendEnv is the environment the node sets on each backend process it
// starts: the root, its read speed, and the policy of the node from the live
// settings. These are the values of every backend; a llama.cpp model option
// still wins in the llama.cpp backend. With the tier off, every key is
// removed, so an inherited value cannot turn it on behind the switch.
func (n *Node) BackendEnv() map[string]string {
	env := make(map[string]string, len(config.PromptCacheEnvKeys))
	for _, k := range config.PromptCacheEnvKeys {
		env[k] = ""
	}
	if !n.Enabled() {
		return env
	}
	s := n.settings()
	p := n.Policy()
	env[config.EnvPromptCacheRoot] = n.root
	if mbps, ok := pc.TransferSpeed(n.root); ok {
		env[config.EnvPromptCacheReadMBps] = strconv.FormatFloat(mbps, 'f', 1, 64)
	}
	minTokens := s.MinTokens
	if minTokens <= 0 {
		minTokens = config.DefaultPromptCacheMinTokens
	}
	env[config.EnvPromptCacheTTLHours] = strconv.Itoa(config.PromptCacheTTLHours(s.TTL))
	env[config.EnvPromptCacheMinTokens] = strconv.Itoa(minTokens)
	env[config.EnvPromptCacheQuotaMiB] = strconv.FormatInt(mib(p.QuotaBytes), 10)
	env[config.EnvPromptCacheMaxSizeMiB] = strconv.FormatInt(mib(p.MaxBytes), 10)
	env[config.EnvPromptCacheLowFreeMiB] = strconv.Itoa(max(s.LowFreeMiB, 0))
	env[config.EnvPromptCacheCriticalFreeMiB] = strconv.Itoa(max(s.CriticalFreeMiB, 0))
	env[config.EnvPromptCacheMinReuse] = strconv.Itoa(config.PromptCacheMinReuse)
	env[config.EnvPromptCacheMaxCheckpoints] = strconv.Itoa(config.PromptCacheMaxCheckpoints)
	env[config.EnvPromptCacheLoadOverheadMs] = strconv.Itoa(config.PromptCacheLoadOverheadMs)
	return env
}

// mib is bytes in whole MiB. A positive value under 1 MiB is 1, so a small
// quota or cap never reads as none.
func mib(bytes int64) int64 {
	if bytes <= 0 {
		return 0
	}
	return max(bytes>>20, 1)
}

// Start measures the transfer speed in the background and runs the janitor at
// start and every SweepInterval until ctx ends. It never blocks: a backend
// that starts before the probe ends runs without a speed.
func (n *Node) Start(ctx context.Context) {
	if n.root == "" || n.rootOff() != "" {
		return
	}
	if n.Enabled() {
		n.startProbe()
	}
	go func() {
		t := time.NewTicker(SweepInterval)
		defer t.Stop()
		for {
			if n.Enabled() {
				// A switch turned on at runtime gets its probe here.
				n.startProbe()
			}
			if _, err := n.Sweep(ctx); err != nil {
				xlog.Warn("prompt cache janitor failed", "root", n.root, "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

func (n *Node) startProbe() {
	pc.StartTransferProbe(n.root, func(mbps float64, err error) {
		if err != nil {
			xlog.Info("prompt cache transfer speed unknown", "root", n.root, "error", err)
			return
		}
		xlog.Info("prompt cache transfer speed measured", "root", n.root, "mbps", mbps)
	})
}

// capBytes is the configured cap: the setting, else a tenth of the free disk
// at start, at most 20 GiB.
func (n *Node) capBytes(s config.PromptCacheConfig) int64 {
	n.mu.Lock()
	if !n.measured && n.stats != nil {
		if st, err := n.stats.Stat(pc.ExistingDir(n.root)); err == nil {
			n.freeAtStart = st.Free
			n.measured = true
		}
	}
	free := n.freeAtStart
	n.mu.Unlock()
	return config.PromptCacheMaxBytes(s.MaxSizeMiB, free)
}

// Policy is the janitor policy from the live settings.
func (n *Node) Policy() pc.Policy {
	s := n.settings()
	capBytes := n.capBytes(s)
	authOn, users := false, 0
	if n.users != nil {
		authOn, users = n.users()
	}
	p := pc.Policy{
		TTL:        s.TTL,
		MaxBytes:   capBytes,
		QuotaBytes: config.PromptCacheQuotaBytes(s.UserQuotaMiB, capBytes, authOn, users),
		// Same overrides the backend gets as model options.
		LowFreeBytes:      uint64(max(s.LowFreeMiB, 0)) << 20,
		CriticalFreeBytes: uint64(max(s.CriticalFreeMiB, 0)) << 20,
	}
	if p.TTL <= 0 {
		p.TTL = config.DefaultPromptCacheTTL
	}
	if n.modelTTLs != nil {
		p.ModelTTL = n.modelTTLs()
	}
	return p
}

// Sweep runs the janitor once.
func (n *Node) Sweep(ctx context.Context) (pc.Status, error) {
	if n.root == "" || n.rootOff() != "" {
		return pc.Status{}, nil
	}
	st, err := n.janitor.Sweep(ctx, n.Policy())
	if err == nil && st.Removed.Files > 0 {
		xlog.Debug("prompt cache janitor removed files", "root", n.root, "files", st.Removed.Files, "bytes", st.Removed.Bytes)
	}
	return st, err
}

// Usage reports what the node holds and the state of its disk.
//
// A root that CheckRoot refused is never read: the report carries only the
// reason.
func (n *Node) Usage(ctx context.Context) (pc.NodeUsage, error) {
	if n.root == "" || n.rootOff() != "" {
		return pc.NodeUsage{}, nil
	}
	return n.janitor.Usage(ctx)
}

// ErrNoScope means a purge names nothing to purge.
var ErrNoScope = errors.New("promptcachesvc: the purge names no scope")

// Scope is what a purge deletes: everything, the files of one model, or the
// files of some namespaces (by namespace id, see promptcache.NSID). Exactly
// one of the three is set.
type Scope struct {
	All     bool     `json:"all,omitempty"`
	Model16 string   `json:"model16,omitempty"`
	NSIDs   []string `json:"nsids,omitempty"`
	// Before, when set, bounds a purge of everything or of a model to the
	// files last used at or before it, so a purge that reaches a worker late
	// or twice never deletes newer entries. A purge of namespaces ignores it:
	// the namespaces of a removed principal go whole.
	Before time.Time `json:"-"`
}

// ModelScope is the scope of one model, from its model string.
func ModelScope(model string) Scope { return Scope{Model16: promptcache.ModelID(model)} }

// NamespaceScope is the scope of some namespaces, from their names. The
// shared namespace is never part of it: it belongs to no principal.
func NamespaceScope(namespaces ...string) Scope {
	s := Scope{}
	for _, ns := range namespaces {
		if ns == promptcache.Shared {
			continue
		}
		s.NSIDs = append(s.NSIDs, promptcache.NSID(promptcache.Salt(ns)))
	}
	return s
}

// Purge deletes the files of the scope on this node. A root that CheckRoot
// refused holds nothing of this node: the purge does nothing there, so a
// read-only root cannot block the purges of a worker.
func (n *Node) Purge(ctx context.Context, s Scope) (pc.Result, error) {
	if n.root == "" || n.rootOff() != "" {
		return pc.Result{}, nil
	}
	switch {
	case s.All && !s.Before.IsZero():
		return pc.PurgeAllBefore(ctx, n.store, s.Before)
	case s.All:
		return pc.PurgeAll(ctx, n.store)
	case s.Model16 != "" && !s.Before.IsZero():
		return pc.PurgeModelBefore(ctx, n.store, s.Model16, s.Before)
	case s.Model16 != "":
		return pc.PurgeModel(ctx, n.store, s.Model16)
	case len(s.NSIDs) > 0:
		var total pc.Result
		for _, id := range s.NSIDs {
			r, err := pc.PurgeNamespace(ctx, n.store, id)
			total.Files += r.Files
			total.Bytes += r.Bytes
			if err != nil {
				return total, err
			}
		}
		return total, nil
	}
	return pc.Result{}, ErrNoScope
}
