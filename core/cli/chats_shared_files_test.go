// SPDX-License-Identifier: MIT
package cli

import (
	"github.com/alecthomas/kong"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("shared filesystem CLI", func() {
	DescribeTable("forwards real Kong flags and environment without path normalization", func(env string, args []string, want string) {
		GinkgoT().Setenv("LOCALAI_CHATS_SHARED_FILES_DIR", env)
		var cmd RunCMD
		parser, err := kong.New(&cmd, kong.Vars{"basepath": GinkgoT().TempDir(), "galleries": "[]", "backends": "[]", "generatedcontentpath": DefaultGeneratedContentPath(), "uploadpath": DefaultUploadPath()})
		Expect(err).NotTo(HaveOccurred())
		_, err = parser.Parse(args)
		Expect(err).NotTo(HaveOccurred())
		cfg := config.NewApplicationConfig(cmd.chatsSharedFilesOption())
		Expect(cfg.ChatsSharedFilesDir).To(Equal(want))
	}, Entry("default", "", []string{}, ""),
		Entry("flag", "", []string{"--chats-shared-files-dir=/srv/workspace"}, "/srv/workspace"),
		Entry("environment", "/srv/env", []string{}, "/srv/env"),
		Entry("flag overrides environment", "/srv/env", []string{"--chats-shared-files-dir=/srv/flag"}, "/srv/flag"),
		Entry("relative remains invalid", "", []string{"--chats-shared-files-dir=relative"}, "relative"),
		Entry("traversal remains invalid", "/srv/a/../b", []string{}, "/srv/a/../b"))
})
