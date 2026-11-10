//go:build linux

// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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
	var sent atomic.Int32
	jobsClient := func(jobs bool) (*Client, func(contract.JobReply), *bool) {
		var mu sync.Mutex
		var reply contract.JobReply
		var c *Client
		liveDuring := false
		sent.Store(0)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			sent.Add(1)
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
	actResult := func(c *Client, op, args string) (contract.Result, error) {
		e := envelope("act")
		e.Request.AddonID, e.Request.Action, e.Request.OperationID, e.Request.ResourceID = "sandbox", "act", op, "chat-c1"
		e.Request.Arguments = json.RawMessage(args)
		ctx := hostops.WithBinding(context.Background(), hostops.Binding{Principal: "alice", ChatID: "c1"})
		return c.Act(ctx, e)
	}
	act := func(c *Client, op, args string) error {
		_, err := actResult(c, op, args)
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

	It("refuses a start, without sending it, when every lease place of the add-on is taken", func() {
		c, answer, _ := jobsClient(true)
		var held []*hostops.LeaseSlot
		for range hostops.MaxLeasesPerAddon {
			sl, err := c.inflight.ReserveLease("sandbox")
			Expect(err).NotTo(HaveOccurred())
			held = append(held, sl)
		}
		answer(contract.JobReply{OK: true, Job: &running})
		res, err := actResult(c, "op1", `{"tool":"job","verb":"start","command":"make"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(sent.Load()).To(BeZero(), "the add-on started nothing")
		var reply contract.JobReply
		Expect(json.Unmarshal(res.Payload, &reply)).To(Succeed())
		Expect(reply.Validate(contract.JobArgs{Verb: contract.JobStart, Command: "make"})).To(Succeed())
		Expect(reply.Code).To(Equal(contract.JobCodeLimit))
		Expect(reply.Error).To(Equal(JobLeasesFullText))

		// Other calls still pass, and a freed place lets a start through.
		answer(contract.JobReply{OK: true, Jobs: []contract.JobInfo{}})
		Expect(act(c, "op2", `{"tool":"job","verb":"list"}`)).To(Succeed())
		Expect(sent.Load()).To(BeEquivalentTo(1))
		held[0].Release()
		answer(contract.JobReply{OK: true, Job: &running})
		Expect(act(c, "op3", `{"tool":"job","verb":"start","command":"make"}`)).To(Succeed())
		Expect(sent.Load()).To(BeEquivalentTo(2))
		ok, leased := live(c, "op3")
		Expect(ok && leased).To(BeTrue())
	})

	It("gives the reserved place back when the start makes no job", func() {
		c, answer, _ := jobsClient(true)
		answer(contract.JobReply{Code: contract.JobCodeLimit, Error: "full"})
		for i := range hostops.MaxLeasesPerAddon + 1 {
			Expect(act(c, fmt.Sprintf("op%d", i), `{"tool":"job","verb":"start","command":"x"}`)).To(Succeed())
		}
		_, err := c.inflight.ReserveLease("sandbox")
		Expect(err).NotTo(HaveOccurred())
	})
})
