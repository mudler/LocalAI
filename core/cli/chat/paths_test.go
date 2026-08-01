package chat

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Agent state directory", func() {
	Describe("StateDir", func() {
		It("prefers an explicit override", func() {
			Expect(StateDir("/custom/dir")).To(Equal("/custom/dir"))
		})

		It("uses XDG_CONFIG_HOME when set", func() {
			tmp := GinkgoT().TempDir()
			GinkgoT().Setenv("XDG_CONFIG_HOME", tmp)
			Expect(StateDir("")).To(Equal(filepath.Join(tmp, "localai", "chat")))
		})

		It("falls back to ~/.config/localai/chat", func() {
			tmp := GinkgoT().TempDir()
			GinkgoT().Setenv("XDG_CONFIG_HOME", "")
			GinkgoT().Setenv("HOME", tmp)
			Expect(StateDir("")).To(Equal(filepath.Join(tmp, ".config", "localai", "chat")))
		})
	})

	Describe("EnsureStateDir", func() {
		It("creates the directory and seeds base_url on first run", func() {
			dir := filepath.Join(GinkgoT().TempDir(), "chat")
			Expect(EnsureStateDir(dir, "http://127.0.0.1:8080/v1")).To(Succeed())

			data, err := os.ReadFile(ConfigPath(dir))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(data)).To(ContainSubstring("base_url: http://127.0.0.1:8080/v1"))
			// A model must NOT be seeded: it goes stale as soon as the user
			// installs a different one.
			Expect(string(data)).ToNot(ContainSubstring("model:"))
		})

		It("leaves an existing config untouched", func() {
			dir := GinkgoT().TempDir()
			original := "model: mine\nbase_url: http://elsewhere.invalid/v1\n"
			Expect(os.WriteFile(ConfigPath(dir), []byte(original), 0o600)).To(Succeed())

			Expect(EnsureStateDir(dir, "http://127.0.0.1:8080/v1")).To(Succeed())

			data, err := os.ReadFile(ConfigPath(dir))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(data)).To(Equal(original))
		})
	})

	Describe("PersistModel", func() {
		It("adds a model to an existing config, preserving other keys", func() {
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(ConfigPath(dir), []byte("base_url: http://x.invalid/v1\n"), 0o600)).To(Succeed())

			Expect(PersistModel(dir, "chosen-model")).To(Succeed())

			data, err := os.ReadFile(ConfigPath(dir))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(data)).To(ContainSubstring("base_url: http://x.invalid/v1"))
			Expect(string(data)).To(ContainSubstring("model: chosen-model"))
		})

		It("replaces an existing model rather than duplicating the key", func() {
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(ConfigPath(dir), []byte("model: old\nbase_url: http://x.invalid/v1\n"), 0o600)).To(Succeed())

			Expect(PersistModel(dir, "new")).To(Succeed())

			data, err := os.ReadFile(ConfigPath(dir))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(data)).To(ContainSubstring("model: new"))
			Expect(string(data)).ToNot(ContainSubstring("model: old"))
		})
	})
})
