package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// --- Fakes ---

// fakeModelLocator implements ModelLocator with configurable node lists.
type fakeModelLocator struct {
	nodes           []BackendNode
	findErr         error
	getErr          error
	removedPairs    []modelNodePair   // records RemoveNodeModel calls
	removedReplicas []modelReplicaRef // records RemoveNodeModel calls including the replica index
}

type modelNodePair struct {
	nodeID    string
	modelName string
}

// modelReplicaRef records a row removal at full replica granularity.
// modelNodePair drops the index because RemoveAllNodeModelReplicas has none;
// the backend delete/upgrade paths address exactly one replica row, so an
// assertion that ignored the index could not tell a correct removal from one
// that wiped a sibling replica still serving traffic.
type modelReplicaRef struct {
	nodeID       string
	modelName    string
	replicaIndex int
}

func (f *fakeModelLocator) FindNodesWithModel(_ context.Context, _ string) ([]BackendNode, error) {
	return f.nodes, f.findErr
}

// Get answers out of the same node list the locator hands out, so a spec that
// registers an AGENT node gets an agent node back and the carrier split is
// driven by the fixture rather than by a second thing to keep in step.
func (f *fakeModelLocator) Get(_ context.Context, nodeID string) (*BackendNode, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for i := range f.nodes {
		if f.nodes[i].ID == nodeID {
			return &f.nodes[i], nil
		}
	}
	return nil, errors.New("no such node")
}

func (f *fakeModelLocator) RemoveNodeModel(_ context.Context, nodeID, modelName string, replicaIndex int) error {
	f.removedPairs = append(f.removedPairs, modelNodePair{nodeID, modelName})
	f.removedReplicas = append(f.removedReplicas, modelReplicaRef{nodeID, modelName, replicaIndex})
	return nil
}

func (f *fakeModelLocator) RemoveAllNodeModelReplicas(_ context.Context, nodeID, modelName string) error {
	f.removedPairs = append(f.removedPairs, modelNodePair{nodeID, modelName})
	return nil
}

// fakeMessagingClient implements messaging.MessagingClient, recording Publish
// and Request calls so we can assert on subjects and payloads.
//
// NO control verb reaches it any more, and the RemoteUnloaderAdapter cannot be
// handed one: it holds no publisher. It survives here for the staging-progress
// broadcasts in staging_progress_broadcast_test.go, which are cross-replica
// events rather than anything addressed to a worker.
type fakeMessagingClient struct {
	mu           sync.Mutex
	published    []publishCall
	publishErr   error // error to return from Publish
	requestReply []byte
	requestErr   error
	requestCalls []requestCall
}

type publishCall struct {
	Subject string
	Data    []byte
}

type requestCall struct {
	Subject string
	Data    []byte
	Timeout time.Duration
}

func (f *fakeMessagingClient) Publish(subject string, data any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var raw []byte
	if data != nil {
		var err error
		raw, err = json.Marshal(data)
		if err != nil {
			return err
		}
	}
	f.published = append(f.published, publishCall{Subject: subject, Data: raw})
	return f.publishErr
}

func (f *fakeMessagingClient) Subscribe(_ string, _ func([]byte)) (messaging.Subscription, error) {
	return &fakeSubscription{}, nil
}

func (f *fakeMessagingClient) QueueSubscribe(_ string, _ string, _ func([]byte)) (messaging.Subscription, error) {
	return &fakeSubscription{}, nil
}

func (f *fakeMessagingClient) QueueSubscribeReply(_ string, _ string, _ func(data []byte, reply func([]byte))) (messaging.Subscription, error) {
	return &fakeSubscription{}, nil
}

func (f *fakeMessagingClient) SubscribeReply(_ string, _ func(data []byte, reply func([]byte))) (messaging.Subscription, error) {
	return &fakeSubscription{}, nil
}

func (f *fakeMessagingClient) Request(subject string, data []byte, timeout time.Duration) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requestCalls = append(f.requestCalls, requestCall{Subject: subject, Data: data, Timeout: timeout})
	return f.requestReply, f.requestErr
}

func (f *fakeMessagingClient) IsConnected() bool { return true }
func (f *fakeMessagingClient) Close()            {}

type fakeSubscription struct{}

func (f *fakeSubscription) Unsubscribe() error { return nil }

// stopPayloadOf decodes the backend.stop body a worker actually received on
// one (node, verb) key. Reading the payload back off the transport is what
// separates "a stop was sent" from "the RIGHT stop was sent"; the two node
// types below are distinguished only by which key they arrive on.
func stopPayloadOf(s *scriptedControlWorkers, key string) messaging.BackendStopRequest {
	GinkgoHelper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.calls {
		if c.Subject != key {
			continue
		}
		var payload messaging.BackendStopRequest
		Expect(json.Unmarshal(c.Data, &payload)).To(Succeed())
		return payload
	}
	Fail("no backend.stop reached " + key)
	return messaging.BackendStopRequest{}
}

// --- Tests ---

var _ = Describe("RemoteUnloaderAdapter", func() {
	var (
		locator *fakeModelLocator
		workers *scriptedControlWorkers
		adapter *RemoteUnloaderAdapter
	)

	BeforeEach(func() {
		locator = &fakeModelLocator{}
		workers = newScriptedControlWorkers()
		adapter = NewRemoteUnloaderAdapter(locator, workers.controlClient(), 3*time.Minute, 15*time.Minute)
	})

	// scriptStop lets a backend node accept the tunnelled backend.stop, which
	// answers 204 and therefore carries no body.
	scriptStop := func(nodeID string) {
		workers.scriptRawReply(controlKey(nodeID, workerctl.PathBackendStop), []byte(`{}`))
	}

	// HasRemoteModel carries the distinction that UnloadRemoteModel
	// deliberately does not, so ShutdownModel can answer 404 for a model that
	// is loaded neither locally nor anywhere in the cluster without making the
	// shared unload path fail for every idempotent cleanup caller.
	Describe("HasRemoteModel", func() {
		It("reports false when no node has the model", func() {
			locator.nodes = nil
			loaded, err := adapter.HasRemoteModel(context.Background(), "my-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(loaded).To(BeFalse())
		})

		It("reports true when a node has the model", func() {
			locator.nodes = []BackendNode{{ID: "node-1", Name: "worker-1"}}
			loaded, err := adapter.HasRemoteModel(context.Background(), "my-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(loaded).To(BeTrue())
		})

		It("surfaces a registry failure instead of reporting absence", func() {
			// An unreachable registry is not evidence that the model is gone;
			// reporting false would let ShutdownModel answer a confident 404
			// on the strength of a failed lookup.
			locator.findErr = errors.New("registry unavailable")
			_, err := adapter.HasRemoteModel(context.Background(), "my-model")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("UnloadRemoteModel", func() {
		It("with no nodes returns nil", func() {
			// Unloading is idempotent: cleanup paths (model deletion, config
			// edits, watchdog eviction) legitimately run against an already
			// unloaded model, and turning that into an error wedges the
			// watchdog's LRU reclaimer, which only untracks a model when
			// shutdown reports success. The same contract is pinned end to end
			// by "should be no-op for models not on any node" in
			// tests/e2e/distributed/node_lifecycle_test.go — keep them in step.
			locator.nodes = nil
			Expect(adapter.UnloadRemoteModel("my-model")).To(Succeed())
			Expect(workers.callSubjects()).To(BeEmpty())
		})

		It("stops the backend on every node holding the model, over their tunnels", func() {
			locator.nodes = []BackendNode{
				{ID: "node-1", Name: "worker-1", NodeType: NodeTypeBackend},
				{ID: "node-2", Name: "worker-2", NodeType: NodeTypeBackend},
			}
			scriptStop("node-1")
			scriptStop("node-2")

			Expect(adapter.UnloadRemoteModel("llama")).To(Succeed())

			Expect(workers.callSubjects()).To(Equal([]string{
				controlKey("node-1", workerctl.PathBackendStop),
				controlKey("node-2", workerctl.PathBackendStop),
			}))

			// Should have removed the model from each node in the registry.
			Expect(locator.removedPairs).To(HaveLen(2))
			Expect(locator.removedPairs[0]).To(Equal(modelNodePair{"node-1", "llama"}))
			Expect(locator.removedPairs[1]).To(Equal(modelNodePair{"node-2", "llama"}))
		})

		It("continues when one node fails", func() {
			locator.nodes = []BackendNode{
				{ID: "node-fail", Name: "worker-fail", NodeType: NodeTypeBackend},
				{ID: "node-ok", Name: "worker-ok", NodeType: NodeTypeBackend},
			}
			workers.scriptUnroutable("node-fail")
			scriptStop("node-ok")

			Expect(adapter.UnloadRemoteModel("llama")).To(HaveOccurred())

			// The second node should still have been processed. The first
			// node's stop errored, so its row was NOT dropped: a row deleted
			// on a route this frontend could not open is a model reclaimed
			// while it is still loaded.
			Expect(locator.removedPairs).To(HaveLen(1))
			Expect(locator.removedPairs[0].nodeID).To(Equal("node-ok"))
		})

		It("propagates forced shutdown to every worker", func() {
			locator.nodes = []BackendNode{{ID: "node-1", Name: "worker-1", NodeType: NodeTypeBackend}}
			scriptStop("node-1")

			Expect(adapter.UnloadRemoteModelContext(context.Background(), "llama", true)).To(Succeed())

			workers.mu.Lock()
			defer workers.mu.Unlock()
			Expect(workers.calls).To(HaveLen(1))
			var payload messaging.BackendStopRequest
			Expect(json.Unmarshal(workers.calls[0].Data, &payload)).To(Succeed())
			Expect(payload).To(Equal(messaging.BackendStopRequest{Backend: "llama", Force: true}))
		})

		// This unload spans BOTH kinds of worker, and the point is that it no
		// longer has to care which is which. It used to: an agent node's stop
		// was published and a backend node's was called, so an unload touching
		// one of each exercised two carriers. A single scripted node could not
		// have caught a frontend that picked one carrier for the whole unload.
		It("takes the same route for every node in one unload, whatever their types", func() {
			locator.nodes = []BackendNode{
				{ID: "agent-1", Name: "agent", NodeType: NodeTypeAgent},
				{ID: "backend-1", Name: "gpu-1", NodeType: NodeTypeBackend},
			}
			scriptStop("agent-1")
			scriptStop("backend-1")

			Expect(adapter.UnloadRemoteModelContext(context.Background(), "llama", false)).To(Succeed())

			Expect(workers.callSubjects()).To(ConsistOf(
				controlKey("agent-1", workerctl.PathBackendStop),
				controlKey("backend-1", workerctl.PathBackendStop),
			))
			// Both rows dropped, which is the negative control: a stop the
			// worker never answered fails, and a failed stop keeps its row.
			Expect(locator.removedPairs).To(HaveLen(2))
		})
	})

	// backend.stop used to be the one verb of the ten decided by the KIND of
	// worker: an agent node's went to the bus, a backend node's over the
	// tunnel. Both kinds serve the same control path now, so the two node
	// types are asserted SEPARATELY rather than as one parameterised case. A
	// single case would show that some node gets the path; only two show that
	// the two kinds no longer differ, which is the whole content of the change.
	Describe("StopBackend and node type", func() {
		It("sends a backend stop to a BACKEND node over the tunnel, naming the backend", func() {
			locator.nodes = []BackendNode{{ID: "backend-1", Name: "gpu-1", NodeType: NodeTypeBackend}}
			scriptStop("backend-1")

			Expect(adapter.StopBackend("backend-1", "llama-backend")).To(Succeed())

			Expect(workers.callSubjects()).To(ContainElement(controlKey("backend-1", workerctl.PathBackendStop)))
			Expect(stopPayloadOf(workers, controlKey("backend-1", workerctl.PathBackendStop))).
				To(Equal(messaging.BackendStopRequest{Backend: "llama-backend"}))
		})

		It("sends a backend stop to an AGENT node over the tunnel too, on the same path", func() {
			locator.nodes = []BackendNode{{ID: "agent-1", Name: "agent", NodeType: NodeTypeAgent}}
			scriptStop("agent-1")

			Expect(adapter.StopBackend("agent-1", "llama-backend")).To(Succeed())

			Expect(workers.callSubjects()).To(ContainElement(controlKey("agent-1", workerctl.PathBackendStop)))
			Expect(stopPayloadOf(workers, controlKey("agent-1", workerctl.PathBackendStop))).
				To(Equal(messaging.BackendStopRequest{Backend: "llama-backend"}))
		})

		It("does not read the node's type at all before stopping a backend on it", func() {
			// The carrier no longer depends on what kind of worker this is, so
			// the lookup that used to choose it is gone. A registry that cannot
			// answer must therefore not stop a backend from being stopped: the
			// stop is issued on the node's own tunnel regardless.
			locator.getErr = errors.New("database is down")
			scriptStop("unknown-1")

			Expect(adapter.StopBackend("unknown-1", "llama-backend")).To(Succeed())

			Expect(workers.callSubjects()).To(ContainElement(controlKey("unknown-1", workerctl.PathBackendStop)))
		})

		It("with an empty backend asks the worker to stop everything", func() {
			locator.nodes = []BackendNode{{ID: "backend-1", Name: "gpu-1", NodeType: NodeTypeBackend}}
			scriptStop("backend-1")

			Expect(adapter.StopBackend("backend-1", "")).To(Succeed())

			workers.mu.Lock()
			defer workers.mu.Unlock()
			var payload messaging.BackendStopRequest
			Expect(json.Unmarshal(workers.calls[0].Data, &payload)).To(Succeed())
			// An empty Backend is what the worker reads as "stop everything";
			// see decodeBackendStopRequest.
			Expect(payload.Backend).To(BeEmpty())
			Expect(payload.Force).To(BeFalse())
		})

		It("names the backend when one is given", func() {
			locator.nodes = []BackendNode{{ID: "backend-1", Name: "gpu-1", NodeType: NodeTypeBackend}}
			scriptStop("backend-1")

			Expect(adapter.StopBackend("backend-1", "llama-backend")).To(Succeed())

			workers.mu.Lock()
			defer workers.mu.Unlock()
			var payload messaging.BackendStopRequest
			Expect(json.Unmarshal(workers.calls[0].Data, &payload)).To(Succeed())
			Expect(payload.Backend).To(Equal("llama-backend"))
			Expect(payload.Force).To(BeFalse())
		})
	})

	Describe("StopModelReplica", func() {
		It("requests an acknowledged stop for the exact process", func() {
			workers.scriptReply(controlKey("node-1", workerctl.PathModelStop),
				messaging.ModelStopReply{Matched: true, Terminated: true, ProcessKey: "llama#2"})
			replica := NodeModel{ModelName: "llama", ReplicaIndex: 2, WorkerLocalAddress: "127.0.0.1:5002", ConfigRevision: "rev-1"}

			reply, err := adapter.StopModelReplica(context.Background(), "node-1", replica, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(reply.Terminated).To(BeTrue())

			workers.mu.Lock()
			defer workers.mu.Unlock()
			Expect(workers.calls).To(HaveLen(1))
			Expect(workers.calls[0].Subject).To(Equal(controlKey("node-1", workerctl.PathModelStop)))

			var request messaging.ModelStopRequest
			Expect(json.Unmarshal(workers.calls[0].Data, &request)).To(Succeed())
			Expect(request).To(Equal(messaging.ModelStopRequest{
				ModelName: "llama", ProcessKey: "llama#2", ExpectedAddress: "127.0.0.1:5002", Force: true, ConfigRevision: "rev-1",
			}))
		})

		It("reports an unroutable worker without inventing a stop reply", func() {
			// A zero ModelStopReply reads as Matched=false, which the cleanup
			// path treats as "there was nothing to stop" and drops the row. It
			// must only ever be paired with an error.
			workers.scriptUnroutable("node-gone")

			reply, err := adapter.StopModelReplica(context.Background(), "node-gone", NodeModel{ModelName: "llama"}, false)
			Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeTrue())
			Expect(reply).To(Equal(messaging.ModelStopReply{}))
		})
	})

	Describe("StopNode", func() {
		It("asks the worker to shut down over its tunnel", func() {
			workers.scriptRawReply(controlKey("node-abc", workerctl.PathNodeStop), []byte(`{}`))

			Expect(adapter.StopNode("node-abc")).To(Succeed())

			Expect(workers.callSubjects()).To(Equal([]string{controlKey("node-abc", workerctl.PathNodeStop)}))
		})
	})

	Describe("DeleteModelFiles", func() {
		It("with no nodes returns nil", func() {
			locator.nodes = nil
			Expect(adapter.DeleteModelFiles("my-model")).To(Succeed())
		})

		It("continues on failure", func() {
			locator.nodes = []BackendNode{
				{ID: "node-1", Name: "w1", NodeType: NodeTypeBackend},
				{ID: "node-2", Name: "w2", NodeType: NodeTypeBackend},
			}
			// Neither node is scripted, so both answer the loud
			// unscripted-verb default. Both must still be attempted.
			Expect(adapter.DeleteModelFiles("my-model")).To(Succeed())
			Expect(workers.callSubjects()).To(Equal([]string{
				controlKey("node-1", workerctl.PathModelDelete),
				controlKey("node-2", workerctl.PathModelDelete),
			}))
		})
	})

	Describe("UnloadModelOnNode", func() {
		It("succeeds when the worker reports the model freed", func() {
			workers.scriptReply(controlKey("node-1", workerctl.PathModelUnload), messaging.ModelUnloadReply{Success: true})
			Expect(adapter.UnloadModelOnNode("node-1", "llama")).To(Succeed())
		})

		It("surfaces the worker's own refusal, which is an answer and not a lost route", func() {
			workers.scriptReply(controlKey("node-1", workerctl.PathModelUnload),
				messaging.ModelUnloadReply{Success: false, Error: "Free failed"})

			err := adapter.UnloadModelOnNode("node-1", "llama")
			Expect(err).To(MatchError(ContainSubstring("Free failed")))
			Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeFalse())
		})
	})
})

var _ = Describe("RemoteUnloaderAdapter timeout configuration", func() {
	// Each verb must be given ITS OWN configured budget, and the budget is
	// observed the only way it can be: by how long the client waits on a worker
	// that took the request and never answered. HTTP carries no deadline, and
	// the transport dials on a context of its own, so nothing on the far side
	// can see it.
	//
	// The two budgets are far apart so a swap cannot pass: install would wait
	// the upgrade budget and upgrade would wait the install one, and each
	// assertion below is on the wrong side of the divide for the other.
	const (
		installBudget = 150 * time.Millisecond
		upgradeBudget = 600 * time.Millisecond
		divide        = 400 * time.Millisecond
	)

	It("gives backend.install the configured install timeout", func() {
		workers := newScriptedControlWorkers()
		workers.scriptHang(controlKey("n1", workerctl.PathBackendInstall))
		adapter := NewRemoteUnloaderAdapter(nil, workers.controlClient(), installBudget, upgradeBudget)

		started := time.Now()
		_, err := adapter.InstallBackend("n1", "llama-cpp", "", "[]", "", "", "", 0, "", nil)
		elapsed := time.Since(started)

		Expect(errors.Is(err, galleryop.ErrWorkerStillInstalling)).To(BeTrue(),
			"a spent budget is reported as still-installing, got %v", err)
		Expect(elapsed).To(BeNumerically(">=", installBudget))
		Expect(elapsed).To(BeNumerically("<", divide))
	})

	It("gives backend.upgrade the configured upgrade timeout", func() {
		workers := newScriptedControlWorkers()
		workers.scriptHang(controlKey("n1", workerctl.PathBackendUpgrade))
		adapter := NewRemoteUnloaderAdapter(nil, workers.controlClient(), installBudget, upgradeBudget)

		started := time.Now()
		_, err := adapter.UpgradeBackend("n1", "llama-cpp", "[]", "", "", "", 0, "", nil)
		elapsed := time.Since(started)

		Expect(errors.Is(err, galleryop.ErrWorkerStillInstalling)).To(BeTrue(),
			"a spent budget is reported as still-installing, got %v", err)
		Expect(elapsed).To(BeNumerically(">", divide))
	})
})

var _ = Describe("RemoteUnloaderAdapter timeout handling", func() {
	It("reports a spent budget as still-installing, so the operation shows as running on the worker", func() {
		workers := newScriptedControlWorkers()
		workers.scriptTimeout("n1")
		adapter := NewRemoteUnloaderAdapter(nil, workers.controlClient(), 100*time.Millisecond, 1*time.Second)

		_, err := adapter.InstallBackend("n1", "vllm", "", "[]", "", "", "", 0, "", nil)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, galleryop.ErrWorkerStillInstalling)).To(BeTrue(),
			"expected wrapped ErrWorkerStillInstalling, got %v", err)
	})

	It("does NOT report an unroutable worker as still installing", func() {
		// The two are different facts and the operator UI shows them
		// differently: one keeps the queue row and pushes the retry out, the
		// other is a plain failure. Both are non-verdicts, so neither may reap.
		workers := newScriptedControlWorkers()
		workers.scriptUnroutable("n1")
		adapter := NewRemoteUnloaderAdapter(nil, workers.controlClient(), 100*time.Millisecond, 1*time.Second)

		_, err := adapter.InstallBackend("n1", "vllm", "", "[]", "", "", "", 0, "", nil)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, galleryop.ErrWorkerStillInstalling)).To(BeFalse())
		Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeTrue())
	})

	// The still-installing rule has THREE call sites: InstallBackend,
	// UpgradeBackend and the legacy force-install fallback. The first two are
	// pinned by the specs above and by the timeout-configuration pair; the
	// fallback was the unpinned one, and widening its guard to any error left
	// the whole suite green. A permanently unroutable worker on the fallback
	// path would then report as still-installing forever, which galleryop
	// treats as a soft failure: the retry is pushed out and the operator never
	// sees the error.
	It("reports a spent budget on the legacy fallback as still-installing, on the upgrade budget", func() {
		workers := newScriptedControlWorkers()
		workers.scriptHang(controlKey("n1", workerctl.PathBackendInstall))
		// Install and upgrade budgets far apart: the fallback re-fires an
		// INSTALL but is part of an upgrade, so it must wait the upgrade
		// budget. Waiting the install one would satisfy a bare
		// still-installing assertion while carrying the wrong deadline.
		adapter := NewRemoteUnloaderAdapter(nil, workers.controlClient(), 100*time.Millisecond, 500*time.Millisecond)

		started := time.Now()
		_, err := adapter.installWithForceFallback("n1", "llama-cpp", "[]", "", "", "", 0, "", nil)
		elapsed := time.Since(started)

		Expect(errors.Is(err, galleryop.ErrWorkerStillInstalling)).To(BeTrue(),
			"a spent budget is reported as still-installing, got %v", err)
		Expect(elapsed).To(BeNumerically(">=", 500*time.Millisecond))
	})

	It("does NOT report an unroutable legacy fallback as still installing", func() {
		workers := newScriptedControlWorkers()
		// The verb is not scripted, so the worker fails to SERVE it rather
		// than answering; that is a 5xx and lands under the no-route umbrella.
		adapter := NewRemoteUnloaderAdapter(nil, workers.controlClient(), time.Minute, time.Minute)

		_, err := adapter.installWithForceFallback("n1", "llama-cpp", "[]", "", "", "", 0, "", nil)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, galleryop.ErrWorkerStillInstalling)).To(BeFalse())
		Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeTrue())
	})

	It("does not read an error merely containing the words nats timeout as a timeout", func() {
		// The string match the bus carrier needed would have matched any
		// message quoting the phrase, and would have turned a permanent failure
		// into "still installing" forever: galleryop reads that as a soft
		// failure, pushes the retry out, and the operator never sees the error.
		//
		// The input has to be an ERROR carrying the phrase. A successful reply
		// whose Error field carries it comes back with a nil error and never
		// reaches the classifier at all, so a spec built on one stays green
		// with the string match restored, which is exactly what the previous
		// version of this spec did.
		workers := newScriptedControlWorkers()
		workers.scriptServerError(controlKey("n1", workerctl.PathBackendInstall),
			`the worker said "nats: timeout" in its log`)
		adapter := NewRemoteUnloaderAdapter(nil, workers.controlClient(), time.Minute, time.Minute)

		_, err := adapter.InstallBackend("n1", "vllm", "", "[]", "", "", "", 0, "", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("nats: timeout"),
			"precondition: the phrase must actually reach the timeout classifier")
		Expect(errors.Is(err, galleryop.ErrWorkerStillInstalling)).To(BeFalse())
		Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeTrue())
	})
})

var _ = Describe("RemoteUnloaderAdapter install progress streaming", func() {
	It("forwards the worker's progress lines to onProgress, in the order it wrote them", func() {
		workers := newScriptedControlWorkers()
		workers.scriptReply(controlKey("n1", workerctl.PathBackendInstall),
			messaging.BackendInstallReply{Success: true, WorkerLocalAddress: "127.0.0.1:0"})
		workers.scriptProgress(controlKey("n1", workerctl.PathBackendInstall), []messaging.BackendInstallProgressEvent{
			{OpID: "op-abc", NodeID: "n1", Backend: "vllm", FileName: "vllm.tar.zst", Current: "100 MB", Total: "1 GB", Percentage: 10},
			{OpID: "op-abc", NodeID: "n1", Backend: "vllm", FileName: "vllm.tar.zst", Current: "500 MB", Total: "1 GB", Percentage: 50},
		})

		adapter := NewRemoteUnloaderAdapter(nil, workers.controlClient(), time.Second, time.Second)
		var received []messaging.BackendInstallProgressEvent
		onProgress := func(ev messaging.BackendInstallProgressEvent) {
			// No lock, and that is an assertion in itself: the callback runs
			// synchronously on the caller's own goroutine, so a race detector
			// run would fail here if it did not.
			received = append(received, ev)
		}

		_, err := adapter.InstallBackend("n1", "vllm", "", "[]", "", "", "", 0, "op-abc", onProgress)
		Expect(err).ToNot(HaveOccurred())

		Expect(received).To(HaveLen(2))
		Expect([]float64{received[0].Percentage, received[1].Percentage}).To(Equal([]float64{10, 50}))
	})

	It("completes when the caller wants no progress at all (reconciler retry path)", func() {
		workers := newScriptedControlWorkers()
		workers.scriptReply(controlKey("n1", workerctl.PathBackendInstall), messaging.BackendInstallReply{Success: true})
		workers.scriptProgress(controlKey("n1", workerctl.PathBackendInstall), []messaging.BackendInstallProgressEvent{
			{OpID: "", NodeID: "n1", Percentage: 42},
		})

		adapter := NewRemoteUnloaderAdapter(nil, workers.controlClient(), time.Second, time.Second)
		reply, err := adapter.InstallBackend("n1", "vllm", "", "[]", "", "", "", 0, "", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(reply.Success).To(BeTrue())
	})
})
