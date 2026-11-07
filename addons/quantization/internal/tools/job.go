// SPDX-License-Identifier: MIT
package tools

// job is what the add-on knows of one resource a tool created: a job it
// started. The engine job ID never leaves the add-on: a model names the job by
// its resource.
type job struct {
	JobID string `json:"jobId,omitempty"`
	Name  string `json:"name"`
	// Revision counts the changes a tool made to the job. It is the data
	// revision of the resource, so a stale approval is noticed.
	Revision uint64 `json:"revision"`
	// StartOp is the operation that started the job: the change set a person
	// can undo. StartedNS is when, in Unix nanoseconds, for the retention.
	// UndoneBy is the operation that undid it.
	StartOp   string `json:"startOp,omitempty"`
	StartedNS int64  `json:"startedNs,omitempty"`
	UndoneBy  string `json:"undoneBy,omitempty"`
}
