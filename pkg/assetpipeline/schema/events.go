// SPDX-License-Identifier: MIT
package schema

import (
	"encoding/json"
	"time"
)

type EventType string

const (
	ConfigurationEvent EventType = "configuration"
	ExecutionEvent     EventType = "execution"
	SystemEvent        EventType = "system"
)

type Action string

const (
	Started     Action = "started"
	Progress    Action = "progress"
	Finished    Action = "finished"
	Selected    Action = "selected"
	Invalidated Action = "invalidated"
	Removed     Action = "removed"
	RunPolicy   Action = "run-policy"
	Notice      Action = "notice"
	Observed    Action = "observed"
)

type DataType string

const (
	String  DataType = "string"
	Boolean DataType = "boolean"
	Integer DataType = "integer"
	Object  DataType = "object"
	Array   DataType = "array"
	Null    DataType = "null"
)

type FailureCode string

const (
	UnknownFailure       FailureCode = "unknown"
	CancelledFailure     FailureCode = "cancelled"
	InterruptedFailure   FailureCode = "interrupted"
	InvalidOutputFailure FailureCode = "invalid-output"
)

type Failure struct {
	Code    FailureCode `json:"code"`
	Message string      `json:"message"`
	Cause   *Failure    `json:"cause,omitempty"`
}

// Event carries one operation. The interpreter rejects fields belonging to
// another operation. Kind/Message describe the user command for the status UI.
type Event struct {
	ID          string          `json:"id"`
	At          time.Time       `json:"at"`
	Version     int             `json:"version"`
	Type        EventType       `json:"type,omitempty"`
	Kind        string          `json:"kind"`
	Node        string          `json:"node,omitempty"`
	Message     string          `json:"message"`
	Path        string          `json:"path,omitempty"`
	ValueType   DataType        `json:"valueType,omitempty"`
	Value       json.RawMessage `json:"value,omitempty"`
	Action      Action          `json:"action,omitempty"`
	AttemptID   string          `json:"attempt,omitempty"`
	Arguments   *Arguments      `json:"arguments,omitempty"`
	Status      Status          `json:"status,omitempty"`
	Outputs     []Value         `json:"outputs,omitempty"`
	Selection   *Value          `json:"selection,omitempty"`
	Explanation string          `json:"explanation,omitempty"`
	Failure     *Failure        `json:"failure,omitempty"`
	Diagnostics []string        `json:"diagnostics,omitempty"`
	Progress    string          `json:"progress,omitempty"`
	JobID       string          `json:"jobId,omitempty"`
	RunAll      *bool           `json:"runAll,omitempty"`
}

type Status string

const (
	Stale       Status = "stale"
	Running     Status = "running"
	Waiting     Status = "waiting"
	Succeeded   Status = "succeeded"
	Failed      Status = "failed"
	Interrupted Status = "interrupted"
	Cancelled   Status = "cancelled"
)

type Profile struct {
	Model   string   `json:"model"`
	Inputs  []string `json:"inputs"`
	Advice  string   `json:"advice,omitempty"`
	Version string   `json:"version"`
}

type Arguments struct {
	Node   Node             `json:"node"`
	Input  Value            `json:"input"`
	Inputs map[string]Value `json:"inputs,omitempty"`
	Target Profile          `json:"target"`
}

// Attempt is the presentation record for a captured invocation.
type Attempt struct {
	ID          string     `json:"id"`
	Arguments   Arguments  `json:"arguments"`
	Status      Status     `json:"status"`
	Started     time.Time  `json:"started"`
	Finished    *time.Time `json:"finished,omitempty"`
	Outputs     []Value    `json:"outputs,omitempty"`
	Explanation string     `json:"explanation,omitempty"`
	Error       string     `json:"error,omitempty"` // UI display text derived from Failure.
	Failure     *Failure   `json:"failure,omitempty"`
	Progress    string     `json:"progress,omitempty"`
	JobID       string     `json:"jobId,omitempty"`
	Diagnostics []string   `json:"diagnostics,omitempty"`
}
