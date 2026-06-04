package config_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

// MITMHostOwners is the load-bearing piece of D2 — a duplicate host
// across model configs is a critical error that disables the listener.
// The test exercises both happy paths (no duplicates → clean Owners
// map) and conflict detection (two configs on one host → entry in
// Conflicts naming both).

var _ = Describe("ModelConfigLoader.MITMHostOwners", func() {
	var (
		dir    string
		loader *config.ModelConfigLoader
	)

	writeYAML := func(name, body string) {
		path := filepath.Join(dir, name+".yaml")
		Expect(os.WriteFile(path, []byte(body), 0o644)).To(Succeed())
		Expect(loader.ReadModelConfig(path)).To(Succeed())
	}

	BeforeEach(func() {
		var err error
		dir, err = os.MkdirTemp("", "mitm-host-owners-test-*")
		Expect(err).ToNot(HaveOccurred())
		loader = config.NewModelConfigLoader(dir)
	})

	AfterEach(func() {
		_ = os.RemoveAll(dir)
	})

	It("returns empty maps when no model declares mitm.hosts", func() {
		writeYAML("plain", `name: plain
backend: llama-cpp
`)
		got := loader.MITMHostOwners()
		Expect(got.Owners).To(BeEmpty())
		Expect(got.Conflicts).To(BeEmpty())
	})

	It("indexes hosts to the owning model name", func() {
		writeYAML("claude", `name: claude
backend: cloud-proxy
mitm:
  hosts:
    - api.anthropic.com
`)
		writeYAML("openai", `name: openai
backend: cloud-proxy
mitm:
  hosts:
    - api.openai.com
    - api.openai.azure.com
`)
		got := loader.MITMHostOwners()
		Expect(got.Owners).To(Equal(map[string]string{
			"api.anthropic.com":    "claude",
			"api.openai.com":       "openai",
			"api.openai.azure.com": "openai",
		}))
		Expect(got.Conflicts).To(BeEmpty())
	})

	It("normalises case and trims whitespace before indexing", func() {
		writeYAML("claude", `name: claude
backend: cloud-proxy
mitm:
  hosts:
    - "  API.ANTHROPIC.com  "
`)
		got := loader.MITMHostOwners()
		Expect(got.Owners).To(HaveKey("api.anthropic.com"))
	})

	It("detects two configs claiming the same host as a conflict", func() {
		// The 1-to-1 invariant the D2 dispatcher relies on: a host
		// claimed twice means the owner lookup is ambiguous, so the
		// caller must NOT start the MITM listener until resolved.
		writeYAML("alpha", `name: alpha
backend: cloud-proxy
mitm:
  hosts:
    - api.anthropic.com
`)
		writeYAML("beta", `name: beta
backend: cloud-proxy
mitm:
  hosts:
    - api.anthropic.com
`)
		got := loader.MITMHostOwners()
		Expect(got.Conflicts).To(HaveKey("api.anthropic.com"))
		Expect(got.Conflicts["api.anthropic.com"]).To(ConsistOf("alpha", "beta"))
	})

	It("treats the same host listed twice within ONE config as a no-op (not a conflict)", func() {
		// A single config repeating a host is benign — same owner
		// either way. The conflict signal must be cross-config only.
		writeYAML("dup", `name: dup
backend: llama-cpp
mitm:
  hosts:
    - api.example.com
    - api.example.com
`)
		got := loader.MITMHostOwners()
		Expect(got.Owners).To(Equal(map[string]string{"api.example.com": "dup"}))
		Expect(got.Conflicts).To(BeEmpty())
	})

	It("ignores empty/whitespace-only host entries", func() {
		writeYAML("sloppy", `name: sloppy
backend: llama-cpp
mitm:
  hosts:
    - ""
    - "   "
    - api.real.com
`)
		got := loader.MITMHostOwners()
		Expect(got.Owners).To(Equal(map[string]string{"api.real.com": "sloppy"}))
	})
})
