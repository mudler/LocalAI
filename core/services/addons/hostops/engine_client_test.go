// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeClientJobs struct {
	calls []ClientCall
	ops   []string
}

func (f *fakeClientJobs) Start(_ context.Context, c ClientCall, task string, _ map[string]string) (ClientJob, error) {
	f.calls = append(f.calls, c)
	f.ops = append(f.ops, "start:"+task)
	return ClientJob{ID: "j1", Status: "pending"}, nil
}
func (f *fakeClientJobs) Get(_ context.Context, c ClientCall, id string) (ClientJob, error) {
	f.calls = append(f.calls, c)
	f.ops = append(f.ops, "get:"+id)
	return ClientJob{ID: id, Status: "running"}, nil
}
func (f *fakeClientJobs) Cancel(_ context.Context, c ClientCall, id string) (ClientJob, error) {
	f.calls = append(f.calls, c)
	f.ops = append(f.ops, "cancel:"+id)
	return ClientJob{ID: id, Status: "cancelled"}, nil
}

var _ = Describe("Client job operations", func() {
	var (
		f    *fixture
		host *fakeClientJobs
		has  bool
	)
	BeforeEach(func() {
		has = true
		host = &fakeClientJobs{}
		f = &fixture{inflight: NewInFlight(), seen: &Call{}}
		f.b = New(f.inflight, func(_, feature string) bool { return has && feature == "mcp_jobs" }, contract.DefaultLimits())
		Expect(RegisterClientJobs(f.b, host)).To(Succeed())
		// An add-on that was granted a wide prefix still meets the client rule.
		Expect(f.b.Register(Spec{Op: "workspace.list", Scope: "read", Feature: "chats", Handler: func(context.Context, Call) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }})).To(Succeed())
		f.h = f.b.Handler("demo", "secret", []string{"client.", "workspace."})
	})
	live := func(op string, actor contract.Actor) {
		e := env("demo", op, "read", time.Now().Add(time.Minute))
		e.Authority.Actor = actor
		DeferCleanup(f.inflight.Begin(e))
	}
	client := contract.Actor{UserID: "owner-1", CredentialID: "client:key-9", Mode: "named-user"}

	It("takes the client and the owner from the host's record of the call, never from the arguments", func() {
		live("op1", client)
		code, out := f.call("secret", "op1", "client.jobs.start", `{"task":"ask","params":{"q":"hi"}}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		Expect(host.calls).To(ConsistOf(ClientCall{OwnerID: "owner-1", ClientID: "key-9"}))
		code, _ = f.call("secret", "op1", "client.jobs.get", `{"id":"j1"}`)
		Expect(code).To(Equal(http.StatusOK))
		code, _ = f.call("secret", "op1", "client.jobs.cancel", `{"id":"j1"}`)
		Expect(code).To(Equal(http.StatusOK))
		Expect(host.ops).To(Equal([]string{"start:ask", "get:j1", "cancel:j1"}))
	})

	It("refuses an argument that names a client or a user, and unknown arguments", func() {
		live("op1", client)
		for _, body := range []string{
			`{"task":"ask","client":"other"}`,
			`{"task":"ask","user":"bob"}`,
			`{"task":"ask","owner":"bob"}`,
			`{"task":"ask","extra":1}`,
			`{"task":""}`,
			`{"task":"ask","params":{"":"x"}}`,
		} {
			code, _ := f.call("secret", "op1", "client.jobs.start", body)
			Expect(code).To(Equal(http.StatusBadRequest), body)
		}
		Expect(host.calls).To(BeEmpty())
	})

	It("refuses the client operations to a call that no client made", func() {
		live("op2", contract.Actor{UserID: "u1", CredentialID: "c1", Mode: "named-user"})
		for _, op := range []string{"client.jobs.start", "client.jobs.get", "client.jobs.cancel"} {
			code, _ := f.call("secret", "op2", op, `{"task":"t","id":"j1"}`)
			Expect(code).To(Equal(http.StatusForbidden), op)
		}
		Expect(host.calls).To(BeEmpty())
	})

	It("refuses every other operation to a call that a client made", func() {
		live("op1", client)
		code, _ := f.call("secret", "op1", "workspace.list", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))
	})

	It("is a host service for any add-on the owner grants it to, by grant and not by add-on id", func() {
		// Two add-ons with ids that mean nothing to the host. Only the owner's
		// grant (the prefix list of the broker handler) decides.
		granted := f.b.Handler("notes-sync", "secret", []string{"client."})
		denied := f.b.Handler("other-addon", "secret", []string{"workspace."})
		for id, h := range map[string]http.Handler{"notes-sync": granted, "other-addon": denied} {
			e := env(id, "op-"+id, "read", time.Now().Add(time.Minute))
			e.Authority.Actor = client
			DeferCleanup(f.inflight.Begin(e))
			f.h = h
			code, _ := f.call("secret", "op-"+id, "client.jobs.get", `{"id":"j1"}`)
			if id == "notes-sync" {
				Expect(code).To(Equal(http.StatusOK), id)
			} else {
				Expect(code).To(Equal(http.StatusForbidden), id)
			}
		}
	})

	It("needs the jobs feature of the owner and a live call", func() {
		live("op1", client)
		has = false
		code, out := f.call("secret", "op1", "client.jobs.get", `{"id":"j1"}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error.Code).To(Equal(contract.HostFeature))
		has = true
		code, out = f.call("secret", "nope", "client.jobs.get", `{"id":"j1"}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error.Code).To(Equal(contract.HostNoLiveOperation))
	})

	It("limits the parameters", func() {
		live("op1", client)
		big := make([]byte, MaxClientJobParamValue+1)
		for i := range big {
			big[i] = 'a'
		}
		code, _ := f.call("secret", "op1", "client.jobs.start", `{"task":"ask","params":{"q":"`+string(big)+`"}}`)
		Expect(code).To(Equal(http.StatusBadRequest))
	})
})
