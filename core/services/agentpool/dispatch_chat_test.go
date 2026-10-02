package agentpool

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/messaging"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// recordingWorkQueue keeps the typed payload so a spec can tell an
// AgentChatEvent from pre-encoded bytes.
type recordingWorkQueue struct {
	kinds    []messaging.WorkKind
	payloads []any
	err      error
}

func (q *recordingWorkQueue) Enqueue(_ context.Context, kind messaging.WorkKind, payload any) error {
	q.kinds = append(q.kinds, kind)
	q.payloads = append(q.payloads, payload)
	return q.err
}

var _ = Describe("dispatchChat", func() {
	It("enqueues an agent run carrying the typed chat event", func() {
		queue := &recordingWorkQueue{}
		svc, err := NewAgentPoolService(&config.ApplicationConfig{}, AgentPoolOptions{WorkQueue: queue})
		Expect(err).ToNot(HaveOccurred())

		id, err := svc.dispatchChat("user-1", "agent-a", "hello")
		Expect(err).ToNot(HaveOccurred())
		Expect(id).ToNot(BeEmpty())

		Expect(queue.kinds).To(Equal([]messaging.WorkKind{messaging.WorkAgentRun}))
		evt, ok := queue.payloads[0].(agents.AgentChatEvent)
		Expect(ok).To(BeTrue(), "the queue must be handed the typed AgentChatEvent")
		Expect(evt.AgentName).To(Equal("agent-a"))
		Expect(evt.UserID).To(Equal("user-1"))
		Expect(evt.Message).To(Equal("hello"))
		Expect(evt.MessageID).To(Equal(id))
		Expect(evt.Role).To(Equal("user"))
	})

	It("reports an enqueue failure to the caller", func() {
		queue := &recordingWorkQueue{err: errors.New("carrier down")}
		svc, err := NewAgentPoolService(&config.ApplicationConfig{}, AgentPoolOptions{WorkQueue: queue})
		Expect(err).ToNot(HaveOccurred())

		_, err = svc.dispatchChat("user-1", "agent-a", "hello")
		Expect(err).To(MatchError(ContainSubstring("carrier down")))
	})

	It("stays in local mode when no work queue is given", func() {
		svc, err := NewAgentPoolService(&config.ApplicationConfig{}, AgentPoolOptions{})
		Expect(err).ToNot(HaveOccurred())
		// A strict interface comparison: Gomega's BeNil would also accept a
		// typed-nil pointer, which the mode switch reads as distributed.
		Expect(svc.distributed.workQueue == nil).To(BeTrue())
	})
})
