package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/xlog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// recordingConfigProvider counts lookups: the dispatcher asks for a config
// only once it has decoded an event and is about to run the agent.
type recordingConfigProvider struct {
	mu    sync.Mutex
	calls []string
}

func (p *recordingConfigProvider) GetAgentConfig(userID, name string) (*AgentConfig, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, userID+"/"+name)
	return nil, errors.New("no such agent")
}

func (p *recordingConfigProvider) Calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

// lockedBuffer lets the handler goroutine log while the spec reads.
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

var _ = Describe("NATSDispatcher consuming agent runs", func() {
	var (
		bus     *testutil.FakeBus
		configs *recordingConfigProvider
		logs    *lockedBuffer
		d       *NATSDispatcher
	)

	BeforeEach(func() {
		bus = testutil.NewFakeBus()
		configs = &recordingConfigProvider{}
		logs = &lockedBuffer{}
		xlog.SetLogger(xlog.NewLoggerWithHandler(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelError}), xlog.LogLevelError))
		DeferCleanup(func() {
			xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text"))
		})

		bridge := NewEventBridge(bus, nil, "test-worker")
		d = NewNATSDispatcher(messaging.NewNATSWorkConsumer(bus), bridge, configs, "http://127.0.0.1:1", "", 0)
		Expect(d.Start(GinkgoT().Context())).To(Succeed())
	})

	It("logs and drops an undecodable event without running an agent", func() {
		var statuses []string
		var mu sync.Mutex
		_, err := bus.Subscribe("agent.*.events.*", func(data []byte) {
			mu.Lock()
			statuses = append(statuses, string(data))
			mu.Unlock()
		})
		Expect(err).ToNot(HaveOccurred())

		// A JSON string is valid on the wire but cannot decode into an event.
		Expect(bus.Publish(messaging.SubjectAgentExecute, "not an event")).To(Succeed())
		// Stop waits for the in-flight handler, so everything below is final.
		Expect(d.Stop()).To(Succeed())

		Expect(logs.String()).To(ContainSubstring("Failed to unmarshal agent chat event"))
		Expect(configs.Calls()).To(BeEmpty())
		mu.Lock()
		defer mu.Unlock()
		Expect(statuses).To(BeEmpty())
	})

	It("runs a decodable event on the process-wide event bridge", func() {
		var events []AgentEvent
		var mu sync.Mutex
		_, err := bus.Subscribe(messaging.SubjectAgentEvents("a1", "u1"), func(data []byte) {
			var evt AgentEvent
			Expect(json.Unmarshal(data, &evt)).To(Succeed())
			mu.Lock()
			events = append(events, evt)
			mu.Unlock()
		})
		Expect(err).ToNot(HaveOccurred())

		Expect(bus.Publish(messaging.SubjectAgentExecute, AgentChatEvent{AgentName: "a1", UserID: "u1", Message: "hi"})).To(Succeed())
		Expect(d.Stop()).To(Succeed())

		Expect(configs.Calls()).To(Equal([]string{"u1/a1"}))
		mu.Lock()
		defer mu.Unlock()
		Expect(events).To(HaveLen(1))
		Expect(events[0].EventType).To(Equal("json_message_status"))
		Expect(events[0].Metadata).To(ContainSubstring("error: agent config not found"))
	})
})

// recordingWorkConsumer records what each Consume call asked for, so a spec
// can pin the limit the production caller chooses rather than what the
// carrier does with it.
type recordingWorkConsumer struct {
	kinds []messaging.WorkKind
	max   []int
}

func (c *recordingWorkConsumer) Consume(_ context.Context, kind messaging.WorkKind, maxInFlight int, _ messaging.WorkHandler) (messaging.Subscription, error) {
	c.kinds = append(c.kinds, kind)
	c.max = append(c.max, maxInFlight)
	return noopSubscription{}, nil
}

type noopSubscription struct{}

func (noopSubscription) Unsubscribe() error { return nil }

var _ = Describe("NATSDispatcher.Start", func() {
	// The CLI agent worker passes 0, so agent runs are unbounded per worker;
	// a dispatcher that dropped or replaced its limit would change that.
	DescribeTable("asks for agent runs with its own concurrency limit",
		func(maxConcurrent int) {
			consumer := &recordingWorkConsumer{}
			d := NewNATSDispatcher(consumer, nil, nil, "", "", maxConcurrent)
			Expect(d.Start(GinkgoT().Context())).To(Succeed())

			Expect(consumer.kinds).To(Equal([]messaging.WorkKind{messaging.WorkAgentRun}))
			Expect(consumer.max).To(Equal([]int{maxConcurrent}))
		},
		Entry("unbounded", 0),
		Entry("serial", 1),
		Entry("bounded", 4),
	)
})
