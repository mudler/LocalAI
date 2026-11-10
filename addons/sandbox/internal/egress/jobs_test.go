// SPDX-License-Identifier: MIT
package egress_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/egress"
)

var _ = Describe("the proxy while a background job runs", func() {
	var plainSrv *httptest.Server
	BeforeEach(func() {
		plainSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n, _ := io.Copy(io.Discard, r.Body)
			_, _ = fmt.Fprintf(w, "got %d", n)
		}))
		DeferCleanup(plainSrv.Close)
	})

	// get sends one plain request for host through the proxy of alice's c1.
	get := func(f fixture, host string, payload string) *http.Response {
		c := f.dial()
		u := "http://" + host + ":" + strconv.Itoa(portOf(plainSrv.URL)) + "/"
		req, _ := http.NewRequest(http.MethodPost, u, strings.NewReader(payload))
		req.Close = true
		Expect(req.WriteProxy(c)).To(Succeed())
		return readResponse(c, bufio.NewReader(c), req)
	}

	It("lets an allowed host through under the job's operation when no call runs", func() {
		f := newFixture(0, allow, egress.Limits{})
		end := f.proxy.BeginJob("alice", "c1", "jobop")
		resp := get(f, "public.test", "")
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(body(resp)).To(Equal("got 0"))
		end()
		Expect(f.host.ops).To(ConsistOf("jobop"))
		Expect(f.host.audit()).To(ContainElement(SatisfyAll(HaveField("Verdict", "done"), HaveField("Host", "public.test"))))
	})

	It("gives a connection to the call while a call runs, and to the oldest job otherwise", func() {
		f := newFixture(0, allow, egress.Limits{})
		endA := f.proxy.BeginJob("alice", "c1", "jobA")
		endB := f.proxy.BeginJob("alice", "c1", "jobB")
		call := f.proxy.Begin(context.Background(), "alice", "c1", "callop")
		Expect(get(f, "public.test", "").StatusCode).To(Equal(http.StatusOK))
		call()
		Expect(get(f, "public.test", "").StatusCode).To(Equal(http.StatusOK))
		endA()
		Expect(get(f, "public.test", "").StatusCode).To(Equal(http.StatusOK))
		endB()
		Expect(f.host.ops).To(Equal([]string{"callop", "jobA", "jobB"}))
		// With no job and no call, the network is closed again.
		resp := get(f, "public.test", "")
		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
		Expect(body(resp)).To(ContainSubstring("no command of this chat is running"))
	})

	It("blocks a host that needs the person's yes with a text the job prints, and the host audits it", func() {
		f := newFixture(0, func(egress.CheckArgs) egress.Verdict {
			return egress.Verdict{Action: "block", Reason: "unattended"}
		}, egress.Limits{})
		end := f.proxy.BeginJob("alice", "c1", "jobop")
		defer end()
		resp := get(f, "public.test", "")
		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
		Expect(body(resp)).To(SatisfyAll(ContainSubstring("Blocked by LocalAI"), ContainSubstring("needs the user's approval"), ContainSubstring("background job")))
		// The host made the verdict under the job's operation, which is where
		// it audits it.
		Expect(f.host.ops).To(ConsistOf("jobop"))
	})

	It("caps the bytes of each job on its own", func() {
		f := newFixture(0, allow, egress.Limits{UpBytes: 1000})
		endA := f.proxy.BeginJob("alice", "c1", "jobA")
		Expect(get(f, "public.test", strings.Repeat("a", 600)).StatusCode).To(Equal(http.StatusOK))
		resp := get(f, "public.test", strings.Repeat("a", 600))
		Expect(resp.StatusCode).NotTo(Equal(http.StatusOK), "job A moved more than its cap")
		endA()
		endB := f.proxy.BeginJob("alice", "c1", "jobB")
		defer endB()
		Expect(get(f, "public.test", strings.Repeat("a", 600)).StatusCode).To(Equal(http.StatusOK), "job B has its own cap")
	})

	It("ends the tunnels of a job when the job ends, and an end may be called twice", func() {
		f := newFixture(0, allow, egress.Limits{})
		end := f.proxy.BeginJob("alice", "c1", "jobop")
		end()
		end()
		Expect(get(f, "public.test", "").StatusCode).To(Equal(http.StatusForbidden))
	})
})
