// SPDX-License-Identifier: MIT
package runtime

import (
	"encoding/json"
	"time"

	"github.com/mudler/xlog"

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
func (c *Client) leaseJobs(env contract.Envelope, r contract.Result) {
	p := c.registration.Capabilities.ExecutionProvider
	if c.inflight == nil || p == nil || p.Jobs == nil {
		return
	}
	var args contract.JobArgs
	var tool struct {
		Tool string `json:"tool"`
	}
	if json.Unmarshal(env.Request.Arguments, &tool) != nil || tool.Tool != p.Jobs.Tool {
		return
	}
	// The arguments also carry the tool name and the host's execution call.
	var raw map[string]json.RawMessage
	if json.Unmarshal(env.Request.Arguments, &raw) != nil {
		return
	}
	delete(raw, "tool")
	delete(raw, contract.ExecutionCallArgKey)
	b, _ := json.Marshal(raw)
	if json.Unmarshal(b, &args) != nil || args.Validate() != nil {
		return
	}
	var reply contract.JobReply
	if json.Unmarshal(r.Payload, &reply) != nil || reply.Validate(args) != nil || !reply.OK {
		return
	}
	addon, inc := c.registration.AddonID, c.registration.Incarnation
	if args.Verb == contract.JobStart && reply.Job != nil && !reply.Job.Finished() {
		until := time.Now().Add(contract.JobLifetime + JobLeaseMargin)
		if err := c.inflight.Lease(addon, env.Request.OperationID, inc, reply.Job.ID, until); err != nil {
			// The job runs, but it cannot commit or reach the network once
			// the call is over. Its output stays readable.
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

func derefJob(j *contract.JobInfo) []contract.JobInfo {
	if j == nil {
		return nil
	}
	return []contract.JobInfo{*j}
}
