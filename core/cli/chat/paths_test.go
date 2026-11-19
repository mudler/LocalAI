package chat

import (
	"os"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// richConfig stands in for a config nib has already taken ownership of: a
// comment, a secret, and a nested block. A flat scalar alone would not catch a
// writer that mangles structure or drops a key it does not know about.
const richConfig = `# hand written note
base_url: http://x.invalid/v1
api_key: secret-token
mcp_servers:
  files:
    command: mcp-files
    args:
      - --root
      - /tmp
`

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
			if runtime.GOOS == "windows" {
				Skip("Windows keeps the state in the standard data folder; the stateDir on Windows specs cover it")
			}
			tmp := GinkgoT().TempDir()
			GinkgoT().Setenv("XDG_CONFIG_HOME", "")
			GinkgoT().Setenv("HOME", tmp)
			Expect(StateDir("")).To(Equal(filepath.Join(tmp, ".config", "localai", "chat")))
		})

		It("fails when neither XDG_CONFIG_HOME nor a home directory is resolvable", func() {
			if runtime.GOOS == "windows" {
				Skip("on Windows the standard data folder is used when there is no home directory")
			}
			GinkgoT().Setenv("XDG_CONFIG_HOME", "")
			GinkgoT().Setenv("HOME", "")

			dir, err := StateDir("")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("agent state dir"))
			// No silent fallback to a relative path: writing an API key into the
			// working directory would be worse than refusing.
			Expect(dir).To(BeEmpty())
		})
	})

	Describe("stateDir on Windows", func() {
		noEnv := func(string) string { return "" }
		standard := func() (string, error) { return `/AppData/Local/LocalAI`, nil }

		It("uses the standard LocalAI folder", func() {
			home := GinkgoT().TempDir()
			dir, err := stateDir("", "windows", noEnv, func() (string, error) { return home, nil }, standard)
			Expect(err).NotTo(HaveOccurred())
			Expect(dir).To(Equal(filepath.Join(`/AppData/Local/LocalAI`, "chat")))
		})

		It("keeps the ~/.config folder of earlier releases when it exists", func() {
			home := GinkgoT().TempDir()
			legacy := filepath.Join(home, ".config", "localai", "chat")
			Expect(os.MkdirAll(legacy, 0o700)).To(Succeed())
			Expect(stateDir("", "windows", noEnv, func() (string, error) { return home, nil }, standard)).To(Equal(legacy))
		})

		It("still prefers XDG_CONFIG_HOME and the override", func() {
			xdg := func(k string) string {
				if k == "XDG_CONFIG_HOME" {
					return "/xdg"
				}
				return ""
			}
			Expect(stateDir("", "windows", xdg, os.UserHomeDir, standard)).To(Equal(filepath.Join("/xdg", "localai", "chat")))
			Expect(stateDir("/o", "windows", xdg, os.UserHomeDir, standard)).To(Equal("/o"))
		})

		It("keeps the Linux and macOS folder unchanged", func() {
			home := GinkgoT().TempDir()
			Expect(stateDir("", "linux", noEnv, func() (string, error) { return home, nil }, standard)).
				To(Equal(filepath.Join(home, ".config", "localai", "chat")))
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

		It("keeps the seeded config and its directory owner-only", func() {
			dir := filepath.Join(GinkgoT().TempDir(), "chat")
			Expect(EnsureStateDir(dir, "http://127.0.0.1:8080/v1")).To(Succeed())

			// nib writes the user's api_key into this same file, so the modes are
			// load-bearing, not cosmetic.
			config, err := os.Stat(ConfigPath(dir))
			Expect(err).ToNot(HaveOccurred())
			if runtime.GOOS != "windows" { // Windows has no permission bits; the folder ACL applies
				Expect(config.Mode().Perm()).To(Equal(os.FileMode(0o600)))
			}

			state, err := os.Stat(dir)
			Expect(err).ToNot(HaveOccurred())
			if runtime.GOOS != "windows" {
				Expect(state.Mode().Perm()).To(Equal(os.FileMode(0o700)))
			}
		})

		It("leaves an existing config byte-for-byte untouched", func() {
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(ConfigPath(dir), []byte(richConfig), 0o600)).To(Succeed())

			Expect(EnsureStateDir(dir, "http://127.0.0.1:8080/v1")).To(Succeed())

			data, err := os.ReadFile(ConfigPath(dir))
			Expect(err).ToNot(HaveOccurred())
			// Byte-exact against a fixture carrying a comment and a nested block:
			// an implementation that "preserves" by re-marshaling through a map
			// fails here rather than passing on a flat scalar.
			Expect(string(data)).To(Equal(richConfig))
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

		It("preserves secrets and nested blocks it does not understand", func() {
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(ConfigPath(dir), []byte(richConfig), 0o600)).To(Succeed())

			Expect(PersistModel(dir, "chosen-model")).To(Succeed())

			data, err := os.ReadFile(ConfigPath(dir))
			Expect(err).ToNot(HaveOccurred())

			var got map[string]any
			Expect(yaml.Unmarshal(data, &got)).To(Succeed())
			Expect(got).To(HaveKeyWithValue("model", "chosen-model"))
			Expect(got).To(HaveKeyWithValue("base_url", "http://x.invalid/v1"))
			// Losing this key logs the user out of their own server.
			Expect(got).To(HaveKeyWithValue("api_key", "secret-token"))
			Expect(got).To(HaveKeyWithValue("mcp_servers",
				HaveKeyWithValue("files", And(
					HaveKeyWithValue("command", "mcp-files"),
					HaveKeyWithValue("args", ConsistOf("--root", "/tmp")),
				)),
			))

			// Documented, accepted behavior rather than an aspiration: the overlay
			// re-marshals, so comments do not survive. nib's own save path erases
			// them too, so preserving them here would buy nothing.
			Expect(string(data)).ToNot(ContainSubstring("# hand written note"))
		})

		It("keeps the rewritten config owner-only and leaves no temp file behind", func() {
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(ConfigPath(dir), []byte(richConfig), 0o600)).To(Succeed())

			Expect(PersistModel(dir, "chosen-model")).To(Succeed())

			info, err := os.Stat(ConfigPath(dir))
			Expect(err).ToNot(HaveOccurred())
			if runtime.GOOS != "windows" { // Windows has no permission bits; the folder ACL applies
				Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
			}

			// The atomic write stages through a sibling temp file; it must not
			// survive a successful write.
			entries, err := os.ReadDir(dir)
			Expect(err).ToNot(HaveOccurred())
			names := []string{}
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			Expect(names).To(ConsistOf("config.yaml"))
		})

		It("creates the state directory when it does not exist yet", func() {
			// Task 4 may persist a picked model before anything else has run.
			dir := filepath.Join(GinkgoT().TempDir(), "chat")

			Expect(PersistModel(dir, "chosen-model")).To(Succeed())

			data, err := os.ReadFile(ConfigPath(dir))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(data)).To(ContainSubstring("model: chosen-model"))
		})
	})
})
