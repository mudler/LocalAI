// SPDX-License-Identifier: MIT
package cli

import (
	"os"

	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("automatic add-on upgrade flag", func() {
	DescribeTable("is off unless asked for", func(env string, args []string, want bool) {
		GinkgoT().Setenv("LOCALAI_AUTO_UPGRADE_ADDONS", "false")
		Expect(os.Unsetenv("LOCALAI_AUTO_UPGRADE_ADDONS")).To(Succeed())
		if env != "" {
			GinkgoT().Setenv("LOCALAI_AUTO_UPGRADE_ADDONS", env)
		}
		cmd := struct {
			Run RunCMD `cmd:""`
		}{}
		parser, err := kong.New(&cmd, kong.Vars{"basepath": GinkgoT().TempDir(), "galleries": "[]", "backends": "[]",
			"generatedcontentpath": GinkgoT().TempDir(), "uploadpath": GinkgoT().TempDir(), "version": "test"})
		Expect(err).NotTo(HaveOccurred())
		_, err = parser.Parse(append([]string{"run"}, args...))
		Expect(err).NotTo(HaveOccurred())
		Expect(cmd.Run.AutoUpgradeAddons).To(Equal(want))
	},
		Entry("default", "", []string{}, false),
		Entry("flag", "", []string{"--auto-upgrade-addons"}, true),
		Entry("environment", "true", []string{}, true),
		Entry("explicit false", "", []string{"--auto-upgrade-addons=false"}, false),
	)
})
