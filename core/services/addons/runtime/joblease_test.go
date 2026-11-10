// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The lease of a background job", func() {
	now := time.Now().UTC().Truncate(time.Second)
	running := contract.JobInfo{ID: "job-abcdef", State: "running", Exit: -1, Started: now, LastOutput: now}
	exited := contract.JobInfo{ID: "job-abcdef", State: "exited", End: "exit", Exit: 0, Started: now, Ended: now, LastOutput: now}

	// jobsClient is a client of an add-on whose jobs tool is "job" and that
	// answers each act with the next reply.
	jobsClient := func(jobs bool) (*Client, func(contract.JobReply), *bool) {
		var mu sync.Mutex
		var reply contract.JobReply
		var c *Client
		liveDuring := false
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			_, _, liveDuring = c.inflight.LookupBound("sandbox", "op1")
			p, _ := json.Marshal(reply)
			res, _ := json.Marshal(contract.Result{Payload: p})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(res)
		}))
		DeferCleanup(srv.Close)
		caps := contract.Capabilities{Service: true, Actions: true}
		if jobs {
			caps.ExecutionProvider = &contract.ExecutionProvider{Tools: []string{"bash"}, StatusTool: "status", Jobs: &contract.ExecutionJobs{Tool: "job"}}
		}
		var e error
		c, e = newClient(Registration{AddonID: "sandbox", Generation: 1, Incarnation: "inc1", Endpoint: srv.URL, Capabilities: caps}, strings.Repeat("a", 64), contract.DefaultLimits())
		Expect(e).NotTo(HaveOccurred())
		c.accept = true
		c.inflight = hostops.NewInFlight()
		return c, func(r contract.JobReply) { mu.Lock(); reply = r; mu.Unlock() }, &liveDuring
	}
	act := func(c *Client, op, args string) error {
		e := envelope("act")
		e.Request.AddonID, e.Request.Action, e.Request.OperationID, e.Request.ResourceID = "sandbox", "act", op, "chat-c1"
		e.Request.Arguments = json.RawMessage(args)
		ctx := hostops.WithBinding(context.Background(), hostops.Binding{Principal: "alice", ChatID: "c1"})
		_, err := c.Act(ctx, e)
		return err
	}
	live := func(c *Client, op string) (bool, bool) {
		_, _, leased, ok := c.inflight.LookupLive("sandbox", op)
		return ok, leased
	}

	It("keeps the start live after the call and ends it when an answer reports the job finished", func() {
		c, answer, _ := jobsClient(true)
		answer(contract.JobReply{OK: true, Job: &running})
		Expect(act(c, "op1", `{"tool":"job","verb":"start","command":"make","execution":{"profile":"base"}}`)).To(Succeed())
		ok, leased := live(c, "op1")
		Expect(ok && leased).To(BeTrue(), "the job's operation stays live")
		Expect(c.inflight.Leases("sandbox")).To(Equal(1))

		answer(contract.JobReply{OK: true, Jobs: []contract.JobInfo{running}})
		Expect(act(c, "op2", `{"tool":"job","verb":"list"}`)).To(Succeed())
		ok, _ = live(c, "op1")
		Expect(ok).To(BeTrue(), "a running job keeps its lease")

		answer(contract.JobReply{OK: true, Job: &exited})
		Expect(act(c, "op3", `{"tool":"job","verb":"read","id":"job-abcdef","observe":true}`)).To(Succeed())
		ok, _ = live(c, "op1")
		Expect(ok).To(BeFalse(), "the lease ends with the job")
	})

	It("leases nothing for another tool, a refusal, a finished start, a bad answer or an add-on without jobs", func() {
		c, answer, liveDuring := jobsClient(true)
		answer(contract.JobReply{OK: true, Job: &running})
		Expect(act(c, "op1", `{"tool":"bash","command":"x"}`)).To(Succeed())
		Expect(*liveDuring).To(BeTrue(), "the call itself is live")
		answer(contract.JobReply{Code: contract.JobCodeLimit, Error: "full"})
		Expect(act(c, "op1", `{"tool":"job","verb":"start","command":"x"}`)).To(Succeed())
		answer(contract.JobReply{OK: true, Job: &exited})
		Expect(act(c, "op1", `{"tool":"job","verb":"start","command":"x"}`)).To(Succeed())
		bad := running
		bad.ID = "../x"
		answer(contract.JobReply{OK: true, Job: &bad})
		Expect(act(c, "op1", `{"tool":"job","verb":"start","command":"x"}`)).To(Succeed())
		Expect(c.inflight.Leases("sandbox")).To(BeZero())

		old, answerOld, _ := jobsClient(false)
		answerOld(contract.JobReply{OK: true, Job: &running})
		Expect(act(old, "op1", `{"tool":"job","verb":"start","command":"x"}`)).To(Succeed())
		Expect(old.inflight.Leases("sandbox")).To(BeZero())
	})
})
