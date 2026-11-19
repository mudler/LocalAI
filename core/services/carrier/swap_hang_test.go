package carrier_test

import (
	"context"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
)

type staticRows struct{ row cluster.CarrierRow }

func (r staticRows) Get(context.Context) (cluster.CarrierRow, error) { return r.row, nil }
func (r staticRows) DBNow(context.Context) (time.Time, error)        { return time.Now(), nil }

type nopReady struct{}

func (nopReady) ReportReady(context.Context, int64, string) error { return nil }

var _ = Describe("a swapper whose build hangs", func() {
	var (
		cur     atomic.Pointer[carrier.Set]
		row     cluster.CarrierRow
		started chan struct{}
	)

	newSwapper := func(timeout time.Duration) *carrier.Swapper {
		GinkgoHelper()
		cur.Store(newFakeCarrier(cluster.CarrierNATS, 1).set)
		// The row names the other carrier as active, so Reconcile builds it.
		row = cluster.CarrierRow{Active: cluster.CarrierTunnel, State: cluster.StateStable, Epoch: 2}
		started = make(chan struct{}, 1)
		s, err := carrier.NewSwapper(carrier.SwapperOptions{
			Cur: &cur, Bus: carrier.NewBroadcaster(&cur), Rows: staticRows{row}, Ready: nopReady{},
			Build: func(ctx context.Context, _ cluster.CarrierRow, _ cluster.Carrier) (*carrier.Set, error) {
				started <- struct{}{}
				<-ctx.Done()
				return nil, ctx.Err()
			},
			Interval: time.Hour, BuildTimeout: timeout,
		})
		Expect(err).ToNot(HaveOccurred())
		return s
	}

	It("lets Close cancel the build instead of waiting behind it", func() {
		s := newSwapper(time.Minute)
		reconciled := make(chan error, 1)
		go func() { reconciled <- s.Reconcile(context.Background(), row) }()
		Eventually(started).Should(Receive())

		closed := make(chan struct{})
		go func() { s.Close(); close(closed) }()
		Eventually(closed, 5*time.Second).Should(BeClosed())
		Eventually(reconciled, 5*time.Second).Should(Receive())
	})

	It("gives up on a build that takes longer than the timeout", func() {
		s := newSwapper(100 * time.Millisecond)
		DeferCleanup(s.Close)
		done := make(chan error, 1)
		go func() { done <- s.Reconcile(context.Background(), row) }()
		Eventually(started).Should(Receive())
		Eventually(done, 5*time.Second).Should(Receive())
	})
})

var _ = Describe("releasing a set with a publish that does not end", func() {
	It("stops waiting for the publish after the deadline", func() {
		restore := carrier.SetReleaseWait(100 * time.Millisecond)
		DeferCleanup(restore)
		var cur atomic.Pointer[carrier.Set]
		cur.Store(newFakeCarrier(cluster.CarrierNATS, 1).set)
		bus := carrier.NewBroadcaster(&cur)
		other := newFakeCarrier(cluster.CarrierTunnel, 2).set
		Expect(bus.Listen(other)).To(Succeed())
		done := other.HoldPublish()
		DeferCleanup(done)

		released := make(chan error, 1)
		go func() { released <- bus.Release(other) }()
		Eventually(released, 5*time.Second).Should(Receive(Succeed()))
	})
})
