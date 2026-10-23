// SPDX-License-Identifier: MIT
package cli

import (
	"os"

	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("development backend preference", func() {
	DescribeTable("exposes the embedded distributed worker flag", func(args []string, want bool) {
		for _, key := range []string{"LOCALAI_PREFER_DEV_BACKENDS", "PREFER_DEV_BACKENDS"} {
			GinkgoT().Setenv(key, "false")
			Expect(os.Unsetenv(key)).To(Succeed())
		}
		cmd := struct {
			Worker WorkerCMD `cmd:""`
		}{}
		parser, err := kong.New(&cmd, kong.Vars{"basepath": GinkgoT().TempDir(), "galleries": "[]", "backends": "[]"})
		Expect(err).NotTo(HaveOccurred())
		_, err = parser.Parse(append([]string{"worker", "--nats-url=nats://example.invalid:4222", "--register-to=http://example.invalid:8080"}, args...))
		Expect(err).NotTo(HaveOccurred())
		Expect(cmd.Worker.PreferDevelopmentBackends).To(Equal(want))
	}, Entry("default", []string{}, false), Entry("enabled", []string{"--prefer-development-backends"}, true), Entry("explicit false", []string{"--prefer-development-backends=false"}, false))
})
