// SPDX-License-Identifier: MIT
package application

import (
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("library sources on a frontend", func() {
	frontend := func(dataPath string, enabled bool) *Application {
		cfg := &config.ApplicationConfig{DataPath: dataPath, OfficialLibraries: false}
		cfg.Addons = config.AddonConfig{Frontend: true, Enabled: enabled}
		return &Application{applicationConfig: cfg}
	}

	It("refuses the store and writes nothing under the add-ons folder", func() {
		dir := realTempDir()
		a := frontend(dir, false)
		_, err := a.LibrarySources()
		Expect(err).To(MatchError(gateway.ErrUnavailable))
		Expect(a.ApplyLibraries()).To(Succeed())
		Expect(filepath.Join(dir, "addons")).NotTo(BeADirectory())
	})

	It("creates no sources.json at startup with the official libraries off", func() {
		dir := realTempDir()
		a := frontend(dir, true)
		a.applicationConfig.Addons.InstanceID = gateway.FrontendIdentity
		a.applicationConfig.Addons.OwnerID = gateway.OwnerIdentity
		applyOfficialLibrariesAtStartup(a.applicationConfig)
		entries, err := os.ReadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})

	It("still opens the store on the combined role", func() {
		dir := realTempDir()
		cfg := &config.ApplicationConfig{DataPath: dir, OfficialLibraries: true}
		a := &Application{applicationConfig: cfg}
		_, err := a.LibrarySources()
		Expect(err).NotTo(HaveOccurred())
	})
})
