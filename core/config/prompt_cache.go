package config

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The disk tier of the llama.cpp prompt cache keeps the KV state of long
// prompts on disk, so it survives a model reload. The backend writes the
// files under a root the node gives it (LOCALAI_PROMPT_CACHE_ROOT); the Go
// janitor (pkg/promptcache) keeps the root within its time to live, quotas
// and cap. See docs/content/models/disk-prompt-cache.md.

// The llama.cpp model options of the disk tier. The backend reads all but
// PromptCacheOptShared, which only Go reads.
const (
	PromptCacheOptDisk      = "prompt_cache_disk"
	PromptCacheOptTTLHours  = "prompt_cache_disk_ttl_hours"
	PromptCacheOptMinTokens = "prompt_cache_disk_min_tokens"
	PromptCacheOptQuotaMiB  = "prompt_cache_disk_quota_mib"
	PromptCacheOptMaxMiB    = "prompt_cache_disk_max_size_mib"
	PromptCacheOptShared    = "prompt_cache_disk_shared"
	PromptCacheOptLowFree   = "prompt_cache_disk_low_free_mib"
	PromptCacheOptCritFree  = "prompt_cache_disk_critical_free_mib"
)

// The environment of a backend process that carries the disk tier.
const (
	// EnvPromptCacheRoot is the root of the disk tier. Unset turns it off.
	EnvPromptCacheRoot = "LOCALAI_PROMPT_CACHE_ROOT"
	// EnvPromptCacheReadMBps is the measured cold read speed of the root.
	EnvPromptCacheReadMBps = "LOCALAI_PROMPT_CACHE_READ_MBPS"

	// The policy of the node, for every backend. A llama.cpp model option of
	// the same meaning wins over these in the llama.cpp backend.

	// EnvPromptCacheTTLHours is the time to live in whole hours, rounded up.
	EnvPromptCacheTTLHours = "LOCALAI_PROMPT_CACHE_TTL_HOURS"
	// EnvPromptCacheMinTokens is the shortest prompt written to disk.
	EnvPromptCacheMinTokens = "LOCALAI_PROMPT_CACHE_MIN_TOKENS"
	// EnvPromptCacheQuotaMiB is the effective quota of one user; 0 is none.
	EnvPromptCacheQuotaMiB = "LOCALAI_PROMPT_CACHE_QUOTA_MIB"
	// EnvPromptCacheMaxSizeMiB is the effective cap of the node. A backend
	// uses it as the cap of its own model directory.
	EnvPromptCacheMaxSizeMiB = "LOCALAI_PROMPT_CACHE_MAX_SIZE_MIB"
	// EnvPromptCacheLowFreeMiB overrides the low watermark; 0 is the
	// default rule.
	EnvPromptCacheLowFreeMiB = "LOCALAI_PROMPT_CACHE_LOW_FREE_MIB"
	// EnvPromptCacheCriticalFreeMiB overrides the critical watermark; 0 is
	// the default rule.
	EnvPromptCacheCriticalFreeMiB = "LOCALAI_PROMPT_CACHE_CRITICAL_FREE_MIB"
	// EnvPromptCacheMinReuse is how many requests must share a prefix
	// before it is written.
	EnvPromptCacheMinReuse = "LOCALAI_PROMPT_CACHE_MIN_REUSE"
	// EnvPromptCacheMaxCheckpoints is the most checkpoints of one entry.
	EnvPromptCacheMaxCheckpoints = "LOCALAI_PROMPT_CACHE_MAX_CHECKPOINTS"
	// EnvPromptCacheLoadOverheadMs is the fixed cost of one load in the gate
	// that chooses between a read and a recompute.
	EnvPromptCacheLoadOverheadMs = "LOCALAI_PROMPT_CACHE_LOAD_OVERHEAD_MS"
)

// PromptCacheEnvKeys are all the environment keys of the disk tier. A node
// with the tier off removes every one of them from a backend, so an
// inherited value cannot turn the tier on behind the switch.
var PromptCacheEnvKeys = []string{
	EnvPromptCacheRoot, EnvPromptCacheReadMBps,
	EnvPromptCacheTTLHours, EnvPromptCacheMinTokens, EnvPromptCacheQuotaMiB,
	EnvPromptCacheMaxSizeMiB, EnvPromptCacheLowFreeMiB, EnvPromptCacheCriticalFreeMiB,
	EnvPromptCacheMinReuse, EnvPromptCacheMaxCheckpoints, EnvPromptCacheLoadOverheadMs,
}

// Fixed values of the backend policy. They are not settings yet.
const (
	// PromptCacheMinReuse: a prefix is written once this many requests
	// share it, so a one-off prompt costs no disk write.
	PromptCacheMinReuse = 2
	// PromptCacheMaxCheckpoints is the most checkpoints kept per entry.
	PromptCacheMaxCheckpoints = 3
	// PromptCacheLoadOverheadMs is the fixed cost of one load, in ms.
	PromptCacheLoadOverheadMs = 50
)

// PromptCacheDirName is the directory of the disk tier under a data path.
const PromptCacheDirName = "prompt-cache"

const (
	// DefaultPromptCacheTTL is how long an entry is kept after its last use.
	DefaultPromptCacheTTL = 7 * 24 * time.Hour
	// DefaultPromptCacheMinTokens is the shortest prompt written to disk.
	DefaultPromptCacheMinTokens = 1024
	// DefaultPromptCacheMaxBytes caps the automatic size of the disk tier.
	DefaultPromptCacheMaxBytes = int64(20) << 30
	// promptCacheFreeShare is the share of the free disk the automatic size
	// may take: one tenth.
	promptCacheFreeShare = 10
)

// PromptCacheConfig is the global configuration of the disk tier.
type PromptCacheConfig struct {
	// DiskDisabled turns the disk tier off. Stored negated so the zero
	// value keeps it on.
	DiskDisabled bool
	// MaxSizeMiB caps the whole disk tier of this node. 0 is automatic: the
	// smaller of 20 GiB and a tenth of the free disk of the root at start.
	MaxSizeMiB int
	// TTL is how long an entry is kept after its last use.
	TTL time.Duration
	// UserQuotaMiB is the most one user (namespace) may keep. 0 is
	// automatic: an equal share of the cap when authentication is on and
	// has more than one user, else none. A negative value means none.
	UserQuotaMiB int
	// MinTokens is the shortest prompt written to disk.
	MinTokens int
	// Shared puts every user in one namespace, so users share entries.
	Shared bool
	// LowFreeMiB is the free space under which the tier pauses and shrinks.
	// 0 is the default rule: max(15% of the filesystem, 10 GiB).
	LowFreeMiB int
	// CriticalFreeMiB is the free space under which the tier is disabled.
	// 0 is the default rule: 5% of the filesystem. Never above the low one.
	CriticalFreeMiB int
}

// DefaultPromptCacheConfig is the configuration of an option-less run.
func DefaultPromptCacheConfig() PromptCacheConfig {
	return PromptCacheConfig{TTL: DefaultPromptCacheTTL, MinTokens: DefaultPromptCacheMinTokens}
}

// WithPromptCacheDisk turns the disk tier on or off.
func WithPromptCacheDisk(enabled bool) AppOption {
	return func(o *ApplicationConfig) { o.PromptCache.DiskDisabled = !enabled }
}

// WithPromptCacheDiskMaxSizeMiB sets the cap of the disk tier; 0 is automatic.
func WithPromptCacheDiskMaxSizeMiB(mib int) AppOption {
	return func(o *ApplicationConfig) { o.PromptCache.MaxSizeMiB = mib }
}

// WithPromptCacheDiskTTL sets the time to live of an entry.
func WithPromptCacheDiskTTL(ttl time.Duration) AppOption {
	return func(o *ApplicationConfig) {
		if ttl > 0 {
			o.PromptCache.TTL = ttl
		}
	}
}

// WithPromptCacheDiskUserQuotaMiB sets the quota of one user; 0 is
// automatic, a negative value means none.
func WithPromptCacheDiskUserQuotaMiB(mib int) AppOption {
	return func(o *ApplicationConfig) { o.PromptCache.UserQuotaMiB = mib }
}

// WithPromptCacheDiskMinTokens sets the shortest prompt written to disk.
func WithPromptCacheDiskMinTokens(n int) AppOption {
	return func(o *ApplicationConfig) {
		if n > 0 {
			o.PromptCache.MinTokens = n
		}
	}
}

// WithPromptCacheDiskShared puts every user in one namespace.
func WithPromptCacheDiskShared(shared bool) AppOption {
	return func(o *ApplicationConfig) { o.PromptCache.Shared = shared }
}

// WithPromptCacheDiskLowFreeMiB sets the low free-space watermark; 0 is the
// default rule.
func WithPromptCacheDiskLowFreeMiB(mib int) AppOption {
	return func(o *ApplicationConfig) { o.PromptCache.LowFreeMiB = max(mib, 0) }
}

// WithPromptCacheDiskCriticalFreeMiB sets the critical free-space watermark;
// 0 is the default rule.
func WithPromptCacheDiskCriticalFreeMiB(mib int) AppOption {
	return func(o *ApplicationConfig) { o.PromptCache.CriticalFreeMiB = max(mib, 0) }
}

// WithPromptCachePath sets the root of the disk tier; it wins over the data
// path. Empty keeps <DataPath>/prompt-cache.
func WithPromptCachePath(path string) AppOption {
	return func(o *ApplicationConfig) { o.PromptCachePath = path }
}

// PromptCacheRoot is the root of the disk tier of this server: the path the
// operator set, else <DataPath>/prompt-cache. It is empty (tier off) with
// neither.
func (o *ApplicationConfig) PromptCacheRoot() string {
	if o == nil {
		return ""
	}
	root, _ := ResolvePromptCacheRoot(o.PromptCachePath, o.DataPath, "", nil)
	return root
}

// Where a prompt cache root comes from, for the logs.
const (
	PromptCacheRootExplicit = "prompt-cache-path"
	PromptCacheRootDataPath = "data-path"
	PromptCacheRootDerived  = "models-path"
)

// noDataPath is the value of a --data-path that keeps nothing on disk.
const noDataPath = "-"

// ResolvePromptCacheRoot chooses the root of the disk tier of a node, and
// says where it came from. The order is:
//
//  1. explicit, the --prompt-cache-path of the operator;
//  2. <dataPath>/prompt-cache, unless dataPath is empty or "-" (none);
//  3. derived, the old root of a worker (<models path>/../data/prompt-cache),
//     only when derivedLocal says it is on a local filesystem. Next to models
//     on a network share it would land on that share, or on the ephemeral
//     root filesystem of a container. A nil derivedLocal refuses it.
//
// It returns an empty root, which turns the tier off, when none applies.
func ResolvePromptCacheRoot(explicit, dataPath, derived string, derivedLocal func(string) bool) (root, source string) {
	if explicit != "" {
		return filepath.Clean(explicit), PromptCacheRootExplicit
	}
	if dataPath != "" && dataPath != noDataPath {
		return filepath.Join(dataPath, PromptCacheDirName), PromptCacheRootDataPath
	}
	if derived != "" && derivedLocal != nil && derivedLocal(derived) {
		return filepath.Clean(derived), PromptCacheRootDerived
	}
	return "", ""
}

// PromptCacheTTLHours is a time to live in whole hours, rounded up; 0 or
// less is the default.
func PromptCacheTTLHours(ttl time.Duration) int {
	if ttl <= 0 {
		ttl = DefaultPromptCacheTTL
	}
	return int(math.Ceil(ttl.Hours()))
}

// PromptCacheMaxBytes is the cap of the disk tier: the configured size, else
// the smaller of 20 GiB and a tenth of freeBytes.
func PromptCacheMaxBytes(configuredMiB int, freeBytes uint64) int64 {
	if configuredMiB > 0 {
		return int64(configuredMiB) << 20
	}
	share := freeBytes / promptCacheFreeShare
	if share > uint64(DefaultPromptCacheMaxBytes) {
		return DefaultPromptCacheMaxBytes
	}
	return int64(share)
}

// PromptCacheQuotaBytes is the quota of one namespace: the configured quota,
// none for a negative value, and for 0 an equal share of maxBytes among the
// users when authentication is on and has more than one user.
func PromptCacheQuotaBytes(configuredMiB int, maxBytes int64, authOn bool, users int) int64 {
	switch {
	case configuredMiB > 0:
		return int64(configuredMiB) << 20
	case configuredMiB < 0:
		return 0
	case authOn && users > 1 && maxBytes > 0:
		return maxBytes / int64(users)
	}
	return 0
}

// parseOptionBool reads a boolean option value the way the llama.cpp backend
// does: true, 1, yes and on are true. A bare option name is true.
func parseOptionBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "true", "1", "yes", "on":
		return true
	}
	return false
}

// PromptCacheShared reports whether the model with these options uses the
// shared namespace: the global switch, or its own prompt_cache_disk_shared.
func PromptCacheShared(options []string, pc PromptCacheConfig) bool {
	if pc.Shared {
		return true
	}
	v, ok := backendOptionValue(options, PromptCacheOptShared)
	return ok && parseOptionBool(v)
}

// PromptCacheModelTTL is the time to live a model sets with
// prompt_cache_disk_ttl_hours, and false when it sets none.
func PromptCacheModelTTL(options []string) (time.Duration, bool) {
	v, ok := backendOptionValue(options, PromptCacheOptTTLHours)
	if !ok {
		return 0, false
	}
	h, err := strconv.ParseFloat(v, 64)
	if err != nil || h < 0 {
		return 0, false
	}
	return time.Duration(h * float64(time.Hour)), true
}

// WithPromptCacheDiskOptions adds the global values of the disk tier to the
// llama.cpp options of a model that does not set them. An option of the model
// always wins, so prompt_cache_disk:false in a model turns its tier off. Other
// backends get nothing: a backend that checks its options would refuse them.
// The result is a copy; options is not changed.
func WithPromptCacheDiskOptions(options []string, backend string, pc PromptCacheConfig) []string {
	if !UsesLlamaCppServingOptions(backend) {
		return options
	}
	out := slices.Clone(options)
	add := func(name, value string) {
		if !backendOptionSet(out, name) {
			out = append(out, name+":"+value)
		}
	}
	add(PromptCacheOptDisk, strconv.FormatBool(!pc.DiskDisabled))
	add(PromptCacheOptTTLHours, strconv.Itoa(PromptCacheTTLHours(pc.TTL)))
	minTokens := pc.MinTokens
	if minTokens <= 0 {
		minTokens = DefaultPromptCacheMinTokens
	}
	add(PromptCacheOptMinTokens, strconv.Itoa(minTokens))
	if pc.UserQuotaMiB > 0 {
		add(PromptCacheOptQuotaMiB, fmt.Sprint(pc.UserQuotaMiB))
	}
	if pc.LowFreeMiB > 0 {
		add(PromptCacheOptLowFree, fmt.Sprint(pc.LowFreeMiB))
	}
	if pc.CriticalFreeMiB > 0 {
		add(PromptCacheOptCritFree, fmt.Sprint(pc.CriticalFreeMiB))
	}
	return out
}
