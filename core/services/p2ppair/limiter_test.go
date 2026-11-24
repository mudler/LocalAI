package p2ppair_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2ppair"
)

var _ = Describe("the default limiter", func() {
	var (
		now time.Time
		l   p2ppair.Limiter
	)

	BeforeEach(func() {
		now = time.Unix(1_000_000, 0)
		l = p2ppair.NewLimiter(func() time.Time { return now })
	})

	It("lets only the cap through when attempts arrive at once", func() {
		p := newPeer()
		var wg sync.WaitGroup
		var ok atomic.Int32
		for range 50 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if allowed, _ := l.Attempt(p, ""); allowed {
					ok.Add(1)
				}
			}()
		}
		wg.Wait()
		Expect(ok.Load()).To(BeEquivalentTo(p2ppair.MaxFailuresPerPeer))
	})

	It("gives an attempt back once", func() {
		p := newPeer()
		for range p2ppair.MaxFailuresPerPeer {
			allowed, refund := l.Attempt(p, "192.0.2.1")
			Expect(allowed).To(BeTrue())
			refund()
			refund()
		}
		allowed, _ := l.Attempt(p, "192.0.2.1")
		Expect(allowed).To(BeTrue())
	})

	It("does not grow without end with peers that never come back", func() {
		for i := range 30000 {
			_, _ = l.Attempt(newPeer(), fmt.Sprintf("10.0.%d.%d", i/250, i%250))
			now = now.Add(10 * time.Millisecond)
		}
		Expect(p2ppair.LimiterSize(l)).To(BeNumerically("<=", 2*10000))
	})
})

// failingStore returns an error that is not a wrong code from Redeem.
type failingStore struct{ p2ppair.Store }

func (failingStore) Redeem(context.Context, p2ppair.RedeemParams) (p2ppair.Invite, p2ppair.Device, error) {
	return p2ppair.Invite{}, p2ppair.Device{}, errors.New("database is down")
}

var _ = Describe("redeem with a store that fails", func() {
	It("does not count the failure as a wrong code", func() {
		st, err := p2ppair.NewJSONStore("", "secret")
		Expect(err).NotTo(HaveOccurred())
		svc, err := p2ppair.NewService(failingStore{st}, p2ppair.Config{ServerPeer: newPeer()})
		Expect(err).NotTo(HaveOccurred())
		from := newPeer()
		for range 3 * p2ppair.MaxFailuresPerPeer {
			_, err := svc.Redeem(context.Background(), from, p2ppair.RedeemRequest{V: 1, Code: "ZZZZZZZZZ2"})
			Expect(err).To(MatchError(ContainSubstring("database is down")))
		}
	})
})
