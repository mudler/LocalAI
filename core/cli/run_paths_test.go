package cli

import (
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Regression for the startup failure observed when a second OS account (or a
// leftover root-owned directory) already created the shared /tmp locations:
//
//	unable to create ImageDir: "mkdir /tmp/generated/content: permission denied"
//
// The historical defaults (/tmp/generated/content and /tmp/localai/upload) are
// shared across every user of a machine. On macOS /tmp is routed to the shared
// /private/tmp for all accounts, so the first account to run LocalAI creates the
// parent with 0750 perms and locks everyone else out. The defaults must instead
// be scoped to the current user so unrelated accounts never collide.
var _ = Describe("default writable paths", func() {
	userScope := fmt.Sprintf("localai-%d", os.Getuid())

	Describe("DefaultGeneratedContentPath", func() {
		It("is scoped to the current user under the OS temp dir", func() {
			p := DefaultGeneratedContentPath()
			Expect(p).To(HavePrefix(os.TempDir()))
			Expect(p).To(ContainSubstring(userScope))
			Expect(p).To(HaveSuffix(filepath.Join("generated", "content")))
		})

		It("is not the historical shared path", func() {
			Expect(DefaultGeneratedContentPath()).ToNot(Equal("/tmp/generated/content"))
		})
	})

	Describe("DefaultUploadPath", func() {
		It("is scoped to the current user under the OS temp dir", func() {
			p := DefaultUploadPath()
			Expect(p).To(HavePrefix(os.TempDir()))
			Expect(p).To(ContainSubstring(userScope))
			Expect(p).To(HaveSuffix("upload"))
		})

		It("is not the historical shared path", func() {
			Expect(DefaultUploadPath()).ToNot(Equal("/tmp/localai/upload"))
		})
	})
})
