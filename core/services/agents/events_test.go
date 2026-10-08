package agents

import (
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeMessagingClient implements messaging.MessagingClient and captures the
// last published payload so tests can assert on it.
type fakeMessagingClient struct {
	lastSubject string
	lastData    any
}

func (f *fakeMessagingClient) Publish(subject string, data any) error {
	f.lastSubject = subject
	f.lastData = data
	return nil
}

func (f *fakeMessagingClient) Subscribe(string, func([]byte)) (messaging.Subscription, error) {
	return &fakeSub{}, nil
}

func (f *fakeMessagingClient) QueueSubscribe(string, string, func([]byte)) (messaging.Subscription, error) {
	return &fakeSub{}, nil
}

func (f *fakeMessagingClient) QueueSubscribeReply(string, string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return &fakeSub{}, nil
}

func (f *fakeMessagingClient) SubscribeReply(string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return &fakeSub{}, nil
}

func (f *fakeMessagingClient) Request(string, []byte, time.Duration) ([]byte, error) {
	return nil, nil
}

func (f *fakeMessagingClient) IsConnected() bool { return true }
func (f *fakeMessagingClient) Close()            {}

type fakeSub struct{}

func (s *fakeSub) Unsubscribe() error { return nil }

var _ = Describe("EventBridge", func() {
	Describe("PublishEvent timestamp", func() {
		// Regression for #9867: agent chat messages rendered a broken
		// timestamp ("Invalid Timestamp" / "12:00 AM") in the web UI because
		// this path emitted Unix nanoseconds while the local dispatcher and the
		// React UI both expect Unix milliseconds. Nanoseconds also overflow JS's
		// safe-integer range. The timestamp must be in milliseconds.
		It("emits the timestamp in Unix milliseconds", func() {
			fake := &fakeMessagingClient{}
			bridge := NewEventBridge(fake, nil, "instance-1")

			before := time.Now().UnixMilli()
			err := bridge.PublishMessage("agent", "user", "agent", "hello", "msg-1")
			after := time.Now().UnixMilli()

			Expect(err).ToNot(HaveOccurred())

			evt, ok := fake.lastData.(AgentEvent)
			Expect(ok).To(BeTrue(), "published payload should be an AgentEvent")

			// A millisecond timestamp falls within [before, after]; a nanosecond
			// one (~1e6 larger) would be far outside this window.
			Expect(evt.Timestamp).To(BeNumerically(">=", before))
			Expect(evt.Timestamp).To(BeNumerically("<=", after))
		})
	})
})

// recordingPublisher keeps what is published to it.
type recordingPublisher struct {
	mu       sync.Mutex
	subjects []string
	payloads []any
}

func (r *recordingPublisher) Publish(subject string, data any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subjects = append(r.subjects, subject)
	r.payloads = append(r.payloads, data)
	return nil
}

func (r *recordingPublisher) seen() ([]string, []any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.subjects...), append([]any(nil), r.payloads...)
}

var _ = Describe("EventBridge.WithPublisher", func() {
	It("publishes every kind of event on the publisher it is given, with the subject and payload of the bridge", func() {
		own := &recordingPublisher{}
		bound := &recordingPublisher{}
		bridge := NewEventBridge(&broadcasterOver{Publisher: own}, nil, "worker-1")
		view := bridge.WithPublisher(bound)

		Expect(view.PublishMessage("a1", "u1", RoleAgent, "hi", "m1")).To(Succeed())
		Expect(view.PublishStatus("a1", "u1", "processing")).To(Succeed())
		Expect(view.PublishStreamEvent("a1", "u1", map[string]any{"type": "content"})).To(Succeed())
		view.PersistObservable("a1", "u1", "chat", map[string]string{"k": "v"})

		subjects, payloads := bound.seen()
		Expect(subjects).To(HaveLen(4))
		for _, s := range subjects {
			Expect(s).To(Equal(messaging.SubjectAgentEvents("a1", "u1")))
		}
		types := []string{}
		for _, p := range payloads {
			evt, ok := p.(AgentEvent)
			Expect(ok).To(BeTrue())
			Expect(evt.AgentName).To(Equal("a1"))
			types = append(types, evt.EventType)
		}
		Expect(types).To(Equal([]string{"json_message", "json_message_status", "stream_event", "observable_update"}))
		ownSubjects, _ := own.seen()
		Expect(ownSubjects).To(BeEmpty(), "nothing leaks to the bus the bridge was built with")
	})

	It("publishes the same subjects and payloads as the bridge it was made from", func() {
		viaBridge := &recordingPublisher{}
		viaView := &recordingPublisher{}
		bridge := NewEventBridge(&broadcasterOver{Publisher: viaBridge}, nil, "worker-1")
		view := NewEventBridge(&broadcasterOver{Publisher: &recordingPublisher{}}, nil, "worker-1").WithPublisher(viaView)

		Expect(bridge.PublishMessage("a1", "u1", RoleUser, "hello", "m1")).To(Succeed())
		Expect(view.PublishMessage("a1", "u1", RoleUser, "hello", "m1")).To(Succeed())

		s1, p1 := viaBridge.seen()
		s2, p2 := viaView.seen()
		Expect(s2).To(Equal(s1))
		e1, e2 := p1[0].(AgentEvent), p2[0].(AgentEvent)
		e1.Timestamp, e2.Timestamp = 0, 0
		Expect(e2).To(Equal(e1))
	})

	It("shares the cancel registry with the bridge", func() {
		bridge := NewEventBridge(&broadcasterOver{Publisher: &recordingPublisher{}}, nil, "worker-1")
		view := bridge.WithPublisher(&recordingPublisher{})

		cancelled := false
		view.RegisterCancel("m1", func() { cancelled = true })
		Expect(bridge.cancelRegistry.Cancel("m1")).To(BeTrue())
		Expect(cancelled).To(BeTrue())

		view.RegisterCancel("m2", func() { cancelled = false })
		view.DeregisterCancel("m2")
		Expect(bridge.cancelRegistry.Cancel("m2")).To(BeFalse())
	})

	It("keeps the publisher of the bridge when it is given none", func() {
		own := &recordingPublisher{}
		bridge := NewEventBridge(&broadcasterOver{Publisher: own}, nil, "worker-1")
		Expect(bridge.WithPublisher(nil).PublishStatus("a1", "u1", "x")).To(Succeed())
		subjects, _ := own.seen()
		Expect(subjects).To(HaveLen(1))
	})
})

// broadcasterOver is a Broadcaster whose Publish goes to a recording publisher
// and which subscribes to nothing.
type broadcasterOver struct{ Publisher messaging.Publisher }

func (b *broadcasterOver) Publish(subject string, data any) error {
	return b.Publisher.Publish(subject, data)
}

func (b *broadcasterOver) Subscribe(string, func([]byte)) (messaging.Subscription, error) {
	return &fakeSub{}, nil
}
