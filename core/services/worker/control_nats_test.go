package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/mudler/xlog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
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
	publish  []published
}

type published struct {
	subject string
	payload any
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

func (b *recordingBus) Publish(subject string, payload any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.publish = append(b.publish, published{subject: subject, payload: payload})
	return nil
}

func (b *recordingBus) published() []published {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]published(nil), b.publish...)
}
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

// lockedBuffer lets a spec read what a handler goroutine logged.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

var _ = Describe("Worker control verbs: install progress and malformed requests", func() {
	var (
		bus *recordingBus
		s   *backendSupervisor
	)

	BeforeEach(func() {
		bus = newRecordingBus()
		s = newLifecycleTestSupervisor(make(chan os.Signal, 1))
		Expect(registerLifecycleForTest(s, bus)).To(Succeed())
	})

	// emitTwo stands in for a gallery download that ticks twice. It reports
	// whether the handler handed it a callback at all, which is how an install
	// without an OpID stays silent.
	emitTwo := func(onDownload func(file, current, total string, percentage float64)) bool {
		if onDownload == nil {
			return false
		}
		onDownload("backend.tar", "1 MB", "2 MB", 50)
		onDownload("backend.tar", "2 MB", "2 MB", 100)
		return true
	}

	progressOn := func(subject string) []workerctl.BackendInstallProgressEvent {
		var evs []workerctl.BackendInstallProgressEvent
		for _, p := range bus.published() {
			Expect(p.subject).To(Equal(subject))
			ev, ok := p.payload.(workerctl.BackendInstallProgressEvent)
			Expect(ok).To(BeTrue(), "progress payload is %T", p.payload)
			evs = append(evs, ev)
		}
		return evs
	}

	expectTwoEvents := func(evs []workerctl.BackendInstallProgressEvent) {
		Expect(evs).To(HaveLen(2))
		for _, ev := range evs {
			Expect(ev.OpID).To(Equal("op1"))
			Expect(ev.NodeID).To(Equal("n1"))
			Expect(ev.Backend).To(Equal("vllm"))
			Expect(ev.Phase).To(Equal(workerctl.PhaseDownloading))
		}
		// The second tick lands inside the debounce window, so it only reaches
		// the bus through the terminal flush that runs before the reply.
		Expect(evs[0].Percentage).To(Equal(50.0))
		Expect(evs[1].Percentage).To(Equal(100.0))
	}

	It("publishes install progress on the per-op subject before replying", func() {
		s.installFn = func(_ workerctl.BackendInstallRequest, _ bool, onDownload func(string, string, string, float64)) (string, error) {
			emitTwo(onDownload)
			return "127.0.0.1:50051", nil
		}
		body, err := json.Marshal(workerctl.BackendInstallRequest{Backend: "vllm", OpID: "op1"})
		Expect(err).NotTo(HaveOccurred())

		var reply string
		Eventually(bus.deliver(messaging.SubjectNodeBackendInstall("n1"), body)).Should(Receive(&reply))
		Expect(reply).To(ContainSubstring(`"success":true`))
		expectTwoEvents(progressOn(messaging.SubjectNodeBackendInstallProgress("n1", "op1")))
	})

	It("publishes upgrade progress on the per-op subject before replying", func() {
		s.upgradeFn = func(_ workerctl.BackendUpgradeRequest, onDownload func(string, string, string, float64)) ([]string, error) {
			emitTwo(onDownload)
			return nil, nil
		}
		body, err := json.Marshal(workerctl.BackendUpgradeRequest{Backend: "vllm", OpID: "op1"})
		Expect(err).NotTo(HaveOccurred())

		var reply string
		Eventually(bus.deliver(messaging.SubjectNodeBackendUpgrade("n1"), body)).Should(Receive(&reply))
		Expect(reply).To(ContainSubstring(`"success":true`))
		expectTwoEvents(progressOn(messaging.SubjectNodeBackendInstallProgress("n1", "op1")))
	})

	It("reports no progress for an install without an OpID", func() {
		gotCallback := make(chan bool, 1)
		s.installFn = func(_ workerctl.BackendInstallRequest, _ bool, onDownload func(string, string, string, float64)) (string, error) {
			gotCallback <- emitTwo(onDownload)
			return "127.0.0.1:50051", nil
		}
		body, err := json.Marshal(workerctl.BackendInstallRequest{Backend: "vllm"})
		Expect(err).NotTo(HaveOccurred())

		Eventually(bus.deliver(messaging.SubjectNodeBackendInstall("n1"), body)).Should(Receive())
		Expect(gotCallback).To(Receive(BeFalse()))
		Expect(bus.published()).To(BeEmpty())
	})

	Context("with a malformed request", func() {
		var logs *lockedBuffer

		BeforeEach(func() {
			logs = &lockedBuffer{}
			handler := slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn})
			xlog.SetLogger(xlog.NewLoggerWithHandler(handler, xlog.LogLevelWarn))
		})

		AfterEach(func() {
			// xlog has no getter for the package logger, so restore the
			// default the suite starts with.
			xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text"))
		})

		DescribeTable("leaves a warning that names the verb",
			func(subject func(string) string, verb string) {
				Eventually(bus.deliver(subject("n1"), []byte(malformedBody))).Should(Receive())
				Expect(logs.String()).To(And(
					ContainSubstring(`msg="Ignoring malformed control request"`),
					ContainSubstring("verb="+verb),
					ContainSubstring("unexpected end of JSON input"),
				))
			},
			Entry("backend.install", messaging.SubjectNodeBackendInstall, "backend.install"),
			Entry("backend.upgrade", messaging.SubjectNodeBackendUpgrade, "backend.upgrade"),
			Entry("backend.delete", messaging.SubjectNodeBackendDelete, "backend.delete"),
			Entry("model.unload", messaging.SubjectNodeModelUnload, "model.unload"),
			Entry("model.stop", messaging.SubjectNodeModelStop, "model.stop"),
			Entry("model.delete", messaging.SubjectNodeModelDelete, "model.delete"),
		)
	})
})
