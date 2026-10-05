// Package failover serves a model name from an ordered chain of target
// models, moving to the next target when one fails and back when it
// recovers.
package failover

import (
	"slices"
	"time"

	"github.com/mudler/LocalAI/core/config"
)

type TargetState string

const (
	StateHealthy    TargetState = "healthy"
	StateDown       TargetState = "down"
	StateRecovering TargetState = "recovering"
	StateMissing    TargetState = "missing"
)

type ChainState string

const (
	ChainPrimary  ChainState = "primary"
	ChainFallback ChainState = "fallback"
	ChainDegraded ChainState = "degraded"
)

type Kind string

const (
	KindLocal  Kind = "local"
	KindRemote Kind = "remote"
)

type Reason string

const (
	ReasonTrip     Reason = "trip"
	ReasonRecovery Reason = "recovery"
	ReasonManual   Reason = "manual"
	ReasonDegraded Reason = "degraded"
	ReasonMissing  Reason = "missing"
	ReasonInitial  Reason = "initial"
)

type EventType string

const (
	EventChainSwitched EventType = "chain.switched"
	EventTargetState   EventType = "target.state"
)

// Event is one change of a target state or of a chain's active target.
type Event struct {
	Type   EventType `json:"type"`
	Chain  string    `json:"chain,omitempty"`
	Target string    `json:"target,omitempty"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	State  string    `json:"state,omitempty"`
	Reason Reason    `json:"reason"`
	Error  string    `json:"error,omitempty"`
	At     time.Time `json:"at"`
}

type TargetStatus struct {
	Model         string      `json:"model"`
	Kind          Kind        `json:"kind"`
	Warm          bool        `json:"warm"`
	State         TargetState `json:"state"`
	ConsecutiveOK int         `json:"consecutive_ok"`
	LastProbe     *time.Time  `json:"last_probe,omitempty"`
	LastError     string      `json:"last_error,omitempty"`
}

type ChainStatus struct {
	Name        string         `json:"name"`
	State       ChainState     `json:"state"`
	Active      string         `json:"active"`
	ActiveSince time.Time      `json:"active_since"`
	Pinned      *string        `json:"pinned"`
	Targets     []TargetStatus `json:"targets"`
}

// KindOf decides how a target is probed: proxy backends forward to another
// server and are checked over HTTP, everything else runs in this instance.
func KindOf(cfg config.ModelConfig) Kind {
	if cfg.IsRemoteProxy() {
		return KindRemote
	}
	return KindLocal
}

// MergePinned adds warm failover targets to the config-pinned model list, so
// the watchdog never evicts them.
func MergePinned(pinned, warm []string) []string {
	out := slices.Clone(pinned)
	for _, w := range warm {
		if !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}
