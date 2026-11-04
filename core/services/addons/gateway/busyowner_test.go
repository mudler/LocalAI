// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("apps list while the owner is busy", func() {
	alice := contract.Actor{UserID: "alice", CredentialID: "key:alice", Mode: "named-user"}
	bob := contract.Actor{UserID: "bob", CredentialID: "key:bob", Mode: "named-user"}
	allow := func(context.Context, contract.Actor) error { return nil }

	// busyOwner serves the apps list until stall is set, then holds every
	// request until the client gives up, as an owner that waits for an upgrade.
	busyOwner := func() (*Gateway, *Gateway, *atomic.Bool) {
		stall := &atomic.Bool{}
		owner := &Gateway{Manager: ownerManager(alice, true), Revalidate: allow}
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(owner)
			Expect(e).NotTo(HaveOccurred())
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/addons/forward" && stall.Load() {
					select {
					case <-r.Context().Done():
					case <-time.After(10 * time.Second):
					}
					return
				}
				h.ServeHTTP(w, r)
			})
		})
		cs["front-a"].config.ReadTimeout = 300 * time.Millisecond
		return &Gateway{Forward: cs["front-a"], Revalidate: allow}, owner, stall
	}

	It("returns a typed timeout in the read deadline, not after the client timeout", func() {
		front, _, stall := busyOwner()
		stall.Store(true)
		started := time.Now()
		_, e := front.Apps(context.Background(), alice)
		Expect(time.Since(started)).To(BeNumerically("<", 3*time.Second))
		var timeout *OwnerTimeout
		Expect(e).To(HaveOccurred())
		Expect(e).To(BeAssignableToTypeOf(timeout))
		Expect(StatusFor(e)).To(Equal(http.StatusServiceUnavailable))
		Expect(e).To(MatchError(ErrUnavailable))
	})

	It("answers with the last good list marked stale, and only for the same person", func() {
		front, _, stall := busyOwner()
		fresh, e := front.Apps(context.Background(), alice)
		Expect(e).NotTo(HaveOccurred())
		Expect(fresh.Stale).To(BeFalse())
		Expect(fresh.Apps).NotTo(BeEmpty())

		stall.Store(true)
		list, e := front.Apps(context.Background(), alice)
		Expect(e).NotTo(HaveOccurred())
		Expect(list.Stale).To(BeTrue())
		Expect(list.StaleReason).To(Equal("owner_busy"))
		Expect(list.StaleSince).NotTo(BeEmpty())
		Expect(list.Apps).To(HaveLen(len(fresh.Apps)))

		_, e = front.Apps(context.Background(), bob)
		Expect(e).To(HaveOccurred(), "another person never gets alice's list")

		stall.Store(false)
		again, e := front.Apps(context.Background(), alice)
		Expect(e).NotTo(HaveOccurred())
		Expect(again.Stale).To(BeFalse())
	})

	It("lets a cached list expire", func() {
		front, _, stall := busyOwner()
		now := time.Now()
		front.appsCache.now = func() time.Time { return now }
		_, e := front.Apps(context.Background(), alice)
		Expect(e).NotTo(HaveOccurred())
		stall.Store(true)
		now = now.Add(AppsStaleTTL + time.Second)
		_, e = front.Apps(context.Background(), alice)
		Expect(e).To(HaveOccurred())
	})

	It("does not hide a denial behind the cache", func() {
		front, _, _ := busyOwner()
		_, e := front.Apps(context.Background(), alice)
		Expect(e).NotTo(HaveOccurred())
		front.Revalidate = func(context.Context, contract.Actor) error { return ErrForbidden }
		_, e = front.Apps(context.Background(), alice)
		Expect(e).To(HaveOccurred())
		Expect(StatusFor(e)).To(Equal(http.StatusForbidden))
	})
})
