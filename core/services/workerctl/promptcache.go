package workerctl

import "encoding/json"

// The disk tier of the llama.cpp prompt cache in distributed mode. Each worker
// keeps its cache files on its own disk; the frontends store the settings and
// the purges once, in the database, and bring every worker up to date with
// promptcache.sync. promptcache.usage asks a worker what it holds.
//
// A worker that predates these verbs answers 404 on HTTP and nothing on NATS;
// the frontend reads both as a route that does not exist and tries again at
// its next pass.

// PromptCacheSettings are the cluster settings of the disk tier, as a worker
// applies them. They are the PromptCacheConfig of the frontends plus what the
// worker cannot learn by itself: whether authentication is on and how many
// users it has (the automatic per-user quota is a share of the cap).
type PromptCacheSettings struct {
	// Version names this set of values: a hash of every other field. The
	// frontend sends the settings again when the worker's recorded version
	// differs.
	Version         string `json:"version"`
	Disabled        bool   `json:"disabled,omitempty"`
	MaxSizeMiB      int    `json:"max_size_mib,omitempty"`
	TTLSeconds      int64  `json:"ttl_seconds,omitempty"`
	UserQuotaMiB    int    `json:"user_quota_mib,omitempty"`
	MinTokens       int    `json:"min_tokens,omitempty"`
	Shared          bool   `json:"shared,omitempty"`
	LowFreeMiB      int    `json:"low_free_mib,omitempty"`
	CriticalFreeMiB int    `json:"critical_free_mib,omitempty"`
	AuthOn          bool   `json:"auth_on,omitempty"`
	Users           int    `json:"users,omitempty"`
}

// PromptCachePurge is one purge of the purge log (a tombstone). Exactly one
// of All, Model16 and NSIDs is set.
type PromptCachePurge struct {
	// Seq orders the purges of the cluster. A worker applies a purge once.
	Seq     int64    `json:"seq"`
	All     bool     `json:"all,omitempty"`
	Model16 string   `json:"model16,omitempty"`
	NSIDs   []string `json:"nsids,omitempty"`
	// AgeMs is how long ago the purge was asked for, in milliseconds, not a
	// timestamp, so the clock of the worker does not matter. A purge of
	// everything or of a model deletes only the files last used before then:
	// a purge delivered late or twice never deletes newer entries. A purge
	// of namespaces ignores it and deletes every file of the namespaces.
	AgeMs int64 `json:"age_ms,omitempty"`
}

// PromptCacheSyncRequest brings a worker up to date: the settings in force
// and the purges it has not applied yet, oldest first.
type PromptCacheSyncRequest struct {
	Settings *PromptCacheSettings `json:"settings,omitempty"`
	Purges   []PromptCachePurge   `json:"purges,omitempty"`
}

// PromptCacheSyncReply is the answer of promptcache.sync.
type PromptCacheSyncReply struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	// Incarnation is the worker process that applied the request. A new
	// incarnation has lost the settings it was sent, which live in memory.
	Incarnation string `json:"incarnation,omitempty"`
	// SettingsVersion is the version the worker runs now.
	SettingsVersion string `json:"settings_version,omitempty"`
	// PurgedThrough is the highest Seq the worker has applied.
	PurgedThrough int64 `json:"purged_through,omitempty"`
	// Files and Bytes are what the purges of this request deleted.
	Files int   `json:"files,omitempty"`
	Bytes int64 `json:"bytes,omitempty"`
}

// PromptCacheUsageRequest asks a worker for its usage.
type PromptCacheUsageRequest struct{}

// PromptCacheUsageReply is the usage of the disk tier of one worker.
type PromptCacheUsageReply struct {
	// Enabled is false when the tier is off on the worker (no data path, the
	// cluster switch, or the switch of the worker).
	Enabled bool `json:"enabled"`
	// OffReason says why the tier is off on the worker: no_path,
	// switched_off, network_disk or not_writable. An older worker sends
	// none.
	OffReason string `json:"off_reason,omitempty"`
	// Usage is the pkg/promptcache NodeUsage of the worker, as it sends it.
	Usage json.RawMessage `json:"usage,omitempty"`
	Error string          `json:"error,omitempty"`
}
