package pgbus

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Internal on purpose: the window in which the poll context expires after the
// connection returned a notification is a few microseconds wide, and a spec that
// races for it passes by luck. These specs make the connection return at the
// moment the window ends.
var _ = Describe("one poll of the listen connection", func() {
	var b *Bus

	BeforeEach(func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		b = &Bus{ctx: ctx, inbound: make(chan inbound, 4)}
	})

	It("offers a notification that arrives together with an expired poll window", func() {
		res := b.poll(func(ctx context.Context) (*pgconn.Notification, error) {
			<-ctx.Done() // the window ends first ...
			return &pgconn.Notification{Channel: "c", Payload: "p"}, nil // ... the read still won
		})

		Expect(res).To(Equal(pollContinue))
		Expect(b.inbound).To(Receive(Equal(inbound{channel: "c", payload: "p"})))
	})

	It("keeps the connection when the window ends with nothing to read", func() {
		res := b.poll(func(ctx context.Context) (*pgconn.Notification, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})

		Expect(res).To(Equal(pollContinue))
		Expect(b.inbound).ToNot(Receive())
	})

	It("asks for a new connection when the read fails before the window ends", func() {
		res := b.poll(func(context.Context) (*pgconn.Notification, error) {
			return nil, errors.New("connection reset")
		})

		Expect(res).To(Equal(pollRedial))
	})
})
