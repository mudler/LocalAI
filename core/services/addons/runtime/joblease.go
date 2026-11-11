// SPDX-License-Identifier: MIT
package runtime

import (
	"encoding/json"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// JobLeaseMargin is how long a job lease outlives the lifetime of its job:
// the kill grace and the final checkpoint run after the lifetime.
const JobLeaseMargin = contract.JobKillGrace + 5*time.Minute

// leaseJobs keeps the request of a background job live after the call that
// started it, so the job's final checkpoint and network checks pass the host
// (see hostops.InFlight.Lease), and ends the lease of every job an answer
// reports finished. It runs in the process that holds the in-flight table,
// the owner of the add-on, while the call is still in flight. Only the jobs
// tool the manifest binds (ExecutionProvider.Jobs) counts, and only an answer
// the host's own check accepts.
func (c *Client) leaseJobs(env contract.Envelope, r contract.Result, slot *hostops.LeaseSlot) {
	args, ok := c.jobArgs(env)
	if !ok {
		return
	}
	var reply contract.JobReply
	if json.Unmarshal(r.Payload, &reply) != nil || reply.Validate(args) != nil || !reply.OK {
		return
	}
	addon, inc := c.registration.AddonID, c.registration.Incarnation
	if args.Verb == contract.JobStart && reply.Job != nil && !reply.Job.Finished() {
		until := time.Now().Add(contract.JobLifetime + JobLeaseMargin)
		lease := c.inflight.Lease
		if slot != nil {
			lease = func(_, op, inc, job string, until time.Time) error { return slot.Lease(op, inc, job, until) }
		}
		if err := lease(addon, env.Request.OperationID, inc, reply.Job.ID, until); err != nil {
			// The job runs, but it cannot commit or reach the network once
			// the call is over. Its output stays readable. The place was
			// reserved before the start, so this is not a full table.
			xlog.Warn("add-on job lease refused", "addon", addon, "job", reply.Job.ID, "error", err)
		}
		return
	}
	for _, j := range append(reply.Jobs, derefJob(reply.Job)...) {
		if j.Finished() {
			c.inflight.EndLease(addon, inc, j.ID)
		}
	}
}

// jobArgs returns the arguments of a call of the jobs tool that the manifest
// binds (ExecutionProvider.Jobs), and false for any other call.
func (c *Client) jobArgs(env contract.Envelope) (contract.JobArgs, bool) {
	p := c.registration.Capabilities.ExecutionProvider
	if c.inflight == nil || p == nil || p.Jobs == nil {
		return contract.JobArgs{}, false
	}
	var args contract.JobArgs
	var tool struct {
		Tool string `json:"tool"`
	}
	if json.Unmarshal(env.Request.Arguments, &tool) != nil || tool.Tool != p.Jobs.Tool {
		return args, false
	}
	// The arguments also carry the tool name and the host's execution call.
	var raw map[string]json.RawMessage
	if json.Unmarshal(env.Request.Arguments, &raw) != nil {
		return args, false
	}
	delete(raw, "tool")
	delete(raw, contract.ExecutionCallArgKey)
	b, _ := json.Marshal(raw)
	if json.Unmarshal(b, &args) != nil || args.Validate() != nil {
		return args, false
	}
	return args, true
}

// JobLeasesFullText is the refusal of a start when every lease place of the
// add-on is taken: a job without a lease could not save its files or reach
// the network once the call is over, so it is not started.
const JobLeasesFullText = "Not started: this add-on holds as many background jobs as it may right now. End a job, or wait for one to end, then try again."

// reserveJobLease takes the place of the lease of a job start before it is
// sent. A full table is a refusal of the jobs tool, and nothing is sent. The
// slot is nil for any other call.
func (c *Client) reserveJobLease(env contract.Envelope) (*hostops.LeaseSlot, *contract.Result) {
	args, ok := c.jobArgs(env)
	if !ok || args.Verb != contract.JobStart {
		return nil, nil
	}
	slot, err := c.inflight.ReserveLease(c.registration.AddonID)
	if err == nil {
		return slot, nil
	}
	p, _ := json.Marshal(contract.JobReply{Code: contract.JobCodeLimit, Error: JobLeasesFullText})
	return nil, &contract.Result{Payload: p}
}

func derefJob(j *contract.JobInfo) []contract.JobInfo {
	if j == nil {
		return nil
	}
	return []contract.JobInfo{*j}
}
