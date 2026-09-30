package worker

import (
	"context"
	"errors"
	"os"
	"sync"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/pkg/system"
)

// recordingBus is a messaging.MessagingClient that keeps every subscription
// callback so a spec can deliver a request by hand and see what the worker
// answers. A Subscribe callback is stored behind a reply func it never calls,
// so a spec can assert "nothing was sent" the same way for both kinds.
type recordingBus struct {
	mu       sync.Mutex
	subjects []string
	handlers map[string]func([]byte, func([]byte))
	failOn   map[string]error
}

func newRecordingBus() *recordingBus {
	return &recordingBus{handlers: map[string]func([]byte, func([]byte)){}, failOn: map[string]error{}}
}

func (b *recordingBus) record(subject string, h func([]byte, func([]byte))) (messaging.Subscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.failOn[subject]; err != nil {
		return nil, err
	}
	b.subjects = append(b.subjects, subject)
	b.handlers[subject] = h
	return releaseSubscription{}, nil
}

func (b *recordingBus) Publish(string, any) error { return nil }
func (b *recordingBus) Subscribe(subject string, h func([]byte)) (messaging.Subscription, error) {
	return b.record(subject, func(data []byte, _ func([]byte)) { h(data) })
}
func (b *recordingBus) QueueSubscribe(string, string, func([]byte)) (messaging.Subscription, error) {
	return releaseSubscription{}, nil
}
func (b *recordingBus) QueueSubscribeReply(string, string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return releaseSubscription{}, nil
}
func (b *recordingBus) SubscribeReply(subject string, h func([]byte, func([]byte))) (messaging.Subscription, error) {
	return b.record(subject, h)
}
func (b *recordingBus) Request(string, []byte, time.Duration) ([]byte, error) { return nil, nil }
func (b *recordingBus) IsConnected() bool                                     { return true }
func (b *recordingBus) Close()                                                {}

func (b *recordingBus) subscribed() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.subjects...)
}

// deliver runs the subscription callback for subject the way the NATS client
// would and returns a channel that receives every reply it sends.
func (b *recordingBus) deliver(subject string, body []byte) <-chan string {
	b.mu.Lock()
	h := b.handlers[subject]
	b.mu.Unlock()
	Expect(h).NotTo(BeNil(), "no subscription for %s", subject)
	replies := make(chan string, 4)
	h(body, func(data []byte) { replies <- string(data) })
	return replies
}

func newLifecycleTestSupervisor(sigCh chan<- os.Signal) *backendSupervisor {
	ss, err := system.GetSystemState(system.WithBackendPath(GinkgoT().TempDir()), system.WithModelPath(GinkgoT().TempDir()))
	Expect(err).NotTo(HaveOccurred())
	return &backendSupervisor{
		cfg:         &Config{},
		nodeID:      "n1",
		systemState: ss,
		sigCh:       sigCh,
		processes:   map[string]*backendProcess{},
	}
}

func registerLifecycleForTest(s *backendSupervisor, bus *recordingBus) error {
	return s.registerLifecycleVerbs(newNATSControlServer(bus, s.nodeID))
}

const malformedBody = `{"backend":`

var _ = Describe("Worker control verbs over NATS", func() {
	var (
		bus   *recordingBus
		sigCh chan os.Signal
		s     *backendSupervisor
	)

	BeforeEach(func() {
		bus = newRecordingBus()
		sigCh = make(chan os.Signal, 1)
		s = newLifecycleTestSupervisor(sigCh)
	})

	It("subscribes exactly the ten lifecycle subjects of the node", func() {
		Expect(registerLifecycleForTest(s, bus)).To(Succeed())
		Expect(bus.subscribed()).To(ConsistOf(
			messaging.SubjectNodeBackendInstall("n1"),
			messaging.SubjectNodeBackendUpgrade("n1"),
			messaging.SubjectNodeBackendStop("n1"),
			messaging.SubjectNodeBackendDelete("n1"),
			messaging.SubjectNodeBackendList("n1"),
			messaging.SubjectNodeModelsRunning("n1"),
			messaging.SubjectNodeModelUnload("n1"),
			messaging.SubjectNodeModelStop("n1"),
			messaging.SubjectNodeModelDelete("n1"),
			messaging.SubjectNodeStop("n1"),
		))
	})

	DescribeTable("answers a malformed body with the verb's refusal bytes",
		func(subject func(string) string, want string) {
			Expect(registerLifecycleForTest(s, bus)).To(Succeed())
			replies := bus.deliver(subject("n1"), []byte(malformedBody))
			Eventually(replies).Should(Receive(Equal(want)))
			Consistently(replies, 50*time.Millisecond).ShouldNot(Receive())
		},
		Entry("backend.install", messaging.SubjectNodeBackendInstall,
			`{"success":false,"error":"invalid request: unexpected end of JSON input"}`),
		Entry("backend.upgrade", messaging.SubjectNodeBackendUpgrade,
			`{"success":false,"error":"invalid request: unexpected end of JSON input"}`),
		Entry("backend.stop", messaging.SubjectNodeBackendStop,
			`{"success":false,"error":"invalid request: decoding backend stop request: unexpected end of JSON input","reports_stopped_processes":true}`),
		Entry("backend.delete", messaging.SubjectNodeBackendDelete,
			`{"success":false,"error":"invalid request: unexpected end of JSON input"}`),
		Entry("model.unload", messaging.SubjectNodeModelUnload,
			`{"success":false,"error":"invalid request: unexpected end of JSON input"}`),
		Entry("model.stop", messaging.SubjectNodeModelStop,
			`{"matched":false,"freed":false,"terminated":false,"process_key":"","error":"invalid request: unexpected end of JSON input"}`),
		Entry("model.delete", messaging.SubjectNodeModelDelete,
			`{"success":false,"error":"invalid request"}`),
	)

	DescribeTable("still answers a malformed body on a verb that ignores its body",
		func(subject func(string) string, want string) {
			Expect(registerLifecycleForTest(s, bus)).To(Succeed())
			replies := bus.deliver(subject("n1"), []byte(malformedBody))
			Eventually(replies).Should(Receive(Equal(want)))
		},
		Entry("backend.list", messaging.SubjectNodeBackendList, `{"backends":null}`),
		Entry("models.running", messaging.SubjectNodeModelsRunning, `{"models":[]}`),
	)

	It("signals shutdown on node.stop without replying, and never blocks on a repeat", func() {
		Expect(registerLifecycleForTest(s, bus)).To(Succeed())
		replies := bus.deliver(messaging.SubjectNodeStop("n1"), nil)
		Expect(sigCh).To(Receive(Equal(syscall.SIGTERM)))
		Expect(replies).NotTo(Receive())

		sigCh <- syscall.SIGINT
		replies = bus.deliver(messaging.SubjectNodeStop("n1"), nil)
		Expect(replies).NotTo(Receive())
		Expect(sigCh).To(Receive(Equal(syscall.SIGINT)))
	})

	It("aborts registration on a subscribe error and names the verb", func() {
		denied := errors.New("permissions violation")
		bus.failOn[messaging.SubjectNodeBackendStop("n1")] = denied

		err := s.registerLifecycleVerbs(newNATSControlServer(bus, "n1"))

		Expect(err).To(MatchError(denied))
		Expect(err.Error()).To(HavePrefix("serving backend.stop: "))
		Expect(bus.subscribed()).NotTo(ContainElement(messaging.SubjectNodeBackendDelete("n1")))
		Expect(bus.subscribed()).NotTo(ContainElement(messaging.SubjectNodeStop("n1")))
	})

	It("runs a unary verb inside the callback and a with-progress verb beside it", func() {
		srv := newNATSControlServer(bus, "n1")
		release := make(chan struct{})
		blocked := func() (any, error) {
			<-release
			return struct{}{}, nil
		}
		Expect(srv.handle(verbBackendList, func(_ context.Context, _ []byte) (any, error) { return blocked() })).To(Succeed())
		Expect(srv.handleWithProgress(verbBackendInstall, func(_ context.Context, _ []byte, _ progressSink) (any, error) { return blocked() })).To(Succeed())

		installReturned := make(chan (<-chan string), 1)
		go func() { installReturned <- bus.deliver(messaging.SubjectNodeBackendInstall("n1"), nil) }()
		var installReplies <-chan string
		Eventually(installReturned).Should(Receive(&installReplies))
		Expect(installReplies).NotTo(Receive())

		listReturned := make(chan (<-chan string), 1)
		go func() { listReturned <- bus.deliver(messaging.SubjectNodeBackendList("n1"), nil) }()
		Consistently(listReturned, 100*time.Millisecond).ShouldNot(Receive())

		close(release)
		var listReplies <-chan string
		Eventually(listReturned).Should(Receive(&listReplies))
		Expect(listReplies).To(Receive(Equal(`{}`)))
		Eventually(installReplies).Should(Receive(Equal(`{}`)))
	})
})
