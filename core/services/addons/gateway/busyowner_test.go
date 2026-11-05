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

	It("never answers with an older list of its own: every frontend gives the same typed error", func() {
		front, _, stall := busyOwner()
		fresh, e := front.Apps(context.Background(), alice)
		Expect(e).NotTo(HaveOccurred())
		Expect(fresh.Apps).NotTo(BeEmpty())

		// This frontend has a good list of alice. A frontend that never read
		// one must not differ from it, so the first one gives no list either.
		stall.Store(true)
		list, e := front.Apps(context.Background(), alice)
		Expect(e).To(HaveOccurred())
		Expect(list.Apps).To(BeEmpty())
		var timeout *OwnerTimeout
		Expect(e).To(BeAssignableToTypeOf(timeout))

		stall.Store(false)
		again, e := front.Apps(context.Background(), alice)
		Expect(e).NotTo(HaveOccurred())
		Expect(again.Apps).To(HaveLen(len(fresh.Apps)))
	})

	It("does not hide a denial", func() {
		front, _, _ := busyOwner()
		_, e := front.Apps(context.Background(), alice)
		Expect(e).NotTo(HaveOccurred())
		front.Revalidate = func(context.Context, contract.Actor) error { return ErrForbidden }
		_, e = front.Apps(context.Background(), alice)
		Expect(e).To(HaveOccurred())
		Expect(StatusFor(e)).To(Equal(http.StatusForbidden))
	})
})
