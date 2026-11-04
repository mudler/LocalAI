// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"

	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type observationCaps struct{ toolSet }

type rawObservationCaps struct {
	toolSet
	caps contract.Capabilities
}

func (s rawObservationCaps) DeclaredCapabilities(context.Context, string, uint64) (contract.Capabilities, error) {
	return s.caps, nil
}

func (s observationCaps) DeclaredCapabilities(context.Context, string, uint64) (contract.Capabilities, error) {
	return contract.Capabilities{Tools: []string{"run", "health"}, ExecutionProvider: &contract.ExecutionProvider{Tools: []string{"run"}, StatusTool: "health"}, ToolDefinitions: []contract.ToolDefinition{
		{Name: "run", Kind: "action", Shadows: "bash"},
		{Name: "health", Kind: "read", UIOnly: true, InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`)},
	}}, nil
}

type observationClient struct {
	contract.Runtime
	calls atomic.Int32
	stale atomic.Bool
	after func()
	actor contract.Actor
}

func (c *observationClient) AdmitObservation(g uint64, i string) (func(), error) {
	if c.stale.Load() || g != 1 || i != "inc" {
		return nil, addonruntime.ErrNotSent
	}
	return func() {}, nil
}
func (c *observationClient) ReadObservation(_ context.Context, e contract.Envelope, q string) (contract.ExecutionStatus, error) {
	c.calls.Add(1)
	if c.stale.Load() {
		return contract.ExecutionStatus{}, addonruntime.ErrNotSent
	}
	if e.Authority.Actor != c.actor || e.Request.OperationID == q || e.Request.OperationID == "" || e.Request.ResourceID != "" || string(e.Request.Arguments) != `{"tool":"health"}` {
		return contract.ExecutionStatus{}, ErrForbidden
	}
	if c.after != nil {
		c.after()
	}
	return contract.ExecutionStatus{Readiness: "available", Isolation: "isolated"}, nil
}

var _ = Describe("exact owner execution observation", func() {
	var g *Gateway
	var c *observationClient
	var r ExecutionStatusRequest
	BeforeEach(func() {
		c = &observationClient{actor: frontActor}
		g = &Gateway{Revalidate: func(context.Context, contract.Actor) error { return nil }, Admission: &addonAdmitted{}, Runtime: currentObservation{c}, Tools: observationCaps{toolSet{gen: 1, tools: map[string][]string{"sample": {"run", "health"}}}}, AuthorizeExecutionStatus: func(_ context.Context, a contract.Actor, b ExecutionStatusBinding) error {
			if a != frontActor || b.CurrentSelection != "rev" {
				return ErrForbidden
			}
			return nil
		}}
		r = ExecutionStatusRequest{Version: 1, Binding: ExecutionStatusBinding{Actor: frontActor, AddonID: "sample", Generation: 1, Incarnation: "inc", ExecutionTool: "run", StatusTool: "health", CurrentSelection: "rev"}, ObservationID: "q"}
	})

	DescribeTable("rejects invalid raw execution declarations", func(field, value string) {
		caps, err := g.Tools.(observationCaps).DeclaredCapabilities(context.Background(), "sample", 1)
		Expect(err).NotTo(HaveOccurred())
		switch field {
		case "schema":
			caps.ToolDefinitions[1].InputSchema = json.RawMessage(value)
		case "resource":
			caps.ToolDefinitions[1].InputSchema = json.RawMessage(`{"type":"object","properties":{"resourceId":{"type":"string"}},"required":["resourceId"],"additionalProperties":false}`)
		case "admin":
			caps.AdminTools = []string{"health"}
		case "status-ui":
			caps.ToolDefinitions[1].UIOnly = false
		case "status-kind":
			caps.ToolDefinitions[1].Kind = value
		case "action-ui":
			caps.ToolDefinitions[0].UIOnly = true
		case "action-kind":
			caps.ToolDefinitions[0].Kind = value
		case "shadow":
			caps.ToolDefinitions[0].Shadows = value
		}
		g.Tools = rawObservationCaps{caps: caps}
		out, err := g.ExecutionStatusRead(context.Background(), frontActor, r)
		Expect(err).To(MatchError(ErrForbidden))
		Expect(out.Status.Readiness).To(Equal("unknown"))
		Expect(c.calls.Load()).To(BeZero())
	},
		Entry("wrong object", "schema", `{"type":"string","additionalProperties":false}`),
		Entry("additional properties true", "schema", `{"type":"object","additionalProperties":true}`),
		Entry("additional properties absent", "schema", `{"type":"object"}`),
		Entry("required", "schema", `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`),
		Entry("enum", "schema", `{"type":"object","enum":["x"],"additionalProperties":false}`),
		Entry("unknown schema keyword", "schema", `{"type":"object","minProperties":1,"additionalProperties":false}`),
		Entry("malformed required", "schema", `{"type":"object","required":null,"additionalProperties":false}`),
		Entry("resource", "resource", ""),
		Entry("admin", "admin", ""),
		Entry("status uiOnly", "status-ui", ""),
		Entry("status kind", "status-kind", "action"),
		Entry("action uiOnly", "action-ui", ""),
		Entry("action kind", "action-kind", "read"),
		Entry("shadow", "shadow", "other"),
	)

	It("reads the exact uiOnly status tool locally and over authenticated mTLS", func() {
		_, cs := carrierFixture(func(car *Carrier) http.Handler {
			h, e := car.OwnerHandler(g)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		for _, gw := range []*Gateway{g, {Forward: cs["front-a"], Revalidate: g.Revalidate}} {
			out, e := gw.ExecutionStatusRead(context.Background(), frontActor, r)
			Expect(e).NotTo(HaveOccurred(), "forward=%v calls=%d", gw.Forward != nil, c.calls.Load())
			Expect(out.Binding).To(Equal(r.Binding))
			Expect(out.ObservationID).To(Equal("q"))
			Expect(out.Status.Readiness).To(Equal("available"))
		}
		Expect(c.calls.Load()).To(Equal(int32(2)))
	})
	It("refuses forged identities tools current selections and incarnations without dispatch", func() {
		for _, change := range []func(*ExecutionStatusRequest){func(r *ExecutionStatusRequest) { r.Binding.Actor.UserID = "forged" }, func(r *ExecutionStatusRequest) { r.Binding.StatusTool = "settings" }, func(r *ExecutionStatusRequest) { r.Binding.CurrentSelection = "old" }, func(r *ExecutionStatusRequest) { r.Binding.Incarnation = "old" }, func(r *ExecutionStatusRequest) { r.Binding.Generation = 2 }, func(r *ExecutionStatusRequest) { r.Binding.ExecutionTool = "other" }, func(r *ExecutionStatusRequest) { r.Version = 2 }, func(r *ExecutionStatusRequest) { r.ObservationID = "" }} {
			bad := r
			change(&bad)
			out, e := g.ExecutionStatusRead(context.Background(), frontActor, bad)
			Expect(e).To(HaveOccurred())
			Expect(out.Status.Readiness).To(Equal("unknown"))
		}
		Expect(c.calls.Load()).To(BeZero())
	})
	It("rejects stale carrier replies and frontend authority fields without dispatch", func() {
		_, cs := carrierFixture(func(car *Carrier) http.Handler {
			h, e := car.OwnerHandler(g)
			Expect(e).NotTo(HaveOccurred())
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path != "/addons/forward" {
					h.ServeHTTP(w, req)
					return
				}
				out := observationUnknown(r)
				out.Binding.Incarnation = "replacement"
				out.Status = contract.ExecutionStatus{Readiness: "available", Isolation: "isolated"}
				Expect(json.NewEncoder(w).Encode(out)).To(Succeed())
			})
		})
		front := &Gateway{Forward: cs["front-a"], Revalidate: g.Revalidate}
		out, e := front.ExecutionStatusRead(context.Background(), frontActor, r)
		Expect(e).To(HaveOccurred())
		Expect(out.Status.Readiness).To(Equal("unknown"))
		Expect(c.calls.Load()).To(BeZero())
		for _, extra := range []string{`"admin":true`, `"grants":["workspace."]`, `"operationId":"chosen"`, `"arguments":{}`} {
			raw, _ := json.Marshal(r)
			raw = append(raw[:len(raw)-1], []byte(","+extra+"}")...)
			var decoded ExecutionStatusRequest
			Expect(decode(strings.NewReader(string(raw)), &decoded)).To(HaveOccurred())
		}
	})
	It("refuses forged observations through the real carrier and ignores ordinary dispatch attempts", func() {
		_, cs := carrierFixture(func(car *Carrier) http.Handler {
			h, e := car.OwnerHandler(g)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		for _, bad := range []ExecutionStatusRequest{
			{Version: 1, Binding: ExecutionStatusBinding{Actor: frontActor}, ObservationID: "q"},
			{Version: 1, Binding: r.Binding, ObservationID: ""},
		} {
			_, e := cs["front-a"].executionStatus(context.Background(), frontActor, bad)
			Expect(e).To(HaveOccurred())
		}
		bad := r
		bad.Binding.Actor.UserID = "forged"
		_, e := cs["front-a"].executionStatus(context.Background(), frontActor, bad)
		Expect(e).To(HaveOccurred())
		_, e = g.Dispatch(context.Background(), frontActor, contract.Request{Action: executionStatusAction})
		Expect(e).To(HaveOccurred())
		Expect(c.calls.Load()).To(BeZero())
	})
	It("does not relay a captured old binding to a same-generation replacement", func() {
		replacement := &observationClient{actor: frontActor}
		g.Runtime = restartObservation{old: c, replacement: replacement}
		_, cs := carrierFixture(func(car *Carrier) http.Handler {
			h, e := car.OwnerHandler(g)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		out, e := (&Gateway{Forward: cs["front-a"], Revalidate: g.Revalidate}).ExecutionStatusRead(context.Background(), frontActor, r)
		Expect(e).To(HaveOccurred())
		Expect(out.Status.Readiness).To(Equal("unknown"))
		Expect(c.calls.Load()).To(BeZero())
		Expect(replacement.calls.Load()).To(BeZero())
	})
	It("settles parent cancellation and owner permission loss as unknown", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c.after = func() {
			g.AuthorizeExecutionStatus = func(context.Context, contract.Actor, ExecutionStatusBinding) error { return ErrForbidden }
		}
		out, e := g.ExecutionStatusRead(ctx, frontActor, r)
		Expect(e).To(HaveOccurred())
		Expect(out.Status.Readiness).To(Equal("unknown"))
		out, e = g.ExecutionStatusRead(context.Background(), frontActor, r)
		Expect(e).To(HaveOccurred())
		Expect(out.Status.Readiness).To(Equal("unknown"))
	})

	It("does not grant owner selection authority through wire capability support", func() {
		g.AuthorizeExecutionStatus = nil
		_, cs := carrierFixture(func(car *Carrier) http.Handler {
			h, e := car.OwnerHandler(g)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		caps, e := cs["front-a"].Capabilities(context.Background())
		Expect(e).NotTo(HaveOccurred())
		Expect(caps.Supports(executionStatusAction)).To(BeTrue())
		out, e := (&Gateway{Forward: cs["front-a"], Revalidate: g.Revalidate}).ExecutionStatusRead(context.Background(), frontActor, r)
		Expect(e).To(HaveOccurred())
		Expect(out.Status.Readiness).To(Equal("unknown"))
		Expect(c.calls.Load()).To(BeZero())
	})
	It("rejects duplicate noncanonical and nested authority keys in observation DTOs", func() {
		raw, _ := json.Marshal(r)
		for _, bad := range []string{
			strings.Replace(string(raw), `"version":1`, `"version":1,"version":1`, 1),
			strings.Replace(string(raw), `"binding":`, `"Binding":`, 1),
			strings.Replace(string(raw), `"actor":{`, `"actor":{"admin":true,`, 1),
			strings.Replace(string(raw), `"userId":`, `"UserId":`, 1),
		} {
			var decoded ExecutionStatusRequest
			Expect(json.Unmarshal([]byte(bad), &decoded)).To(HaveOccurred())
		}
	})

	It("withdraws a positive late reply when exact-client admission becomes stale", func() {
		_, cs := carrierFixture(func(car *Carrier) http.Handler {
			h, e := car.OwnerHandler(g)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		for _, gw := range []*Gateway{g, {Forward: cs["front-a"], Revalidate: g.Revalidate}} {
			c.stale.Store(false)
			c.after = func() { c.stale.Store(true) }
			out, e := gw.ExecutionStatusRead(context.Background(), frontActor, r)
			Expect(e).To(HaveOccurred())
			Expect(out.Status.Readiness).To(Equal("unknown"))
		}
	})
})

type currentObservation struct{ c *observationClient }

func (s currentObservation) Runtime(context.Context, string) (addonruntime.Instance, error) {
	return addonruntime.Instance{Generation: 1, Incarnation: "inc", Client: s.c}, nil
}

// Snapshot is captured first; withdrawal occurs before gateway admission.
type restartObservation struct{ old, replacement *observationClient }

func (s restartObservation) Runtime(context.Context, string) (addonruntime.Instance, error) {
	s.old.stale.Store(true)
	return addonruntime.Instance{Generation: 1, Incarnation: "inc", Client: s.old}, nil
}
