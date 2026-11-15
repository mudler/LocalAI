package remotes_test

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/remotes"
)

var _ = Describe("model diff", func() {
	owned := func(pairs ...string) []remotes.OwnedEntry {
		var out []remotes.OwnedEntry
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, remotes.OwnedEntry{Name: pairs[i], UpstreamID: pairs[i+1]})
		}
		return out
	}
	um := func(ids ...string) []remotes.UpstreamModel {
		var out []remotes.UpstreamModel
		for _, id := range ids {
			out = append(out, remotes.UpstreamModel{ID: id})
		}
		return out
	}

	It("finds new, unavailable and kept models, sorted", func() {
		d := remotes.ModelDiffOf(um("z", "c", "a", "b"), owned("local-a", "a", "gone", "x", "local-b", "b"))
		Expect(ids(d.New)).To(Equal([]string{"c", "z"}))
		Expect(d.Unavailable).To(Equal([]string{"gone"}))
		Expect(d.Kept).To(Equal([]string{"local-a", "local-b"}))
	})
	It("handles empty input without nil slices", func() {
		d := remotes.ModelDiffOf(nil, nil)
		Expect(d.New).NotTo(BeNil())
		Expect(d.Unavailable).NotTo(BeNil())
		Expect(d.Kept).NotTo(BeNil())
		Expect(d.New).To(BeEmpty())
	})
	It("everything is unavailable when the remote lists nothing", func() {
		d := remotes.ModelDiffOf(nil, owned("a", "a", "b", "b"))
		Expect(d.Unavailable).To(Equal([]string{"a", "b"}))
	})
	It("ignores duplicates and empty ids in the listing", func() {
		d := remotes.ModelDiffOf(um("a", "a", ""), nil)
		Expect(ids(d.New)).To(Equal([]string{"a"}))
	})
	It("does not touch its input", func() {
		up := um("b", "a")
		o := owned("x", "a")
		remotes.ModelDiffOf(up, o)
		Expect(ids(up)).To(Equal([]string{"b", "a"}))
		Expect(o).To(Equal(owned("x", "a")))
	})
	It("gives the same answer for the same input", func() {
		up := um("m3", "m1", "m2")
		first := remotes.ModelDiffOf(up, nil)
		for range 5 {
			Expect(remotes.ModelDiffOf(up, nil)).To(Equal(first))
		}
	})
})

var _ = Describe("entry naming", func() {
	DescribeTable("sanitise",
		func(in, want string) { Expect(remotes.SanitizeName(in)).To(Equal(want)) },
		Entry("plain", "gpt-4o", "gpt-4o"),
		Entry("slash", "meta-llama/Llama-3.1-8B", "meta-llama-Llama-3.1-8B"),
		Entry("colon", "llama3:8b", "llama3-8b"),
		Entry("spaces and runs", "a  b //c", "a-b-c"),
		Entry("dots", "../etc/passwd", "etc-passwd"),
		Entry("double dots inside", "a..b", "a.b"),
		Entry("ends trimmed", "-.name.-", "name"),
		Entry("unicode", "modèle", "mod-le"),
		Entry("nothing left", "///", ""),
		Entry("path separator", "a\\b", "a-b"),
	)
	It("caps the length", func() {
		Expect(len(remotes.SanitizeName(strings.Repeat("a", 500)))).To(Equal(120))
	})
	It("is stable", func() {
		Expect(remotes.SanitizeName("x/y:z")).To(Equal(remotes.SanitizeName("x/y:z")))
	})
	It("makes a slug from a remote name", func() {
		Expect(remotes.RemoteSlug("My Home Server")).To(Equal("my-home-server"))
		Expect(remotes.RemoteSlug("???")).To(Equal("remote"))
	})

	taken := func(names ...string) func(string) bool {
		return func(n string) bool {
			for _, t := range names {
				if t == n {
					return true
				}
			}
			return false
		}
	}
	It("uses the id when it is free", func() {
		n, ok := remotes.ChooseEntryName("meta/llama", "home", taken())
		Expect(ok).To(BeTrue())
		Expect(n).To(Equal("meta-llama"))
	})
	It("prefixes with the remote slug when the id is taken", func() {
		n, ok := remotes.ChooseEntryName("gpt-4o", "work", taken("gpt-4o"))
		Expect(ok).To(BeTrue())
		Expect(n).To(Equal("work-gpt-4o"))
	})
	It("gives up when both names are taken", func() {
		_, ok := remotes.ChooseEntryName("gpt-4o", "work", taken("gpt-4o", "work-gpt-4o"))
		Expect(ok).To(BeFalse())
	})
	It("gives up when the id has no usable characters", func() {
		_, ok := remotes.ChooseEntryName("///", "work", taken())
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("entry YAML", func() {
	// Each entry is written to a models directory and read back by the real
	// config loader, as a start of the server would.
	load := func(name string, body []byte) config.ModelConfig {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, name+".yaml"), body, 0o644)).To(Succeed())
		loader := config.NewModelConfigLoader(dir)
		Expect(loader.LoadModelConfigsFromPath(dir)).To(Succeed())
		cfg, ok := loader.GetModelConfig(name)
		Expect(ok).To(BeTrue())
		valid, err := cfg.Validate()
		Expect(err).NotTo(HaveOccurred())
		Expect(valid).To(BeTrue())
		return cfg
	}
	noCredential := func(body []byte) {
		for _, k := range []string{"api_key", "api_key_env", "api_key_file"} {
			Expect(string(body)).NotTo(ContainSubstring(k))
		}
	}

	It("builds a localai entry", func() {
		body, err := remotes.BuildEntry(remotes.KindLocalAI, "https://remote.example.com/v1", "qwen", "c1", remotes.UpstreamModel{ID: "qwen/3:8b", Usecases: []string{"chat", "vision"}})
		Expect(err).NotTo(HaveOccurred())
		noCredential(body)
		cfg := load("qwen", body)
		Expect(cfg.Backend).To(Equal("localai-proxy"))
		Expect(cfg.KnownUsecaseStrings).To(Equal([]string{"FLAG_CHAT", "FLAG_VISION"}), "the loader writes the flag names")
		Expect(cfg.HasUsecases(config.FLAG_VISION)).To(BeTrue())
		Expect(cfg.Proxy.UpstreamURL).To(Equal("https://remote.example.com"))
		Expect(cfg.Proxy.UpstreamModel).To(Equal("qwen/3:8b"))
		Expect(cfg.Proxy.Remote).To(Equal("c1"))
	})
	It("builds an openai entry", func() {
		body, err := remotes.BuildEntry(remotes.KindOpenAI, "https://api.openai.com/v1", "gpt-4o", "c2", remotes.UpstreamModel{ID: "gpt-4o"})
		Expect(err).NotTo(HaveOccurred())
		noCredential(body)
		cfg := load("gpt-4o", body)
		Expect(cfg.Backend).To(Equal("cloud-proxy"))
		Expect(cfg.Proxy.Mode).To(Equal("passthrough"))
		Expect(cfg.Proxy.Provider).To(Equal("openai"))
		Expect(cfg.Proxy.UpstreamURL).To(Equal("https://api.openai.com/v1/chat/completions"))
		Expect(cfg.Proxy.UpstreamModel).To(Equal("gpt-4o"))
		Expect(cfg.Proxy.Remote).To(Equal("c2"))
		Expect(cfg.HasUsecases(config.FLAG_CHAT)).To(BeTrue())
	})
	It("builds an anthropic entry", func() {
		body, err := remotes.BuildEntry(remotes.KindAnthropic, "", "claude", "c3", remotes.UpstreamModel{ID: "claude-sonnet-4"})
		Expect(err).NotTo(HaveOccurred())
		noCredential(body)
		cfg := load("claude", body)
		Expect(cfg.Backend).To(Equal("cloud-proxy"))
		Expect(cfg.Proxy.Provider).To(Equal("anthropic"))
		Expect(cfg.Proxy.Mode).To(Equal("passthrough"))
		Expect(cfg.Proxy.UpstreamURL).To(Equal("https://api.anthropic.com/v1/messages"))
		Expect(cfg.Proxy.UpstreamModel).To(Equal("claude-sonnet-4"))
	})
	It("uses the gateway address of an anthropic remote", func() {
		body, err := remotes.BuildEntry(remotes.KindAnthropic, "https://gw.example.com", "c", "c3", remotes.UpstreamModel{ID: "m"})
		Expect(err).NotTo(HaveOccurred())
		Expect(load("c", body).Proxy.UpstreamURL).To(Equal("https://gw.example.com/v1/messages"))
	})
	It("writes the same bytes for the same input", func() {
		m := remotes.UpstreamModel{ID: "a", Usecases: []string{"chat"}}
		a, _ := remotes.BuildEntry(remotes.KindLocalAI, "https://r.example.com", "a", "c", m)
		b, _ := remotes.BuildEntry(remotes.KindLocalAI, "https://r.example.com", "a", "c", m)
		Expect(a).To(Equal(b))
		Expect(remotes.HashBytes(a)).To(Equal(remotes.HashBytes(b)))
	})
})
