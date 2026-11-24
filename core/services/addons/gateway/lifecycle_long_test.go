// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("long lifecycle calls", func() {
	It("classifies the lifecycle actions that run for minutes and keeps quick calls short", func() {
		for _, a := range []string{"act", "install", "upgrade", "enable", "disable", "uninstall", ActionUpdateRunQueued,
			ActionRecoveryBackup, ActionRecoveryVerify, ActionRecoveryRestore, ActionRecoveryDeleteBackup, ActionRecoveryPrune, ActionRecoveryPurge} {
			Expect(longCall(a)).To(BeTrue(), a)
		}
		for _, a := range []string{"read", "operation-status", ActionUpdateStatus, ActionUpdateCheck, ActionRecoveryStatus, "addons-installed", ActionUpdateQueue} {
			Expect(longCall(a)).To(BeFalse(), a)
		}
	})

	It("lets an owner answer an install after the short deadline and the server write deadline", func() {
		actor := contract.Actor{UserID: "u", CredentialID: "k", Mode: "named-user"}
		root := filepath.Join(GinkgoT().TempDir(), "owner")
		manager, e := addons.NewManager(context.Background(), addons.OwnerConfig{InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "disk", Root: root}, catalog.Catalog{}, &packages.Installer{Root: filepath.Join(root, "packages")}, func(context.Context, contract.Actor) error { return nil })
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(manager.Close)
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(&Gateway{Manager: manager, Revalidate: func(context.Context, contract.Actor) error { return nil }})
			Expect(e).NotTo(HaveOccurred())
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// What http.Server does for its WriteTimeout, with a short one.
				_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(150 * time.Millisecond))
				if r.URL.Path == "/addons/forward" {
					// The owner works longer than the short client allows.
					time.Sleep(400 * time.Millisecond)
				}
				h.ServeHTTP(w, r)
			})
		})
		// Bound the short client without sleeping 65 seconds.
		cs["front-a"].client.Timeout = 200 * time.Millisecond
		install := contract.Request{AddonID: "sample", OperationID: "slow-install", Action: "install", Arguments: []byte(`{"sourceId":"absent","version":"1.0.0"}`)}
		_, e = cs["front-a"].Dispatch(context.Background(), actor, install)
		// The owner answers with its own typed error. The call must not end as
		// an unknown outcome or as unavailable because a client or a
		// connection gave up first.
		var unknown *OutcomeUnknown
		Expect(errors.As(e, &unknown)).To(BeFalse(), "got %v", e)
		Expect(e).To(HaveOccurred())
		Expect(errors.Is(e, ErrUnavailable)).To(BeFalse(), "got %v", e)

		// A quick mutation that is not listed keeps the short client and fails closed.
		_, e = cs["front-a"].Dispatch(context.Background(), actor, contract.Request{AddonID: "sample", OperationID: "slow-other", Action: "confirm", Arguments: []byte(`{}`)})
		Expect(errors.As(e, &unknown)).To(BeTrue(), "got %v", e)
	})
})
