package studio

import (
	"context"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The capabilities the host asks about before it lets a connector chat do
// something on the owner's behalf. The names are the closed list of the
// contract, shared with the manifest and the binding.
const (
	CapTriggersPropose  = contract.CapTriggersPropose
	CapTriggersRun      = contract.CapTriggersRun
	CapProposalsConfirm = contract.CapProposalsConfirm
)

// CapabilityRequest is one question to a CapabilityGate. Binding is the
// connector binding, Owner its owner and Agent its agent. Sender is the
// platform ID of the person who wrote, and is empty when the question is not
// about one message (the tools a turn gets, a run that is due). Kind is the
// kind of the proposal for CapProposalsConfirm, such as "trigger". Sensitive
// says that the thing asked for changes what the agent may do or reach, so a
// gate must not grant it on the word of a platform role alone.
type CapabilityRequest struct {
	Capability string
	Binding    string
	Owner      string
	Agent      string
	Sender     string
	// Roles are the role IDs, declared by the kind, that the add-on reported
	// for the sender of this message. Empty when Sender is.
	Roles     []string
	Kind      string
	Sensitive bool
}

// CapabilityGate decides whether a binding, and for a message its sender, may
// use a capability. It is the one seam between the features that act from a
// connector chat and the model of who may do what. An implementation reads
// the switches of the owner (the grants of the agent, the capabilities of the
// binding, pairing and roles) and answers from them. It must be fast and safe
// for concurrent use, and it must fail closed: any doubt is false.
type CapabilityGate interface {
	Allow(ctx context.Context, r CapabilityRequest) bool
}

// CapabilityGateFunc makes a function a CapabilityGate.
type CapabilityGateFunc func(ctx context.Context, r CapabilityRequest) bool

// Allow calls f.
func (f CapabilityGateFunc) Allow(ctx context.Context, r CapabilityRequest) bool { return f(ctx, r) }

// DenyCapabilities is the gate a service has until the application sets
// another: nothing is allowed. A connector chat therefore gets no trigger
// tool, no confirmation by text and no run from a trigger it proposed, until
// something enables them.
var DenyCapabilities CapabilityGate = CapabilityGateFunc(func(context.Context, CapabilityRequest) bool { return false })

// SetCapabilityGate gives the service the gate for the capabilities above.
// The application calls it once, before the service is shared. Nil restores
// DenyCapabilities.
func (s *Service) SetCapabilityGate(g CapabilityGate) {
	if g == nil {
		g = DenyCapabilities
	}
	s.capGate = g
}

// allowCap asks the gate; a service without one denies.
func (s *Service) allowCap(ctx context.Context, r CapabilityRequest) bool {
	if s.capGate == nil {
		return false
	}
	return s.capGate.Allow(ctx, r)
}
