package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/vrambudget"
	"github.com/mudler/xlog"
)

// NodeLimits are the memory settings that one node may set apart from the
// cluster: by the worker's env at registration, or by an admin override. A
// nil field is not set at that level.
type NodeLimits struct {
	MaxActiveBackends        *int     `json:"max_active_backends,omitempty"`
	VRAMBudget               *string  `json:"vram_budget,omitempty"`
	MemoryReclaimerThreshold *float64 `json:"memory_reclaimer_threshold,omitempty"`
	ModelSwap                *bool    `json:"model_swap,omitempty"`
}

// IsEmpty reports whether l sets nothing.
func (l NodeLimits) IsEmpty() bool {
	return l.MaxActiveBackends == nil && l.VRAMBudget == nil && l.MemoryReclaimerThreshold == nil && l.ModelSwap == nil
}

// Validate checks the values an admin or a worker may set.
func (l NodeLimits) Validate() error {
	if l.MaxActiveBackends != nil && *l.MaxActiveBackends < 0 {
		return fmt.Errorf("max_active_backends must be 0 (no limit) or more, not %d", *l.MaxActiveBackends)
	}
	if l.MemoryReclaimerThreshold != nil {
		if v := *l.MemoryReclaimerThreshold; v <= 0 || v > 1 {
			return fmt.Errorf("memory_reclaimer_threshold must be in (0, 1], not %v", v)
		}
	}
	if l.VRAMBudget != nil && *l.VRAMBudget != "" {
		if _, err := vrambudget.Parse(*l.VRAMBudget); err != nil {
			return fmt.Errorf("vram_budget: %w", err)
		}
	}
	return nil
}

// merge sets on l every field that over sets.
func (l *NodeLimits) merge(over NodeLimits) {
	if over.MaxActiveBackends != nil {
		l.MaxActiveBackends = over.MaxActiveBackends
	}
	if over.VRAMBudget != nil {
		l.VRAMBudget = over.VRAMBudget
	}
	if over.MemoryReclaimerThreshold != nil {
		l.MemoryReclaimerThreshold = over.MemoryReclaimerThreshold
	}
	if over.ModelSwap != nil {
		l.ModelSwap = over.ModelSwap
	}
}

func decodeLimits(s string) NodeLimits {
	var l NodeLimits
	if s == "" {
		return l
	}
	if err := json.Unmarshal([]byte(s), &l); err != nil {
		xlog.Warn("Ignoring unreadable node limits", "error", err)
		return NodeLimits{}
	}
	return l
}

func encodeLimits(l NodeLimits) string {
	if l.IsEmpty() {
		return ""
	}
	b, err := json.Marshal(l)
	if err != nil {
		return ""
	}
	return string(b)
}

// WorkerLimits reads the limits the worker's env set, as stored at
// registration.
func (n *BackendNode) WorkerLimits() NodeLimits { return decodeLimits(n.LimitsWorker) }

// OverrideLimits reads the limits an admin set for this node. The VRAM budget
// override lives in the vram_budget columns (see VRAMBudgetManuallySet).
func (n *BackendNode) OverrideLimits() NodeLimits {
	l := decodeLimits(n.LimitsOverride)
	if n.VRAMBudgetManuallySet {
		b := n.VRAMBudget
		l.VRAMBudget = &b
	} else {
		l.VRAMBudget = nil
	}
	return l
}

// SetWorkerLimits stores the limits a worker reports at registration. An
// empty set is stored as "{}", so an empty column still tells a row written
// before node limits existed (see effectiveVRAMBudget).
func (n *BackendNode) SetWorkerLimits(l NodeLimits) {
	if s := encodeLimits(l); s != "" {
		n.LimitsWorker = s
		return
	}
	n.LimitsWorker = "{}"
}

// Where an effective limit comes from.
const (
	LimitSourceOverride = "override"
	LimitSourceWorker   = "worker"
	LimitSourceCluster  = "cluster"
	LimitSourceDefault  = "default"
)

// UnifiedMemoryReclaimerThreshold is the memory threshold of a node that
// reports unified memory while the cluster states no reclaimer setting: on
// such a node the GPU and the system share one pool, and running it full
// starves the host.
const UnifiedMemoryReclaimerThreshold = 0.90

// MemoryPolicy is the cluster's memory settings: the runtime settings of
// Settings > Models in memory as they apply to worker nodes.
type MemoryPolicy struct {
	MaxActiveBackends     int
	ModelSwap             bool
	SizeAwareEviction     bool
	ForceEvictionWhenBusy bool
	LRUMaxRetries         int
	LRURetryInterval      time.Duration
	IdleEnabled           bool
	IdleTimeout           time.Duration
	ReclaimerEnabled      bool
	ReclaimerThreshold    float64
	// ReclaimerStated is true when the cluster settings name the reclaimer
	// (enabled or threshold). Otherwise a unified-memory node reclaims at
	// UnifiedMemoryReclaimerThreshold.
	ReclaimerStated bool
	VRAMBudget      string
}

// MemoryPolicyFromConfig reads the policy from the runtime settings in
// force. stated lists the setting keys the cluster store holds.
func MemoryPolicyFromConfig(cfg *config.ApplicationConfig, stated []string) MemoryPolicy {
	p := MemoryPolicy{
		MaxActiveBackends:     cfg.GetEffectiveMaxActiveBackends(),
		ModelSwap:             cfg.ModelSwapEnabled(),
		SizeAwareEviction:     cfg.SizeAwareEviction,
		ForceEvictionWhenBusy: cfg.ForceEvictionWhenBusy,
		LRUMaxRetries:         cfg.LRUEvictionMaxRetries,
		LRURetryInterval:      cfg.LRUEvictionRetryInterval,
		IdleEnabled:           cfg.WatchDog && cfg.WatchDogIdle,
		IdleTimeout:           cfg.WatchDogIdleTimeout,
		ReclaimerEnabled:      cfg.MemoryReclaimerEnabled,
		ReclaimerThreshold:    cfg.MemoryReclaimerThreshold,
		VRAMBudget:            cfg.VRAMBudget,
	}
	for _, k := range stated {
		if k == "memory_reclaimer_enabled" || k == "memory_reclaimer_threshold" {
			p.ReclaimerStated = true
		}
	}
	return p
}

// MemoryPolicySource returns the cluster memory policy in force now. It is
// read on each decision, so a settings change applies at the next one.
type MemoryPolicySource func() MemoryPolicy

// DefaultMemoryPolicy is the policy of an option-less frontend: no model
// limit, swap on, the LRU wait of a default run.
func DefaultMemoryPolicy() MemoryPolicy {
	return MemoryPolicyFromConfig(config.DefaultRuntimeBaseline(), nil)
}

// NodePolicy is the memory policy one node runs with, and where each
// per-node value comes from.
type NodePolicy struct {
	MemoryPolicy
	Sources map[string]string
}

// EffectiveLimits is the per-node part of a NodePolicy, as the API and the
// heartbeat reply show it.
type EffectiveLimits struct {
	MaxActiveBackends        int               `json:"max_active_backends"`
	VRAMBudget               string            `json:"vram_budget"`
	MemoryReclaimerEnabled   bool              `json:"memory_reclaimer_enabled"`
	MemoryReclaimerThreshold float64           `json:"memory_reclaimer_threshold"`
	ModelSwap                bool              `json:"model_swap"`
	Sources                  map[string]string `json:"sources"`
}

// Limits returns the per-node values of p.
func (p NodePolicy) Limits() EffectiveLimits {
	return EffectiveLimits{
		MaxActiveBackends:        p.MaxActiveBackends,
		VRAMBudget:               p.VRAMBudget,
		MemoryReclaimerEnabled:   p.ReclaimerEnabled,
		MemoryReclaimerThreshold: p.ReclaimerThreshold,
		ModelSwap:                p.ModelSwap,
		Sources:                  p.Sources,
	}
}

// For resolves the policy of node n. Precedence, highest first: the admin
// override of the node, the worker's env, the cluster value, the default.
func (p MemoryPolicy) For(n *BackendNode) NodePolicy {
	out := NodePolicy{MemoryPolicy: p, Sources: map[string]string{
		"max_active_backends":        LimitSourceCluster,
		"vram_budget":                LimitSourceCluster,
		"memory_reclaimer_threshold": LimitSourceCluster,
		"model_swap":                 LimitSourceCluster,
	}}
	if n == nil {
		return out
	}
	if !p.ReclaimerStated && n.UnifiedMemory {
		out.ReclaimerEnabled = true
		out.ReclaimerThreshold = UnifiedMemoryReclaimerThreshold
		out.Sources["memory_reclaimer_threshold"] = LimitSourceDefault
	}
	for _, level := range []struct {
		limits NodeLimits
		source string
	}{{n.WorkerLimits(), LimitSourceWorker}, {n.OverrideLimits(), LimitSourceOverride}} {
		l := level.limits
		if l.MaxActiveBackends != nil {
			out.MaxActiveBackends = *l.MaxActiveBackends
			out.Sources["max_active_backends"] = level.source
		}
		if l.VRAMBudget != nil {
			out.VRAMBudget = *l.VRAMBudget
			out.Sources["vram_budget"] = level.source
		}
		if l.MemoryReclaimerThreshold != nil {
			// A threshold set for one node (by its worker's env or an admin
			// override) asks for the reclaimer there, even when the cluster
			// leaves it off: a threshold that never acts would be a no-op.
			out.ReclaimerEnabled = true
			out.ReclaimerThreshold = *l.MemoryReclaimerThreshold
			out.Sources["memory_reclaimer_threshold"] = level.source
		}
		if l.ModelSwap != nil {
			out.ModelSwap = *l.ModelSwap
			out.Sources["model_swap"] = level.source
		}
	}
	return out
}

// SetMemoryPolicy installs the source of the cluster memory policy. Until it
// is called, DefaultMemoryPolicy applies.
func (r *NodeRegistry) SetMemoryPolicy(src MemoryPolicySource) {
	if src == nil {
		r.memoryPolicy.Store(nil)
		return
	}
	r.memoryPolicy.Store(&src)
}

// HasMemoryPolicy reports whether a source of the cluster memory policy is
// installed (distributed mode with the cluster settings attached).
func (r *NodeRegistry) HasMemoryPolicy() bool { return r.memoryPolicy.Load() != nil }

// MemoryPolicy returns the cluster memory policy in force.
func (r *NodeRegistry) MemoryPolicy() MemoryPolicy {
	if src := r.memoryPolicy.Load(); src != nil {
		return (*src)()
	}
	return DefaultMemoryPolicy()
}

// NodePolicy returns the memory policy of node n.
func (r *NodeRegistry) NodePolicy(n *BackendNode) NodePolicy {
	return r.MemoryPolicy().For(n)
}

// effectiveVRAMBudget is the budget of a node that has no admin override:
// the worker's own, else the cluster default.
func (r *NodeRegistry) effectiveVRAMBudget(n *BackendNode) string {
	if n.LimitsWorker == "" && n.VRAMBudget != "" {
		// A row written before node limits existed holds the worker's
		// budget in vram_budget.
		return n.VRAMBudget
	}
	if b := n.WorkerLimits().VRAMBudget; b != nil {
		return *b
	}
	return r.MemoryPolicy().VRAMBudget
}

// SetNodeLimits stores admin overrides for a node. Only the fields l sets
// change; the VRAM budget goes through UpdateVRAMBudget so it is resolved and
// enforced like the budget of PUT /api/nodes/:id/vram-budget.
func (r *NodeRegistry) SetNodeLimits(ctx context.Context, nodeID string, l NodeLimits) error {
	if err := l.Validate(); err != nil {
		return err
	}
	node, err := r.Get(ctx, nodeID)
	if err != nil {
		return err
	}
	over := decodeLimits(node.LimitsOverride)
	budget := l.VRAMBudget
	l.VRAMBudget = nil
	over.merge(l)
	if err := r.db.WithContext(ctx).Model(&BackendNode{}).Where("id = ?", nodeID).
		Update(ColLimitsOverride, encodeLimits(over)).Error; err != nil {
		return fmt.Errorf("storing the limits of node %s: %w", nodeID, err)
	}
	if budget != nil {
		if *budget == "" {
			return r.ResetVRAMBudget(ctx, nodeID)
		}
		return r.UpdateVRAMBudget(ctx, nodeID, *budget)
	}
	return nil
}

// ResetNodeLimits clears every admin override of a node, so the worker's env
// and the cluster settings apply again.
func (r *NodeRegistry) ResetNodeLimits(ctx context.Context, nodeID string) error {
	if err := r.db.WithContext(ctx).Model(&BackendNode{}).Where("id = ?", nodeID).
		Update(ColLimitsOverride, "").Error; err != nil {
		return fmt.Errorf("clearing the limits of node %s: %w", nodeID, err)
	}
	return r.ResetVRAMBudget(ctx, nodeID)
}

// vramCeiling returns the VRAM budget ceiling and the raw total of a node,
// refreshing the stored effective budget when the worker's or the cluster's
// value changed since it was written. A heartbeat calls it before it caps
// the free VRAM the worker reports.
func (r *NodeRegistry) vramCeiling(ctx context.Context, nodeID string) (ceiling, total, headroom uint64) {
	var row BackendNode
	if err := r.db.WithContext(ctx).Model(&BackendNode{}).
		Select([]string{ColVRAMBudget, ColVRAMBudgetBytes, "vram_budget_manually_set", ColTotalVRAM, ColLimitsWorker, ColLimitsOverride, "unified_memory"}).
		Where("id = ?", nodeID).Take(&row).Error; err != nil {
		xlog.Debug("Could not read the VRAM budget of a node", "node", nodeID, "error", err)
		return 0, 0, 0
	}
	if !row.VRAMBudgetManuallySet {
		budget := r.effectiveVRAMBudget(&BackendNode{LimitsWorker: row.LimitsWorker, VRAMBudget: row.VRAMBudget})
		ceiling := r.resolveVRAMBudgetBytes(budget, row.TotalVRAM)
		if budget != row.VRAMBudget || ceiling != row.VRAMBudgetBytes {
			if err := r.db.WithContext(ctx).Model(&BackendNode{}).Where("id = ?", nodeID).
				Updates(map[string]any{ColVRAMBudget: budget, ColVRAMBudgetBytes: ceiling}).Error; err != nil {
				xlog.Warn("Failed to refresh the VRAM budget of a node", "node", nodeID, "error", err)
			}
		}
		row.VRAMBudget, row.VRAMBudgetBytes = budget, ceiling
	}
	return row.VRAMBudgetBytes, row.TotalVRAM, r.headroomVRAM(&row)
}

// headroomVRAM is the GPU memory the memory reclaimer of node n keeps free
// when a model is placed: (1 - threshold) of the memory the node may use,
// while its reclaimer is on.
func (r *NodeRegistry) headroomVRAM(n *BackendNode) uint64 {
	p := r.NodePolicy(n)
	if !p.ReclaimerEnabled || p.ReclaimerThreshold <= 0 || p.ReclaimerThreshold >= 1 {
		return 0
	}
	return uint64((1 - p.ReclaimerThreshold) * float64(nodeMemoryCeiling(n)))
}

// uncapAvailable is the inverse of capAvailable for a stored free VRAM
// figure: an estimate of the raw free VRAM, so a new budget can be applied
// before the next heartbeat brings the real reading. A stored 0 gives the
// least raw value it can stand for.
func uncapAvailable(stored, ceilingBytes, totalBytes uint64) uint64 {
	if ceilingBytes == 0 || totalBytes == 0 || ceilingBytes >= totalBytes {
		return stored
	}
	return min(stored+(totalBytes-ceilingBytes), totalBytes)
}
