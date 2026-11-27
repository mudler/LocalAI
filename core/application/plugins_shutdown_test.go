// SPDX-License-Identifier: MIT
package application

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The refresh loop of the Plugins service reads and writes under DataPath
// (scratch folders of the fetches, the package files). Shutdown must stop it
// and wait for it, or whoever removes the directory next races its writes.
var _ = Describe("the plugins refresh loop at shutdown", func() {
	It("has returned when Shutdown returns", func() {
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()), system.WithBackendPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		app, err := New(config.WithSystemState(state), config.DisableMetricsEndpoint,
			config.WithDisableStats(true), config.WithDataPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		Expect(app.Plugins()).NotTo(BeNil(), "the service starts with a data path and the chats' files")
		Expect(app.pluginsDone).NotTo(BeNil())
		Expect(app.Shutdown()).To(Succeed())
		Expect(app.pluginsDone).To(BeClosed())
	})
})
