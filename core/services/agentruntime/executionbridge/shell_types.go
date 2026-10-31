// SPDX-License-Identifier: MIT
package executionbridge

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/agentruntime/nibadapter"
	"github.com/mudler/LocalAI/core/services/agentruntime/ownership"
	"github.com/mudler/LocalAI/core/services/agentruntime/toolbroker"
)

var ErrHold = errors.New("host execution unavailable; hold required")

// Scope is host construction input, never accepted from a native candidate.
type Scope struct {
	redacted
	Identity nibadapter.Identity
	Revision uint64
	Lease    ownership.Lease
}

// HostCandidate contains correlation only, not authority. Arguments returns a copy.
type HostCandidate struct {
	redacted
	open func() candidateFields
}
type candidateFields struct{ invocation, protocol, agent, name, arguments string }

func (c HostCandidate) InvocationID() string { return c.fields().invocation }
func (c HostCandidate) ProtocolID() string   { return c.fields().protocol }
func (c HostCandidate) AgentID() string      { return c.fields().agent }
func (c HostCandidate) Name() string         { return c.fields().name }
func (c HostCandidate) Arguments() []byte    { return []byte(c.fields().arguments) }
func (c HostCandidate) fields() candidateFields {
	if c.open == nil {
		return candidateFields{}
	}
	return c.open()
}

// Registration must be resolved by the host registry, including root and parent
// grant. Engine AgentID is only a lookup hint, never a helper grant.
type Registration struct {
	redacted
	Authority                    toolbroker.Authority
	Tool                         toolbroker.ToolID
	Helper, Registration, Agent  string
	Provenance, Projection       string
	ApprovalTTL                  time.Duration
	AuthRevision, ConfigRevision uint64
}

// Coordinator preserves the accepted fenced Manager operations. Embedding the
// journal surface is intentional; Dispatch is never used by this adapter.
type Coordinator interface {
	toolbroker.Journal
	AdmitExecution(context.Context, ownership.Lease, time.Time, ownership.ExecutionMetadata) (ownership.ExecutionAdmission, error)
	DispatchAdmittedExecution(context.Context, ownership.Lease, time.Time, uint64, ownership.ExecutionAdmission) (ownership.ExecutionAdmission, error)
	ReadCompletedExecution(context.Context, ownership.Lease, time.Time, string, uint64, ownership.ExecutionAdmission) (ownership.CompletedExecution, error)
}
type Dependencies struct {
	Clock           func() time.Time
	Reader          toolbroker.ConversationReader
	Manager         Coordinator
	Catalog         toolbroker.Catalog
	Resolve         func(context.Context, Scope, HostCandidate) (Registration, error)
	Helpers         func(context.Context, toolbroker.Scope, string) (toolbroker.HelperGrant, error)
	Approvals       func(context.Context, toolbroker.ApprovalDecision) (toolbroker.Approval, error)
	Audit           func(context.Context, toolbroker.AuditEntry) error
	ProjectText     func(context.Context, HostCandidate, string) (string, error)
	Release         func(context.Context, HostCandidate, ownership.ExecutionAdmission) error
	AuthorizeOrigin func(context.Context, HostCandidate, ownership.ExecutionAdmission) error
	Waiting         func(context.Context, Control) error
}

// Bridge is isolated: callers install both adapters on the same native attempt.
// No native body or public executor fallback is stored here.
type Bridge struct {
	redacted
	scope  Scope
	dep    Dependencies
	broker *toolbroker.Broker
	mu     sync.Mutex
	closed bool
	active map[*controlSlot]struct{}
}
type executionCandidate struct {
	redacted
	candidate    HostCandidate
	registration Registration
	binding      toolbroker.Binding
	call         Call
	admission    ownership.ExecutionAdmission
}
