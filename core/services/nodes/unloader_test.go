package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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

func (f *fakeModelLocator) RemoveNodeModel(_ context.Context, nodeID, modelName string) error {
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

func (f *fakeMessagingClient) Request(subject string, data []byte, _ time.Duration) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requestCalls = append(f.requestCalls, requestCall{Subject: subject, Data: data})
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
		adapter = NewRemoteUnloaderAdapter(locator, mc)
	})

	Describe("UnloadRemoteModel", func() {
		It("with no nodes returns nil", func() {
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
			adapter = NewRemoteUnloaderAdapter(locator, failOnce)

			Expect(adapter.UnloadRemoteModel("llama")).To(Succeed())

			// The second node should still have been processed.
			// The first node's StopBackend errored, so RemoveNodeModel was NOT called for it.
			// The second node's StopBackend succeeded, so RemoveNodeModel WAS called.
			Expect(locator.removedPairs).To(HaveLen(1))
			Expect(locator.removedPairs[0].nodeID).To(Equal("node-ok"))
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

			var payload struct {
				Backend string `json:"backend"`
			}
			Expect(json.Unmarshal(mc.published[0].Data, &payload)).To(Succeed())
			Expect(payload.Backend).To(Equal("llama-backend"))
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
