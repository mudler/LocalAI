// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"time"

	"github.com/mudler/LocalAI/pkg/assetpipeline/schema"
)

type Arguments struct {
	Node   NodeConfig
	Input  Value
	Inputs map[string]Value
	Target Profile
}

type Attempt struct {
	ID          string
	Arguments   Arguments
	Status      Status
	Started     time.Time
	Finished    *time.Time
	Outputs     []Value
	Explanation string
	Failure     *Failure
	Progress    string
	JobID       string
	Diagnostics []string
}

type Event struct {
	ID      string
	At      time.Time
	Version int
	Type    schema.EventType
	Kind    EventKind
	Node    string
	Message string

	Path      ConfigurationPath
	ValueType schema.DataType
	Value     any

	Action      schema.Action
	AttemptID   string
	Arguments   *Arguments
	Status      Status
	Outputs     []Value
	Selection   *Value
	Explanation string
	Failure     *Failure
	Diagnostics []string
	Progress    string
	JobID       string
	RunAll      *bool
}

// Failure retains structured causes without carrying transport annotations.
type Failure struct {
	Code    schema.FailureCode
	Message string
	Cause   *Failure
}

func (f *Failure) Error() string {
	if f == nil {
		return ""
	}
	return f.Message
}

func (f *Failure) Unwrap() error {
	if f == nil || f.Cause == nil {
		return nil
	}
	return f.Cause
}

// Optional empty maps have the same execution meaning as absent maps. Canonical
// arguments keep a captured invocation stable across the wire's omitempty fields.
func canonicalArguments(a Arguments) Arguments {
	if len(a.Node.Inputs) == 0 {
		a.Node.Inputs = nil
	}
	if len(a.Node.Params) == 0 {
		a.Node.Params = nil
	}
	if len(a.Inputs) == 0 {
		a.Inputs = nil
	}
	return a
}

func (e Event) hasExecutionPayload() bool {
	return e.AttemptID != "" || e.Arguments != nil || e.Status != "" ||
		e.Outputs != nil || e.Selection != nil || e.Failure != nil || e.RunAll != nil ||
		e.Progress != "" || e.JobID != "" || e.Explanation != "" || e.Diagnostics != nil
}
