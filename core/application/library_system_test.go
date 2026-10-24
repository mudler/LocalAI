// SPDX-License-Identifier: MIT
package application

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the owner's library system call", func() {
	var checks int
	owner := func() *libraryOwner {
		return &libraryOwner{
			adminOnly: func(context.Context, contract.Actor) error { checks++; return gateway.ErrForbidden },
			installer: func() *backendlib.Installer { return nil },
		}
	}
	BeforeEach(func() { checks = 0 })

	It("runs the release check without an actor and without the administrator check", func() {
		_, err := owner().Library(context.Background(), contract.Actor{}, gateway.LibraryCall{Op: gateway.LibraryBackendReleases, System: true})
		// The test installer is absent, so the verb itself is unavailable; what
		// matters is that the administrator check did not refuse it first.
		Expect(err).To(MatchError(gateway.ErrUnavailable))
		Expect(checks).To(BeZero())
	})

	It("refuses any other verb marked as a system call", func() {
		_, err := owner().Library(context.Background(), contract.Actor{}, gateway.LibraryCall{Op: gateway.LibraryRemove, ID: "x", System: true})
		Expect(err).To(MatchError(gateway.ErrForbidden))
	})

	It("still refuses a user-facing call without an actor", func() {
		_, err := owner().Library(context.Background(), contract.Actor{}, gateway.LibraryCall{Op: gateway.LibraryBackendReleases})
		Expect(err).To(MatchError(gateway.ErrForbidden))
		Expect(checks).To(Equal(1))
	})
})

var _ = Describe("the frontend's library actor", func() {
	It("gives the background loop no actor in named-user mode", func() {
		cfg := &config.ApplicationConfig{}
		cfg.Addons = config.AddonConfig{Frontend: true, Enabled: true}
		a := &Application{applicationConfig: cfg}
		_, err := a.libraryActor(context.Background())
		Expect(err).To(MatchError(gateway.ErrForbidden))
	})
})
