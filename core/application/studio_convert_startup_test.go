//go:build auth

package application

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
)

var _ = Describe("the conversion of the old studio data", func() {
	// The conversion of the old data can take long, and in a cluster it waits for
	// a lock another frontend holds. It must never delay the start of the server,
	// also when the collections service is off.
	It("with collections off does not block startup", func() {
		release := make(chan struct{})
		started := make(chan struct{})
		old := runStudioConversion
		runStudioConversion = func(*Application) {
			close(started)
			<-release
		}
		DeferCleanup(func() { runStudioConversion = old })

		a := &Application{applicationConfig: &config.ApplicationConfig{DataPath: GinkgoT().TempDir()}}
		a.applicationConfig.Collections.Enabled = false

		st, err := store.NewJSON("")
		Expect(err).ToNot(HaveOccurred())
		svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: studiotest.NewGateHarness()})
		Expect(err).ToNot(HaveOccurred())

		returned := make(chan struct{})
		go func() {
			a.prepareStudioConversion(svc)
			close(returned)
		}()
		select {
		case <-returned:
		case <-time.After(5 * time.Second):
			close(release)
			Fail("prepareStudioConversion waited for the conversion")
		}
		Eventually(started, 5*time.Second).Should(BeClosed(), "the conversion never started")
		close(release)
	})
})
