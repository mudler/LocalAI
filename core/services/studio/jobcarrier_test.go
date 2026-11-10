package studio_test

import (
	"encoding/json"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/bgjob"
)

// serialBus delivers the messages of a subscription one at a time, as the
// message bus does: the next one waits until the handler has returned.
type serialBus struct {
	mu      sync.Mutex
	handler func([]byte, func([]byte))
	deliver sync.Mutex
}

func (b *serialBus) SubscribeReply(_ string, h func([]byte, func([]byte))) (messaging.Subscription, error) {
	b.mu.Lock()
	b.handler = h
	b.mu.Unlock()
	return nopSub{}, nil
}

func (b *serialBus) Request(_ string, data []byte, _ time.Duration) ([]byte, error) {
	reply := make(chan []byte, 1)
	go func() {
		b.deliver.Lock()
		defer b.deliver.Unlock()
		b.mu.Lock()
		h := b.handler
		b.mu.Unlock()
		h(data, func(r []byte) { reply <- r })
	}()
	return <-reply, nil
}

type nopSub struct{}

func (nopSub) Unsubscribe() error { return nil }

var _ = Describe("the job carrier", func() {
	It("answers a request while another one of the node waits", func() {
		bus := &serialBus{}
		c := studio.NewJobCarrier(bus, nil)
		release := make(chan struct{})
		stop, err := c.Serve("n", func(q bgjob.Request) bgjob.Reply {
			if q.Action == bgjob.ActionInput {
				<-release // a child that does not read its input
			}
			return bgjob.Reply{Wrote: 1}
		})
		Expect(err).NotTo(HaveOccurred())
		defer stop()
		defer close(release)

		go func() { _, _ = c.Ask("n", bgjob.Request{Action: bgjob.ActionInput}) }()
		time.Sleep(100 * time.Millisecond)
		done := make(chan struct{})
		go func() {
			defer close(done)
			rep, err := c.Ask("n", bgjob.Request{Action: bgjob.ActionList})
			Expect(err).NotTo(HaveOccurred())
			b, _ := json.Marshal(rep)
			Expect(string(b)).To(ContainSubstring(`"wrote":1`))
		}()
		Eventually(done, "2s").Should(BeClosed())
	})
})
