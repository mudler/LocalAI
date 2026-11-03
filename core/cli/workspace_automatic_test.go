// SPDX-License-Identifier: MIT
package cli

import (
	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("automatic workspace CLI", func() {
	It("has no custom workspace path flag", func() {
		var cmd RunCMD
		parser, err := kong.New(&cmd, kong.Vars{"basepath": GinkgoT().TempDir(), "galleries": "[]", "backends": "[]", "generatedcontentpath": DefaultGeneratedContentPath(), "uploadpath": DefaultUploadPath()})
		Expect(err).NotTo(HaveOccurred())
		_, err = parser.Parse([]string{"--chats-shared-files-dir=/srv/workspace"})
		Expect(err).To(MatchError(ContainSubstring("unknown flag")))
	})
})
