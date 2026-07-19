package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
)

// --- Fakes ---

// fakeModelLocator implements ModelLocator with configurable node lists.
type fakeModelLocator struct {
	nodes        []BackendNode
	findErr      error
	removedPairs []modelNodePair // records RemoveNodeModel calls
}

type modelNodePair struct {
	nodeID    string
	modelName string
}

func (f *fakeModelLocator) FindNodesWithModel(_ context.Context, _ string) ([]BackendNode, error) {
	return f.nodes, f.findErr
}

func (f *fakeModelLocator) RemoveNodeModel(_ context.Context, nodeID, modelName string, _ int) error {
	f.removedPairs = append(f.removedPairs, modelNodePair{nodeID, modelName})
	return nil
}

func (f *fakeModelLocator) RemoveAllNodeModelReplicas(_ context.Context, nodeID, modelName string) error {
	f.removedPairs = append(f.removedPairs, modelNodePair{nodeID, modelName})
	return nil
}

// fakeMessagingClient implements messaging.MessagingClient, recording Publish
// and Request calls so we can assert on subjects and payloads.
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

// --- Tests ---

var _ = Describe("RemoteUnloaderAdapter", func() {
	var (
		locator *fakeModelLocator
		mc      *fakeMessagingClient
		adapter *RemoteUnloaderAdapter
	)

	BeforeEach(func() {
		locator = &fakeModelLocator{}
		mc = &fakeMessagingClient{}
		adapter = NewRemoteUnloaderAdapter(locator, mc, 3*time.Minute, 15*time.Minute)
	})

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
			Expect(mc.published).To(BeEmpty())
		})

		It("broadcasts to all nodes with model", func() {
			locator.nodes = []BackendNode{
				{ID: "node-1", Name: "worker-1"},
				{ID: "node-2", Name: "worker-2"},
			}
			Expect(adapter.UnloadRemoteModel("llama")).To(Succeed())

			// Should have published a StopBackend for each node.
			Expect(mc.published).To(HaveLen(2))
			Expect(mc.published[0].Subject).To(Equal(messaging.SubjectNodeBackendStop("node-1")))
			Expect(mc.published[1].Subject).To(Equal(messaging.SubjectNodeBackendStop("node-2")))

			// Should have removed the model from each node in the registry.
			Expect(locator.removedPairs).To(HaveLen(2))
			Expect(locator.removedPairs[0]).To(Equal(modelNodePair{"node-1", "llama"}))
			Expect(locator.removedPairs[1]).To(Equal(modelNodePair{"node-2", "llama"}))
		})

		It("continues when one node fails", func() {
			locator.nodes = []BackendNode{
				{ID: "node-fail", Name: "worker-fail"},
				{ID: "node-ok", Name: "worker-ok"},
			}
			// Use a messaging client that fails the first Publish call only.
			failOnce := &failOnceMessagingClient{inner: mc, failOn: 0}
			adapter = NewRemoteUnloaderAdapter(locator, failOnce, 3*time.Minute, 15*time.Minute)

			Expect(adapter.UnloadRemoteModel("llama")).To(HaveOccurred())

			// The second node should still have been processed.
			// The first node's StopBackend errored, so RemoveNodeModel was NOT called for it.
			// The second node's StopBackend succeeded, so RemoveNodeModel WAS called.
			Expect(locator.removedPairs).To(HaveLen(1))
			Expect(locator.removedPairs[0].nodeID).To(Equal("node-ok"))
		})

		It("propagates forced shutdown to every worker", func() {
			locator.nodes = []BackendNode{{ID: "node-1", Name: "worker-1"}}
			Expect(adapter.UnloadRemoteModelContext(context.Background(), "llama", true)).To(Succeed())

			var payload messaging.BackendStopRequest
			Expect(json.Unmarshal(mc.published[0].Data, &payload)).To(Succeed())
			Expect(payload).To(Equal(messaging.BackendStopRequest{Backend: "llama", Force: true}))
		})
	})

	Describe("StopBackend", func() {
		It("with empty backend publishes nil payload", func() {
			Expect(adapter.StopBackend("node-1", "")).To(Succeed())
			Expect(mc.published).To(HaveLen(1))
			Expect(mc.published[0].Subject).To(Equal(messaging.SubjectNodeBackendStop("node-1")))
			Expect(mc.published[0].Data).To(BeNil())
		})

		It("with backend name publishes JSON", func() {
			Expect(adapter.StopBackend("node-1", "llama-backend")).To(Succeed())
			Expect(mc.published).To(HaveLen(1))

			var payload messaging.BackendStopRequest
			Expect(json.Unmarshal(mc.published[0].Data, &payload)).To(Succeed())
			Expect(payload.Backend).To(Equal("llama-backend"))
			Expect(payload.Force).To(BeFalse())
		})
	})

	Describe("StopNode", func() {
		It("publishes to correct subject", func() {
			Expect(adapter.StopNode("node-abc")).To(Succeed())
			Expect(mc.published).To(HaveLen(1))
			Expect(mc.published[0].Subject).To(Equal(messaging.SubjectNodeStop("node-abc")))
			Expect(mc.published[0].Data).To(BeNil())
		})
	})

	Describe("DeleteModelFiles", func() {
		It("with no nodes returns nil", func() {
			locator.nodes = nil
			Expect(adapter.DeleteModelFiles("my-model")).To(Succeed())
		})

		It("continues on failure", func() {
			locator.nodes = []BackendNode{
				{ID: "node-1", Name: "w1"},
				{ID: "node-2", Name: "w2"},
			}
			// Request will fail for all calls.
			mc.requestErr = fmt.Errorf("timeout")
			Expect(adapter.DeleteModelFiles("my-model")).To(Succeed())
			// Both nodes attempted.
			Expect(mc.requestCalls).To(HaveLen(2))
			Expect(mc.requestCalls[0].Subject).To(Equal(messaging.SubjectNodeModelDelete("node-1")))
			Expect(mc.requestCalls[1].Subject).To(Equal(messaging.SubjectNodeModelDelete("node-2")))
		})
	})
})

// failOnceMessagingClient wraps fakeMessagingClient but fails the Publish call
// at index failOn (0-based) and succeeds all others.
type failOnceMessagingClient struct {
	inner   *fakeMessagingClient
	failOn  int
	callIdx int
	mu      sync.Mutex
}

func (f *failOnceMessagingClient) Publish(subject string, data any) error {
	f.mu.Lock()
	idx := f.callIdx
	f.callIdx++
	f.mu.Unlock()
	if idx == f.failOn {
		return fmt.Errorf("simulated failure")
	}
	return f.inner.Publish(subject, data)
}

func (f *failOnceMessagingClient) Subscribe(subject string, handler func([]byte)) (messaging.Subscription, error) {
	return f.inner.Subscribe(subject, handler)
}

func (f *failOnceMessagingClient) QueueSubscribe(subject, queue string, handler func([]byte)) (messaging.Subscription, error) {
	return f.inner.QueueSubscribe(subject, queue, handler)
}

func (f *failOnceMessagingClient) QueueSubscribeReply(subject, queue string, handler func(data []byte, reply func([]byte))) (messaging.Subscription, error) {
	return f.inner.QueueSubscribeReply(subject, queue, handler)
}

func (f *failOnceMessagingClient) SubscribeReply(subject string, handler func(data []byte, reply func([]byte))) (messaging.Subscription, error) {
	return f.inner.SubscribeReply(subject, handler)
}

func (f *failOnceMessagingClient) Request(subject string, data []byte, timeout time.Duration) ([]byte, error) {
	return f.inner.Request(subject, data, timeout)
}

func (f *failOnceMessagingClient) IsConnected() bool { return true }
func (f *failOnceMessagingClient) Close()            {}

var _ = Describe("RemoteUnloaderAdapter timeout configuration", func() {
	It("passes the configured install timeout to the messaging client", func() {
		mc := newScriptedMessagingClient()
		mc.scriptReply(messaging.SubjectNodeBackendInstall("n1"), messaging.BackendInstallReply{Success: true, Address: "127.0.0.1:0"})
		adapter := NewRemoteUnloaderAdapter(nil, mc, 7*time.Minute, 11*time.Minute)

		_, err := adapter.InstallBackend("n1", "llama-cpp", "", "[]", "", "", "", 0, "", nil)
		Expect(err).ToNot(HaveOccurred())

		Expect(mc.calls).To(HaveLen(1))
		Expect(mc.calls[0].Timeout).To(Equal(7 * time.Minute))
	})

	It("passes the configured upgrade timeout to the messaging client", func() {
		mc := newScriptedMessagingClient()
		mc.scriptReply(messaging.SubjectNodeBackendUpgrade("n1"), messaging.BackendUpgradeReply{Success: true})
		adapter := NewRemoteUnloaderAdapter(nil, mc, 7*time.Minute, 11*time.Minute)

		_, err := adapter.UpgradeBackend("n1", "llama-cpp", "[]", "", "", "", 0, "", nil)
		Expect(err).ToNot(HaveOccurred())

		Expect(mc.calls).To(HaveLen(1))
		Expect(mc.calls[0].Timeout).To(Equal(11 * time.Minute))
	})
})

var _ = Describe("RemoteUnloaderAdapter NATS timeout handling", func() {
	It("wraps nats.ErrTimeout from InstallBackend in galleryop.ErrWorkerStillInstalling", func() {
		mc := newScriptedMessagingClient()
		mc.scriptErr(messaging.SubjectNodeBackendInstall("n1"), nats.ErrTimeout)
		adapter := NewRemoteUnloaderAdapter(nil, mc, 100*time.Millisecond, 1*time.Second)

		_, err := adapter.InstallBackend("n1", "vllm", "", "[]", "", "", "", 0, "", nil)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, galleryop.ErrWorkerStillInstalling)).To(BeTrue(),
			"expected wrapped ErrWorkerStillInstalling, got %v", err)
	})

	It("does NOT wrap non-timeout errors", func() {
		mc := newScriptedMessagingClient()
		mc.scriptErr(messaging.SubjectNodeBackendInstall("n1"), nats.ErrNoResponders)
		adapter := NewRemoteUnloaderAdapter(nil, mc, 100*time.Millisecond, 1*time.Second)

		_, err := adapter.InstallBackend("n1", "vllm", "", "[]", "", "", "", 0, "", nil)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, galleryop.ErrWorkerStillInstalling)).To(BeFalse())
		Expect(errors.Is(err, nats.ErrNoResponders)).To(BeTrue())
	})
})

var _ = Describe("RemoteUnloaderAdapter install progress streaming", func() {
	It("forwards BackendInstallProgressEvent values into the onProgress callback when the worker publishes them", func() {
		mc := newScriptedMessagingClient()
		mc.scriptReply(messaging.SubjectNodeBackendInstall("n1"), messaging.BackendInstallReply{Success: true, Address: "127.0.0.1:0"})
		mc.scheduleProgressPublish("n1", "op-abc", []messaging.BackendInstallProgressEvent{
			{OpID: "op-abc", NodeID: "n1", Backend: "vllm", FileName: "vllm.tar.zst", Current: "100 MB", Total: "1 GB", Percentage: 10},
			{OpID: "op-abc", NodeID: "n1", Backend: "vllm", FileName: "vllm.tar.zst", Current: "500 MB", Total: "1 GB", Percentage: 50},
		})

		adapter := NewRemoteUnloaderAdapter(nil, mc, 1*time.Second, 1*time.Second)
		var (
			received []messaging.BackendInstallProgressEvent
			mu       sync.Mutex
		)
		onProgress := func(ev messaging.BackendInstallProgressEvent) {
			mu.Lock()
			defer mu.Unlock()
			received = append(received, ev)
		}

		_, err := adapter.InstallBackend("n1", "vllm", "", "[]", "", "", "", 0, "op-abc", onProgress)
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() int {
			mu.Lock()
			defer mu.Unlock()
			return len(received)
		}, "1s").Should(Equal(2))
	})

	It("does NOT subscribe when onProgress is nil (reconciler retry path)", func() {
		mc := newScriptedMessagingClient()
		mc.scriptReply(messaging.SubjectNodeBackendInstall("n1"), messaging.BackendInstallReply{Success: true})

		adapter := NewRemoteUnloaderAdapter(nil, mc, 1*time.Second, 1*time.Second)
		_, err := adapter.InstallBackend("n1", "vllm", "", "[]", "", "", "", 0, "", nil)
		Expect(err).ToNot(HaveOccurred())

		Expect(mc.subscribeCalls()).To(BeEmpty(),
			"reconciler-driven retries must not subscribe to the per-op progress subject")
	})
})
