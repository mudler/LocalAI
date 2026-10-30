package notifytest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/notify"
)

// NewPushStore returns an empty push store. It runs inside a spec, so it may
// use GinkgoT().TempDir() and DeferCleanup.
type NewPushStore func() notify.PushStore

func sub(p, endpoint string, at time.Time) notify.PushSubscription {
	return notify.PushSubscription{Principal: p, Endpoint: endpoint, P256dh: "key-" + endpoint, Auth: "auth-" + endpoint, Created: at}
}

func endpoints(ss []notify.PushSubscription) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Endpoint
	}
	return out
}

// RunPush registers the contract specs for the PushStores newStore returns.
func RunPush(newStore NewPushStore) {
	ctx := context.Background()
	It("subscribes, lists and unsubscribes", func() {
		s := newStore()
		Expect(s.Subscribe(ctx, sub("u1", "https://push/a", t0))).To(Succeed())
		_ = s.Subscribe(ctx, sub("u1", "https://push/b", t0.Add(time.Second)))
		_ = s.Subscribe(ctx, sub("u2", "https://push/c", t0))
		got, err := s.ForPrincipal(ctx, "u1")
		Expect(err).NotTo(HaveOccurred())
		Expect(endpoints(got)).To(Equal([]string{"https://push/a", "https://push/b"}))
		Expect(got[0].P256dh).To(Equal("key-https://push/a"))
		Expect(got[0].Auth).To(Equal("auth-https://push/a"))
		_ = s.Unsubscribe(ctx, "u2", "https://push/a") // not u2's
		got, _ = s.ForPrincipal(ctx, "u1")
		Expect(got).To(HaveLen(2), "another user removed a subscription")
		_ = s.Unsubscribe(ctx, "u1", "https://push/a")
		got, _ = s.ForPrincipal(ctx, "u1")
		Expect(endpoints(got)).To(Equal([]string{"https://push/b"}), "after unsubscribe")
	})
	It("gives an endpoint to the last user to subscribe it", func() {
		s := newStore()
		_ = s.Subscribe(ctx, sub("u1", "https://push/a", t0))
		_ = s.Subscribe(ctx, sub("u2", "https://push/a", t0.Add(time.Second)))
		got, _ := s.ForPrincipal(ctx, "u1")
		Expect(got).To(BeEmpty(), "u1 still has subscriptions")
		got, _ = s.ForPrincipal(ctx, "u2")
		Expect(got).To(HaveLen(1))
	})
	It("drops an endpoint whoever owns it", func() {
		s := newStore()
		_ = s.Subscribe(ctx, sub("u1", "https://push/a", t0))
		Expect(s.Drop(ctx, "https://push/a")).To(Succeed())
		got, _ := s.ForPrincipal(ctx, "u1")
		Expect(got).To(BeEmpty(), "after drop")
	})
	It("trims to the newest subscriptions", func() {
		s := newStore()
		for i := range notify.MaxPushSubscriptions + 3 {
			_ = s.Subscribe(ctx, sub("u1", fmt.Sprintf("https://push/%02d", i), t0.Add(time.Duration(i)*time.Second)))
		}
		_ = s.Subscribe(ctx, sub("u2", "https://push/other", t0))
		got, _ := s.ForPrincipal(ctx, "u1")
		Expect(got).To(HaveLen(notify.MaxPushSubscriptions), "kept %v", endpoints(got))
		Expect(got[0].Endpoint).To(Equal("https://push/03"))
		other, _ := s.ForPrincipal(ctx, "u2")
		Expect(other).To(HaveLen(1), "trimming u1 touched u2")
	})
	It("makes the VAPID keys once even when calls race", func() {
		s := newStore()
		var mu sync.Mutex
		made := 0
		gen := func() (string, string, error) {
			mu.Lock()
			made++
			n := made
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			return fmt.Sprintf("priv-%d", n), fmt.Sprintf("pub-%d", n), nil
		}
		results := make(chan [2]string, 8)
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				p, q, err := s.EnsureVAPID(ctx, gen)
				Expect(err).NotTo(HaveOccurred())
				results <- [2]string{p, q}
			}()
		}
		wg.Wait()
		close(results)
		var first [2]string
		for r := range results {
			if first == ([2]string{}) {
				first = r
			}
			Expect(r).To(Equal(first), "two callers got different keys")
		}
		again, _, _ := s.EnsureVAPID(ctx, func() (string, string, error) { return "", "", errors.New("must not generate again") })
		Expect(again).To(Equal(first[0]), "a later call got another key")
	})
}

// SeedPush stores a subscription and the keys; CheckPushSeed finds them in
// another store on the same data.
func SeedPush(s notify.PushStore) {
	_ = s.Subscribe(context.Background(), sub("u1", "https://push/seed", t0))
	_, _, err := s.EnsureVAPID(context.Background(), func() (string, string, error) { return "kept-private", "kept-public", nil })
	Expect(err).NotTo(HaveOccurred())
}

// CheckPushSeed checks what SeedPush wrote.
func CheckPushSeed(s notify.PushStore) {
	got, err := s.ForPrincipal(context.Background(), "u1")
	Expect(err).NotTo(HaveOccurred())
	Expect(got).To(HaveLen(1))
	Expect(got[0].Endpoint).To(Equal("https://push/seed"))
	priv, pub, err := s.EnsureVAPID(context.Background(), func() (string, string, error) { return "new", "new", nil })
	Expect(err).NotTo(HaveOccurred())
	Expect(priv).To(Equal("kept-private"))
	Expect(pub).To(Equal("kept-public"))
}
