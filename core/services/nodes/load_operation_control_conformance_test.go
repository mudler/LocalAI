// SPDX-License-Identifier: MIT
package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// sentRequest is one control request a carrier put on the wire, decoded.
type sentRequest struct {
	Verb    string
	Timeout time.Duration
	Install workerctl.BackendInstallRequest
	Stop    workerctl.ModelStopRequest
	Op      workerctl.OperationRequest
	Unload  workerctl.ModelUnloadRequest
}

// loadOperationHarness drives one carrier for the conformance of
// LoadOperationControl. A second carrier plugs in by implementing it and being
// added to loadOperationCarriers. The harness knows how a failure looks on its
// own wire; the specs only say which of the four conditions it stands for.
type loadOperationHarness interface {
	// Control is the carrier under test.
	Control() LoadOperationControl
	// NoRoute makes every later call find no route to the node.
	NoRoute()
	// TimesOut makes every later call get no reply in time.
	TimesOut()
	// WorkerRefuses makes the worker answer every call with its own refusal.
	WorkerRefuses()
	// WorkerAnswers makes the worker answer every call with success.
	WorkerAnswers()
	// Sent returns the requests the carrier sent, in order.
	Sent() []sentRequest
}

var loadOperationCarriers = map[string]func() loadOperationHarness{
	"the NATS carrier": newNATSLoadOperationHarness,
}

type natsLoadOperationHarness struct {
	mc      *scriptedMessagingClient
	adapter *RemoteUnloaderAdapter
}

func newNATSLoadOperationHarness() loadOperationHarness {
	mc := newScriptedMessagingClient()
	return &natsLoadOperationHarness{mc: mc, adapter: NewRemoteUnloaderAdapter(&fakeModelLocator{}, mc, 3*time.Minute, 15*time.Minute)}
}

func (h *natsLoadOperationHarness) Control() LoadOperationControl { return h.adapter }

func (h *natsLoadOperationHarness) subjects() []string {
	const node = conformanceNode
	return []string{
		messaging.SubjectNodeBackendInstall(node), messaging.SubjectNodeModelStop(node),
		messaging.SubjectNodeModelOp(node), messaging.SubjectNodeModelUnload(node),
	}
}

func (h *natsLoadOperationHarness) NoRoute() {
	for _, s := range h.subjects() {
		h.mc.scriptNoResponders(s)
	}
}

func (h *natsLoadOperationHarness) TimesOut() {
	for _, s := range h.subjects() {
		h.mc.scriptErr(s, nats.ErrTimeout)
	}
}

func (h *natsLoadOperationHarness) WorkerRefuses() {
	const node = conformanceNode
	h.mc.scriptReply(messaging.SubjectNodeBackendInstall(node), workerctl.BackendInstallReply{Success: false, Error: "disk full"})
	h.mc.scriptReply(messaging.SubjectNodeModelStop(node), workerctl.ModelStopReply{Matched: true, Error: "does not belong to operation"})
	h.mc.scriptReply(messaging.SubjectNodeModelOp(node), workerctl.OperationReply{Unknown: []string{"op"}})
	h.mc.scriptReply(messaging.SubjectNodeModelUnload(node), workerctl.ModelUnloadReply{Success: false, Error: "process was replaced during unload"})
}

func (h *natsLoadOperationHarness) WorkerAnswers() {
	const node = conformanceNode
	h.mc.scriptReply(messaging.SubjectNodeBackendInstall(node), workerctl.BackendInstallReply{Success: true, Address: "127.0.0.1:9001", ProcessInstance: "i", ReportsOperations: true})
	h.mc.scriptReply(messaging.SubjectNodeModelStop(node), workerctl.ModelStopReply{Matched: true, Terminated: true})
	h.mc.scriptReply(messaging.SubjectNodeModelOp(node), workerctl.OperationReply{Renewed: []string{"op"}, Completed: []string{"op"}})
	h.mc.scriptReply(messaging.SubjectNodeModelUnload(node), workerctl.ModelUnloadReply{Success: true})
}

func (h *natsLoadOperationHarness) Sent() []sentRequest {
	h.mc.mu.Lock()
	defer h.mc.mu.Unlock()
	const node = conformanceNode
	var out []sentRequest
	for _, c := range h.mc.calls {
		r := sentRequest{Timeout: c.Timeout}
		switch c.Subject {
		case messaging.SubjectNodeBackendInstall(node):
			r.Verb = "install"
			Expect(json.Unmarshal(c.Data, &r.Install)).To(Succeed())
		case messaging.SubjectNodeModelStop(node):
			r.Verb = "stop"
			Expect(json.Unmarshal(c.Data, &r.Stop)).To(Succeed())
		case messaging.SubjectNodeModelOp(node):
			r.Verb = "op"
			Expect(json.Unmarshal(c.Data, &r.Op)).To(Succeed())
		case messaging.SubjectNodeModelUnload(node):
			r.Verb = "unload"
			Expect(json.Unmarshal(c.Data, &r.Unload)).To(Succeed())
		}
		out = append(out, r)
	}
	return out
}

const conformanceNode = "11111111-2222-3333-4444-555555555555"

// Every carrier of LoadOperationControl must pass these. They pin the contract
// in interfaces.go, so a carrier can be written from the interface alone.
var _ = Describe("LoadOperationControl conformance", func() {
	for name, newHarness := range loadOperationCarriers {
		Describe(name, func() {
			var h loadOperationHarness

			BeforeEach(func() { h = newHarness() })

			// call is one method, with the answer it should give a worker that
			// answers.
			type call struct {
				verb string
				run  func() error
			}
			calls := func() []call {
				c := h.Control()
				replica := NodeModel{ModelName: "m", ReplicaIndex: 1, Address: "127.0.0.1:9001"}
				return []call{
					{"install", func() error {
						_, err := c.InstallBackendOp(conformanceNode, "llama-cpp", "m", "", 1, "", "op", time.Hour, nil)
						return err
					}},
					{"stop", func() error {
						_, err := c.StopLoadOperation(context.Background(), conformanceNode, workerctl.ModelStopRequest{
							ModelName: "m", ProcessKey: "m#1", ExpectedAddress: "127.0.0.1:9001", OperationID: "op", Force: true})
						return err
					}},
					{"op", func() error {
						_, err := c.OperationControl(conformanceNode, workerctl.OperationRequest{Renew: []string{"op"}})
						return err
					}},
					{"unload", func() error { return c.UnloadReplica(conformanceNode, replica) }},
				}
			}

			It("reports no route as ErrNoRoute, and only that", func() {
				h.NoRoute()
				for _, c := range calls() {
					err := c.run()
					Expect(errors.Is(err, ErrNoRoute)).To(BeTrue(), "%s: %v", c.verb, err)
				}
			})

			It("does not report a timeout as ErrNoRoute", func() {
				h.TimesOut()
				for _, c := range calls() {
					err := c.run()
					Expect(err).To(HaveOccurred(), c.verb)
					Expect(errors.Is(err, ErrNoRoute)).To(BeFalse(), "%s: a slow worker is not an absent one", c.verb)
				}
			})

			It("never reports a worker's own refusal as ErrNoRoute", func() {
				h.WorkerRefuses()
				for _, c := range calls() {
					err := c.run()
					Expect(errors.Is(err, ErrNoRoute)).To(BeFalse(), "%s: the worker answered, so it is present", c.verb)
				}
			})

			It("hands a worker's refusal back in the reply, with no error", func() {
				h.WorkerRefuses()
				c := h.Control()
				install, err := c.InstallBackendOp(conformanceNode, "b", "m", "", 0, "", "op", time.Hour, nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(install.Success).To(BeFalse())
				stop, err := c.StopLoadOperation(context.Background(), conformanceNode, workerctl.ModelStopRequest{ProcessKey: "m#0", OperationID: "op"})
				Expect(err).ToNot(HaveOccurred())
				Expect(stop.Error).ToNot(BeEmpty())
				ops, err := c.OperationControl(conformanceNode, workerctl.OperationRequest{Renew: []string{"op"}})
				Expect(err).ToNot(HaveOccurred())
				Expect(ops.Unknown).To(ConsistOf("op"))
			})

			It("gives every call its own bounded timeout, and renewals the shortest", func() {
				h.WorkerAnswers()
				for _, c := range calls() {
					Expect(c.run()).To(Succeed(), c.verb)
				}
				for _, s := range h.Sent() {
					Expect(s.Timeout).To(BeNumerically(">", 0), s.Verb)
					if s.Verb == "op" {
						Expect(s.Timeout).To(BeNumerically("<=", 5*time.Second),
							"a renewal that takes longer than its cadence cannot hold the kill TTL")
					}
				}
			})

			It("carries the operation, the process and the deadline to the worker", func() {
				h.WorkerAnswers()
				for _, c := range calls() {
					Expect(c.run()).To(Succeed(), c.verb)
				}
				sent := map[string]sentRequest{}
				for _, s := range h.Sent() {
					sent[s.Verb] = s
				}
				Expect(sent["install"].Install.OperationID).To(Equal("op"))
				Expect(sent["install"].Install.DeadlineMs).To(Equal(time.Hour.Milliseconds()), "a duration, not a timestamp")
				Expect(sent["stop"].Stop.OperationID).To(Equal("op"))
				Expect(sent["stop"].Stop.ProcessKey).To(Equal("m#1"))
				Expect(sent["stop"].Stop.ExpectedAddress).To(Equal("127.0.0.1:9001"))
				Expect(sent["op"].Op.Renew).To(ConsistOf("op"))
				Expect(sent["unload"].Unload.Address).To(Equal("127.0.0.1:9001"))
			})

			It("stops idempotently: a repeated stop is answered, not refused", func() {
				h.WorkerAnswers()
				c := h.Control()
				for range 2 {
					reply, err := c.StopLoadOperation(context.Background(), conformanceNode, workerctl.ModelStopRequest{ProcessKey: "m#0", OperationID: "op"})
					Expect(err).ToNot(HaveOccurred())
					Expect(reply.Terminated).To(BeTrue())
				}
			})

			It("refuses a stop with no operation id, and an unload with no address, without sending anything", func() {
				h.WorkerAnswers()
				c := h.Control()
				_, err := c.StopLoadOperation(context.Background(), conformanceNode, workerctl.ModelStopRequest{ProcessKey: "m#0"})
				Expect(err).To(HaveOccurred())
				Expect(c.UnloadReplica(conformanceNode, NodeModel{ModelName: "m"})).To(Succeed())
				Expect(h.Sent()).To(BeEmpty(), "a carrier must never ask a worker to pick a process")
			})
		})
	}
})
