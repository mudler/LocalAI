package agents

import (
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// recordingBus is a messaging.Broadcaster that captures the last published
// payload. A Broadcaster and not a MessagingClient because fan-out is the whole
// of what an EventBridge may do with a carrier.
type recordingBus struct {
	lastSubject string
	lastData    any
}

func (f *recordingBus) Publish(subject string, data any) error {
	f.lastSubject = subject
	f.lastData = data
	return nil
}

func (f *recordingBus) Subscribe(string, func([]byte)) (messaging.Subscription, error) {
	return &fakeSub{}, nil
}

var _ messaging.Broadcaster = (*recordingBus)(nil)

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
			fake := &recordingBus{}
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
