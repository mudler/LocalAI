package llamacpp_test

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	es "github.com/mudler/LocalAI/pkg/enginesettings"
	lc "github.com/mudler/LocalAI/pkg/enginesettings/llamacpp"
)

const inklingCommand = `llama-server -m Inkling-2-Mini-UD-Q3_K_XL.gguf --jinja --ctx-size 18000 --parallel 1 ` +
	`--cache-type-k q8_0 --cache-type-v q8_0 --temp 1.0 --top-p 1.0 --top-k 0 --min-p 0.0 -ub 64 -ngl 99 ` +
	`-fit off -fa on -t 6 -tb 12 --no-op-offload --load-mode none ` +
	`-ot "blk\.([0-6]|1[89]|2[0-3])\.ffn_.*_exps.*=CPU"`

const inklingYAML = `
parameters:
  model: Inkling-2-Mini-UD-Q3_K_XL.gguf
  temperature: 1.0
  top_p: 1.0
  top_k: 0
  min_p: 0.0
context_size: 18000
threads: 6
gpu_layers: 99
flash_attention: "on"
cache_type_k: q8_0
cache_type_v: q8_0
mmap: false
options:
  - use_jinja:true
  - parallel:1
  - ubatch:64
  - threads_batch:12
  - fit:off
  - no_op_offload
  - '-ot:blk\.([0-6]|1[89]|2[0-3])\.ffn_.*_exps.*=CPU'
`

var engine = lc.Engine()

func inklingConfig() es.Config {
	var c es.Config
	Expect(yaml.Unmarshal([]byte(inklingYAML), &c)).To(Succeed())
	return c
}

// canon gives a value the form a JSON client sees, so a parsed config and
// a YAML one compare by meaning (1 and 1.0 are the same number).
func canon(v any) any {
	b, err := json.Marshal(v)
	Expect(err).ToNot(HaveOccurred())
	var out any
	Expect(json.Unmarshal(b, &out)).To(Succeed())
	return out
}

// withOptions is the parsed fields with the options list, as in a model file.
func withOptions(r es.ParseResult) es.Config {
	c := es.Config{}
	for k, v := range r.Fields {
		c[k] = v
	}
	c["options"] = r.Options
	return c
}

func parse(cmd string) es.ParseResult {
	r, err := engine.ParseCommand(cmd)
	Expect(err).ToNot(HaveOccurred())
	return r
}

func build(c es.Config) es.BuildResult {
	b, err := engine.BuildCommand(c)
	Expect(err).ToNot(HaveOccurred())
	return b
}

func equalOrNil(v any) OmegaMatcher {
	if v == nil {
		return BeNil()
	}
	return Equal(v)
}

// field reads a parsed value by its YAML path.
func field(r es.ParseResult, path string) any {
	var node any = map[string]any(r.Fields)
	for _, k := range strings.Split(path, ".") {
		m, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node = m[k]
	}
	return node
}

var _ = Describe("ParseCommand", func() {
	It("turns the Inkling command into the expected model YAML", func() {
		r := parse(inklingCommand)
		Expect(r.Unmapped).To(BeEmpty())
		Expect(r.Warnings).To(BeEmpty())
		Expect(canon(withOptions(r))).To(Equal(canon(inklingConfig())))
	})

	It("accepts the =, short and on/off forms", func() {
		r := parse(`llama-server --ctx-size=4096 -ngl 20 --flash-attn off -fit --no-jinja --mlock -b 512 -np 2 --temperature=0.7`)
		Expect(r.Unmapped).To(BeEmpty())
		Expect(field(r, "context_size")).To(Equal(4096))
		Expect(field(r, "gpu_layers")).To(Equal(20))
		Expect(field(r, "flash_attention")).To(Equal("off"))
		Expect(field(r, "parameters.batch")).To(Equal(512))
		Expect(field(r, "parameters.temperature")).To(Equal(0.7))
		Expect(r.Options).To(Equal([]string{"use_jinja:false", "parallel:2", "fit:on"}))
		Expect(field(r, "mmlock")).To(BeTrue())
	})

	It("reads a bare -fa as on and does not eat the next flag", func() {
		r := parse(`llama-server -fa -t 4`)
		Expect(field(r, "flash_attention")).To(Equal("on"))
		Expect(field(r, "threads")).To(Equal(4))
	})

	It("lets the last of repeated flags win", func() {
		r := parse(`-c 1000 -c 2000`)
		Expect(field(r, "context_size")).To(Equal(2000))
	})

	DescribeTable("maps --load-mode",
		func(mode string, mmap, mlock any, options []string) {
			r := parse("llama-server --load-mode " + mode)
			Expect(field(r, "mmap")).To(equalOrNil(mmap))
			Expect(field(r, "mmlock")).To(equalOrNil(mlock))
			Expect(r.Options).To(Equal(options))
		},
		Entry("none", "none", false, nil, []string{}),
		Entry("mmap", "mmap", true, nil, []string{}),
		Entry("mlock", "mlock", false, true, []string{}),
		Entry("mmap_mlock", "mmap_mlock", true, true, []string{}),
		Entry("direct_io", "direct_io", nil, nil, []string{"direct_io:true"}),
	)

	It("warns about load-mode auto and rejects unknown modes", func() {
		r := parse(`--load-mode auto`)
		Expect(field(r, "mmap")).To(BeNil())
		Expect(r.Warnings).ToNot(BeEmpty())
		r = parse(`--load-mode sideways`)
		Expect(r.Unmapped).To(HaveLen(1))
	})

	It("keeps unknown flags as raw options and reports them", func() {
		r := parse(`llama-server --rope-freq-base 10000 --no-perf --grp-attn-n=4`)
		Expect(r.Options).To(Equal([]string{"--rope-freq-base:10000", "--no-perf", "--grp-attn-n:4"}))
		Expect(r.Unmapped).To(HaveLen(3))
		Expect(r.Unmapped[0].Flag).To(Equal("--rope-freq-base"))
		Expect(r.Unmapped[0].Value).To(Equal("10000"))
		Expect(r.Unmapped[0].Reason).ToNot(BeEmpty())
	})

	It("reports flags LocalAI manages and does not forward them", func() {
		r := parse(`llama-server --host 0.0.0.0 --port 8080 -hf org/model --version`)
		Expect(r.Options).To(BeEmpty())
		Expect(r.Unmapped).To(HaveLen(4))
		for _, u := range r.Unmapped {
			Expect(u.Reason).To(HavePrefix("ignored"))
		}
	})

	It("reports invalid values without forwarding them", func() {
		r := parse(`-c abc -ctk nope --top-p 2 -ngl`)
		Expect(r.Unmapped).To(HaveLen(4))
		Expect(r.Options).To(BeEmpty())
		Expect(field(r, "context_size")).To(BeNil())
		Expect(field(r, "cache_type_k")).To(BeNil())
	})

	It("does not run shell syntax", func() {
		r := parse("llama-server -c 10; touch /nonexistent/x $(id) `id`")
		Expect(r.Unmapped).ToNot(BeEmpty())
		Expect(r.Warnings).ToNot(BeEmpty())
	})

	It("warns about stray arguments and model paths", func() {
		r := parse(`llama-server stray -m /models/a.gguf`)
		Expect(r.Warnings).To(HaveLen(2))
		Expect(field(r, "parameters.model")).To(Equal("/models/a.gguf"))
	})

	It("passes several -ot patterns through and describes them", func() {
		r := parse(`-ot "blk\.3\.ffn_.*=CUDA0" -ot "\.ffn_.*_exps.*=CPU" --override-tensor x=CPU`)
		Expect(r.Options).To(HaveLen(3))
		Expect(r.Unmapped).To(BeEmpty())
		Expect(r.Placements).To(HaveLen(3))
		Expect(r.Placements[0].Placement).To(BeNil())
		Expect(r.Placements[1].Placement.Mode).To(Equal(es.ExpertsCPUAll))
	})

	It("rejects an oversized command", func() {
		_, err := engine.ParseCommand(strings.Repeat("a ", es.MaxCommandLength))
		Expect(err).To(HaveOccurred())
	})

	It("rejects NUL bytes", func() {
		_, err := engine.ParseCommand("llama-server\x00 -c 1")
		Expect(err).To(HaveOccurred())
	})

	It("serialises empty lists as arrays", func() {
		b, err := json.Marshal(parse(`llama-server`))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(b)).To(ContainSubstring(`"options":[]`))
		Expect(string(b)).To(ContainSubstring(`"unmapped":[]`))
		Expect(string(b)).To(ContainSubstring(`"warnings":[]`))
	})
})

var _ = Describe("BuildCommand", func() {
	It("builds a command for the Inkling YAML that parses back to the same YAML", func() {
		b := build(inklingConfig())
		Expect(b.Warnings).To(BeEmpty())
		Expect(b.Command).To(Equal(`llama-server -m Inkling-2-Mini-UD-Q3_K_XL.gguf --ctx-size 18000 -ngl 99 -t 6 ` +
			`-fa on -ctk q8_0 -ctv q8_0 --load-mode none --jinja -np 1 -ub 64 -tb 12 -fit off --no-op-offload ` +
			`--temp 1.0 --top-p 1.0 --top-k 0 --min-p 0.0 -ot "blk\.([0-6]|1[89]|2[0-3])\.ffn_.*_exps.*=CPU"`))
		r := parse(b.Command)
		Expect(r.Unmapped).To(BeEmpty())
		Expect(canon(withOptions(r))).To(Equal(canon(inklingConfig())))
	})

	It("round-trips the original command through parse and build and parse", func() {
		first := parse(inklingCommand)
		second := parse(build(canon(withOptions(first)).(map[string]any)).Command)
		Expect(second.Fields).To(Equal(first.Fields))
		Expect(second.Options).To(Equal(first.Options))
	})

	It("builds nothing but the binary for an empty config", func() {
		Expect(build(es.Config{}).Command).To(Equal("llama-server"))
	})

	It("writes load modes", func() {
		Expect(build(es.Config{"mmap": true}).Command).To(Equal("llama-server --load-mode mmap"))
		Expect(build(es.Config{"mmlock": true}).Command).To(Equal("llama-server --load-mode mlock"))
		Expect(build(es.Config{"mmap": true, "mmlock": true}).Command).To(Equal("llama-server --load-mode mmap_mlock"))
		Expect(build(es.Config{"options": []string{"direct_io:true"}}).Command).To(Equal("llama-server --load-mode direct_io"))
	})

	It("understands option aliases and bare booleans", func() {
		b := build(es.Config{"options": []string{"jinja", "n_parallel:2", "no_op_offload:false", "fit_params:off"}})
		Expect(b.Command).To(Equal("llama-server --jinja -np 2 -fit off --op-offload"))
	})

	It("warns about options that have no flag and bad values", func() {
		b := build(es.Config{"options": []string{"not_a_llama_option:true", "parallel:x"}})
		Expect(b.Warnings).To(HaveLen(2))
		Expect(b.Command).To(Equal("llama-server"))
	})

	It("quotes values that need it and survives a parse", func() {
		in := es.Config{"parameters": map[string]any{"model": `my model $x "q".gguf`}, "options": []string{`--chat-template:a b\`}}
		r := parse(build(in).Command)
		Expect(field(r, "parameters.model")).To(Equal(`my model $x "q".gguf`))
		Expect(r.Options).To(Equal([]string{`--chat-template:a b\`}))
	})

	It("does not modify its input", func() {
		in := inklingConfig()
		before, _ := json.Marshal(in)
		build(in)
		after, _ := json.Marshal(in)
		Expect(after).To(Equal(before))
		Expect(build(es.Config{}).Command).To(Equal("llama-server"))
	})
})

var _ = Describe("Flags table", func() {
	It("has unique keys, CLI spellings and docs", func() {
		keys, cli := map[string]bool{}, map[string]bool{}
		for _, f := range engine.Flags {
			Expect(keys[f.Key]).To(BeFalse(), f.Key)
			keys[f.Key] = true
			Expect(f.Doc).ToNot(BeEmpty(), f.Key)
			Expect(f.Group).ToNot(BeEmpty(), f.Key)
			if f.LocalAIOnly {
				Expect(f.CLI).To(BeEmpty(), f.Key)
				continue
			}
			Expect(f.CLI).ToNot(BeEmpty(), f.Key)
			for _, c := range append(append([]string{}, f.CLI...), f.CLINegated...) {
				Expect(cli[c]).To(BeFalse(), c)
				cli[c] = true
			}
		}
	})
})

var _ = Describe("Tensor placement", func() {
	match := func(pattern string, n int) bool {
		expr, _, _ := strings.Cut(pattern, "=CPU")
		return regexp.MustCompile(expr).MatchString("blk." + strconv.Itoa(n) + ".ffn_down_exps.weight")
	}

	It("builds the regex of the Inkling example from ranges", func() {
		rs, err := es.ParseLayerRanges("0-6,18-23")
		Expect(err).ToNot(HaveOccurred())
		got, err := lc.Pattern(es.Placement{Mode: es.ExpertsCPULayers, Layers: rs})
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(`blk\.([0-6]|1[89]|2[0-3])\.ffn_.*_exps.*=CPU`))
	})

	It("keeps all experts on the CPU", func() {
		o, err := lc.Option(es.Placement{Mode: es.ExpertsCPUAll})
		Expect(err).ToNot(HaveOccurred())
		Expect(o).To(Equal(`-ot:\.ffn_.*_exps.*=CPU`))
		Expect(match(o[4:], 7)).To(BeTrue())
		Expect(regexp.MustCompile(`\.ffn_.*_exps.*`).MatchString("blk.1.ffn_gate_shexp.weight")).To(BeFalse())
	})

	DescribeTable("writes ranges",
		func(spec, group string) {
			rs, err := es.ParseLayerRanges(spec)
			Expect(err).ToNot(HaveOccurred())
			got, err := lc.Pattern(es.Placement{Mode: es.ExpertsCPULayers, Layers: rs})
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(`blk\.(` + group + `)\.ffn_.*_exps.*=CPU`))
		},
		Entry("single layer", "5", `5`),
		Entry("single two-digit layer", "12", `12`),
		Entry("whole decade", "10-19", `1[0-9]`),
		Entry("digits and decade", "0-19", `[0-9]|1[0-9]`),
		Entry("across digit lengths", "7-12", `[7-9]|1[0-2]`),
		Entry("merges touching and overlapping ranges", "0-3,4-6,5-9", `[0-9]`),
		Entry("sorts the input", "20-21,1", `1|2[01]`),
		Entry("middle block", "13-48", `1[3-9]|[23][0-9]|4[0-8]`),
		Entry("three digits", "98-101", `9[89]|10[01]`),
	)

	It("matches exactly the requested layers", func() {
		for _, spec := range []string{"0", "9", "10", "0-6,18-23", "3,5,7-11,99-103", "0-999", "250-260,5", "1-1"} {
			rs, err := es.ParseLayerRanges(spec)
			Expect(err).ToNot(HaveOccurred())
			want := map[int]bool{}
			for _, r := range rs {
				for n := r.From; n <= r.To; n++ {
					want[n] = true
				}
			}
			pat, err := lc.Pattern(es.Placement{Mode: es.ExpertsCPULayers, Layers: rs})
			Expect(err).ToNot(HaveOccurred())
			for n := 0; n <= 1200; n++ {
				Expect(match(pat, n)).To(Equal(want[n]), "spec %s layer %d", spec, n)
			}
		}
	})

	It("keeps the first N layers on the GPU", func() {
		for _, n := range []int{1, 7, 9, 10, 18, 19, 20, 99, 100, 345} {
			pat, err := lc.Pattern(es.Placement{Mode: es.FirstLayersGPU, FirstGPULayers: n})
			Expect(err).ToNot(HaveOccurred())
			Expect(pat).ToNot(ContainSubstring(","))
			for l := 0; l <= 1500; l++ {
				Expect(match(pat, l)).To(Equal(l >= n), "n %d layer %d (%s)", n, l, pat)
			}
		}
	})

	It("uses a bounded range when the layer count is known", func() {
		got, err := lc.Pattern(es.Placement{Mode: es.FirstLayersGPU, FirstGPULayers: 8, TotalLayers: 24})
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(`blk\.([89]|1[0-9]|2[0-3])\.ffn_.*_exps.*=CPU`))
		_, err = lc.Pattern(es.Placement{Mode: es.FirstLayersGPU, FirstGPULayers: 24, TotalLayers: 24})
		Expect(err).To(HaveOccurred())
	})

	It("treats zero GPU layers as all experts on the CPU", func() {
		got, _ := lc.Pattern(es.Placement{Mode: es.FirstLayersGPU})
		Expect(got).To(Equal(`\.ffn_.*_exps.*=CPU`))
	})

	It("rejects bad input", func() {
		for _, spec := range []string{"a", "5-3", "-1", "1000", "1-x"} {
			_, err := es.ParseLayerRanges(spec)
			Expect(err).To(HaveOccurred(), spec)
		}
		_, err := lc.Pattern(es.Placement{Mode: es.ExpertsCPULayers})
		Expect(err).To(HaveOccurred())
		_, err = lc.Pattern(es.Placement{Mode: "nope"})
		Expect(err).To(HaveOccurred())
		_, err = lc.Pattern(es.Placement{Mode: es.FirstLayersGPU, FirstGPULayers: 5000})
		Expect(err).To(HaveOccurred())
	})

	It("formats ranges", func() {
		rs, _ := es.ParseLayerRanges("18-23, 5,0-6")
		Expect(es.FormatLayerRanges(rs)).To(Equal("0-6,18-23"))
	})

	It("recognises what it generates", func() {
		cases := []es.Placement{
			{Mode: es.ExpertsCPUAll},
			{Mode: es.ExpertsCPULayers, Layers: []es.LayerRange{{From: 0, To: 6}, {From: 18, To: 23}}},
			{Mode: es.ExpertsCPULayers, Layers: []es.LayerRange{{From: 5, To: 5}}},
			{Mode: es.ExpertsCPULayers, Layers: []es.LayerRange{{From: 98, To: 101}, {From: 200, To: 200}}},
			{Mode: es.FirstLayersGPU, FirstGPULayers: 12},
			{Mode: es.FirstLayersGPU, FirstGPULayers: 105},
		}
		for _, c := range cases {
			o, err := lc.Option(c)
			Expect(err).ToNot(HaveOccurred())
			got, ok := lc.ParseOption(o)
			Expect(ok).To(BeTrue(), o)
			Expect(got).To(Equal(c))
		}
	})

	It("returns raw for patterns it did not generate", func() {
		for _, o := range []string{
			`-ot:blk\.3\.ffn_.*=CUDA0`,
			`-ot:blk\.([0-6]|1[89]|2[0-3]|9)\.ffn_.*_exps.*=CPU`, // valid but not the canonical text
			`-ot:blk\.(.*)\.ffn_.*_exps.*=CPU`,
			`-ot:blk\.()\.ffn_.*_exps.*=CPU`,
			`-ot:blk\.([\d)\.ffn_.*_exps.*=CPU`,
			`-ot:`,
			`-c:4`,
			`nonsense`,
		} {
			_, ok := lc.ParseOption(o)
			Expect(ok).To(BeFalse(), o)
		}
	})

	It("does not compile huge groups", func() {
		_, ok := lc.ParsePattern(`blk\.(` + strings.Repeat("1|", 5000) + `1)\.ffn_.*_exps.*=CPU`)
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("the llama.cpp engine", func() {
	It("is valid and registers", func() {
		Expect(engine.Validate()).To(Succeed())
		r := es.NewRegistry()
		Expect(r.Register(lc.Engine())).To(Succeed())
		got, ok := r.Get(lc.ID)
		Expect(ok).To(BeTrue())
		Expect(got.Name).To(Equal("llama.cpp"))
	})

	DescribeTable("serves the llama.cpp backend family and nothing else",
		func(backend string, want bool) {
			Expect(engine.MatchesBackend(backend)).To(Equal(want))
		},
		Entry("the meta name", "llama-cpp", true),
		Entry("the dotted name", "llama.cpp", true),
		Entry("no backend (GGUF auto-detect)", "", true),
		Entry("a hardware build", "cuda12-llama-cpp", true),
		Entry("a pinned Metal build", "metal-darwin-arm64-llama-cpp", true),
		Entry("the development channel", "vulkan-llama-cpp-development", true),
		Entry("an old name", "llama", true),
		Entry("another old name", "go-llama", true),
		Entry("upper case", "LLAMA-CPP", true),
		Entry("ik-llama.cpp, another engine", "ik-llama-cpp", false),
		Entry("a hardware build of ik-llama.cpp", "cuda12-ik-llama-cpp", false),
		Entry("vLLM", "vllm", false),
		Entry("a name that only contains llama", "llama-cpp-python-x", false),
	)

	It("lists its capabilities, combined load mode and presets in the schema", func() {
		s := engine.Schema()
		Expect(s.Capabilities).To(ConsistOf(es.CapabilityCommandLine, es.CapabilityTensorPlacement, es.CapabilityDrafting))
		Expect(s.Binary).To(Equal("llama-server"))
		Expect(s.Example).To(HavePrefix("llama-server "))
		Expect(s.Placement.Option).To(Equal("-ot"))
		Expect(s.Placement.Aliases).To(ContainElement("--override-tensor"))
		Expect(s.Placement.Group).To(Equal(lc.GroupPlacement))
		Expect(s.Combined).To(HaveLen(2))
		Expect(s.Combined[0].Keys).To(Equal([]string{"mmap", "mmlock", "direct_io"}))
		Expect(s.Presets).To(HaveLen(9))
		byID := map[string]es.Preset{}
		for _, p := range s.Presets {
			byID[p.ID] = p
		}
		Expect(byID["bigMoe"].Placement.Mode).To(Equal(es.ExpertsCPUAll))
		Expect(byID["bigMoe"].Settings["gpu_layers"]).To(Equal(999))
		Expect(byID["bigMoe"].Profile).To(BeFalse())
		Expect(s.ReservedOptions).To(ContainElement("n_cpu_moe"))
		Expect(s.Estimate).To(HaveKeyWithValue("ubatch", "ubatch"))
		Expect(s.Estimate).To(HaveKeyWithValue("cache_type_v", "cache_type_v"))
	})

	Describe("the memory decision table", func() {
		profilesOf := func(set string) []es.Preset {
			var got []es.Preset
			for _, p := range engine.Schema().Presets {
				if p.Profile && p.Set == set {
					got = append(got, p)
				}
			}
			return got
		}
		byID := func(id string) es.Preset {
			for _, p := range engine.Schema().Presets {
				if p.ID == id {
					return p
				}
			}
			Fail("no profile " + id)
			return es.Preset{}
		}

		It("describes every memory kind, with discrete as the fallback", func() {
			m := engine.Schema().Memory
			Expect(m).ToNot(BeNil())
			Expect(m.Fallback).To(Equal(es.MemoryDiscrete))
			var kinds []es.MemoryKind
			for _, k := range m.Kinds {
				kinds = append(kinds, k.Kind)
			}
			Expect(kinds).To(ConsistOf(es.MemoryKinds))
		})

		It("has Balanced and Long prompts over the batch sizes only, Balanced unsetting what Long prompts sets", func() {
			got := profilesOf("")
			Expect(got).To(HaveLen(2))
			Expect(got[0].ID).To(Equal("balanced"))
			for _, v := range got[0].Settings {
				Expect(v).To(BeNil())
			}
			for _, kind := range es.MemoryKinds {
				for k := range got[1].SettingsFor(kind, false) {
					Expect(got[0].Settings).To(HaveKey(k), string(kind))
				}
			}
		})

		DescribeTable("Long prompts sizes the micro-batch for the memory kind",
			func(kind es.MemoryKind, ubatch int) {
				s := byID("longPrompts").SettingsFor(kind, false)
				Expect(s).To(HaveKeyWithValue("batch", 2048))
				Expect(s).To(HaveKeyWithValue("ubatch", ubatch))
			},
			Entry("unified memory: 2048, as measured on a GB10", es.MemoryUnified, 2048),
			Entry("a discrete GPU: 1024, the compute buffer must fit one card", es.MemoryDiscrete, 1024),
			Entry("no GPU: 512", es.MemorySystem, 512),
		)

		It("has Default, 8, 16 and 32+ streams in the concurrency set", func() {
			got := profilesOf(es.ProfileSetConcurrency)
			var ids []string
			for _, p := range got {
				ids = append(ids, p.ID)
				Expect(p.BestFor).ToNot(BeEmpty(), p.ID)
			}
			Expect(ids).To(Equal([]string{"concurrencyDefault", "streams8", "streams16", "streams32"}))
			Expect(got[1].Streams).To(Equal(8))
			Expect(got[2].Streams).To(Equal(16))
			Expect(got[3].Streams).To(Equal(32))
			// Default unsets the keys every tier shares, so a switch back
			// removes what a tier wrote.
			for _, k := range []string{"parallel", "paged_kv", "paged_preempt"} {
				Expect(got[0].Settings).To(HaveKeyWithValue(k, BeNil()))
			}
		})

		It("writes the brief's values on every kind", func() {
			for _, kind := range []es.MemoryKind{es.MemoryUnified, es.MemoryDiscrete} {
				s8 := byID("streams8").SettingsFor(kind, false)
				Expect(s8).To(HaveKeyWithValue("parallel", 8))
				Expect(s8).To(HaveKeyWithValue("paged_kv", true))
				Expect(s8).To(HaveKeyWithValue("paged_preempt", true))

				s16 := byID("streams16").SettingsFor(kind, false)
				Expect(s16).To(HaveKeyWithValue("parallel", 16))
				Expect(s16).To(HaveKeyWithValue("backend_sampling", "on"))
				Expect(s16).To(HaveKeyWithValue("spec_type", "none"))

				s32 := byID("streams32").SettingsFor(kind, false)
				Expect(s32).To(HaveKeyWithValue("parallel", 32))
				Expect(s32).To(HaveKeyWithValue("backend_sampling", "on"))
			}
		})

		It("uses an 8-bit KV cache at 32 streams on a discrete GPU, and on unified memory only when f16 does not fit", func() {
			p := byID("streams32")
			d := p.SettingsFor(es.MemoryDiscrete, false)
			Expect(d).To(HaveKeyWithValue("cache_type_k", "q8_0"))
			Expect(d).To(HaveKeyWithValue("cache_type_v", "q8_0"))
			Expect(d).To(HaveKeyWithValue("flash_attention", "on"))

			Expect(p.SettingsFor(es.MemoryUnified, false)).ToNot(HaveKey("cache_type_k"))
			tight := p.SettingsFor(es.MemoryUnified, true)
			Expect(tight).To(HaveKeyWithValue("cache_type_k", "q8_0"))
			Expect(tight).To(HaveKeyWithValue("flash_attention", "on"))
		})

		It("bounds the host caches on unified memory and the CPU, and leaves them on a discrete GPU", func() {
			for _, id := range []string{"streams8", "streams16", "streams32"} {
				Expect(byID(id).SettingsFor(es.MemoryUnified, false)).To(HaveKey("cache_ram"), id)
				Expect(byID(id).SettingsFor(es.MemoryUnified, false)).To(HaveKey("cache_ram"), id)
				// The checkpoints are bounded per model by the hardware defaults, not fixed here.
				Expect(byID(id).SettingsFor(es.MemoryUnified, false)).NotTo(HaveKey("ctx_checkpoints"), id)
				Expect(byID(id).SettingsFor(es.MemoryDiscrete, false)).ToNot(HaveKey("cache_ram"), id)
			}
		})

		It("allows more slots on unified memory than its floors on a discrete GPU, and no 32 streams without a GPU", func() {
			for _, id := range []string{"streams8", "streams16", "streams32"} {
				Expect(byID(id).Memory[es.MemoryDiscrete].MinMemory).To(BeNumerically(">", 0), id)
				Expect(byID(id).Memory[es.MemoryUnified].MinMemory).To(BeNumerically(">", 0), id)
			}
			Expect(byID("streams32").Memory[es.MemorySystem].Unsupported).To(BeTrue())
			Expect(byID("streams16").SettingsFor(es.MemorySystem, false)).To(HaveKeyWithValue("backend_sampling", BeNil()))
		})

		It("advises the default load mode on unified memory", func() {
			m := engine.Schema().Memory
			Expect(m.Advice).To(ContainElement(SatisfyAll(
				HaveField("Control", "load_mode"),
				HaveField("Kind", es.MemoryUnified),
				HaveField("Value", "default"),
				HaveField("Avoid", ContainElement("mmap")),
			)))
		})
	})

	It("has a GPU sampling choice of Off, Auto and On, Off writing no entry", func() {
		var f es.Flag
		for _, x := range engine.Schema().Flags {
			if x.Key == "backend_sampling" {
				f = x
			}
		}
		Expect(f.Option).To(Equal("backend_sampling"))
		Expect(f.Target).To(Equal(es.TargetOption))
		Expect(f.Enum).To(Equal([]string{"off", "auto", "on"}))
		Expect(f.Default).To(Equal("off"))
		Expect(f.LocalAIOnly).To(BeTrue())
		var c es.Combined
		for _, x := range engine.Schema().Combined {
			if x.ID == "backend_sampling" {
				c = x
			}
		}
		Expect(c.Group).To(Equal(lc.GroupSpeed))
		Expect(c.Keys).To(Equal([]string{"backend_sampling"}))
		Expect(c.Choices).To(HaveLen(3))
		Expect(c.Choices[0].Set["backend_sampling"]).To(BeNil())
		Expect(c.Choices[1].Set["backend_sampling"]).To(Equal("auto"))
		Expect(c.Choices[2].Set["backend_sampling"]).To(Equal("on"))
		Expect(c.Choices[1].Doc).To(Equal("Use GPU sampling when the model runs on a GPU with several parallel requests and the request does not need grammar or logprobs."))
		Expect(c.Choices[2].Doc).To(Equal("Sample tokens on the GPU. Faster with many parallel requests. Turned off automatically for requests that use a grammar, a reasoning budget or logprobs."))
		res, err := engine.BuildCommand(es.Config{"options": []any{"backend_sampling:auto"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Command).To(Equal("llama-server"))
		Expect(res.Warnings).To(ConsistOf(ContainSubstring("read by LocalAI")))
	})

	It("keeps the paged serving options in the file and out of the command", func() {
		res, err := engine.BuildCommand(es.Config{"options": []any{"paged_serving:true", "paged_preempt:false", "max_prefill_tokens:512", "parallel:8"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Command).To(Equal("llama-server -np 8"))
		Expect(res.Warnings).To(HaveLen(3))
		for _, w := range res.Warnings {
			Expect(w).To(ContainSubstring("read by LocalAI"))
		}
		s := engine.Schema()
		names := map[string]es.Flag{}
		for _, f := range s.Flags {
			names[f.Key] = f
		}
		Expect(names["paged_kv"].Option).To(Equal("paged_kv"))
		Expect(names["paged_kv"].OptionAliases).To(ContainElement("paged_serving"))
		Expect(names["prefill_budget"].OptionAliases).To(ContainElement("max_prefill_tokens"))
		Expect(names["paged_preempt"].Group).To(Equal(lc.GroupServing))
		Expect(names["parallel"].Group).To(Equal(lc.GroupServing))
	})

	// The combined control writes what --load-mode parses to, so the editor
	// and a pasted command agree.
	It("has load mode choices that write what --load-mode parses to", func() {
		for _, ch := range engine.Schema().Combined[0].Choices {
			if ch.Value == "default" {
				continue
			}
			r := parse("llama-server --load-mode " + ch.Value)
			opts := map[string]bool{}
			for _, o := range r.Options {
				opts[o] = true
			}
			for k, v := range ch.Set {
				switch k {
				case "direct_io":
					Expect(opts["direct_io:true"]).To(Equal(v == true), ch.Value)
				default:
					Expect(field(r, k)).To(equalOrNil(v), ch.Value+" "+k)
				}
			}
		}
	})

	It("refuses a config value of the wrong type", func() {
		for _, c := range []es.Config{
			{"context_size": "big"},
			{"context_size": 1.5},
			{"mmap": "yes"},
			{"parameters": "x"},
			{"parameters": map[string]any{"model": 3}},
			{"options": "x"},
			{"options": []any{1}},
		} {
			_, err := engine.BuildCommand(c)
			Expect(err).To(HaveOccurred(), "%v", c)
		}
	})

	Describe("the drafting section", func() {
		It("offers Off, N-gram, Draft model and MTP over spec_type and draft_model", func() {
			d := engine.Schema().Drafting
			Expect(d).ToNot(BeNil())
			Expect(d.Group).To(Equal(lc.GroupSpeculative))
			Expect(d.TypeKey).To(Equal("spec_type"))
			Expect(d.ModelKey).To(Equal("draft_model"))
			Expect(d.OffType).To(Equal("none"))
			var ids []string
			for _, m := range d.Modes {
				ids = append(ids, m.ID)
			}
			Expect(ids).To(Equal([]string{"off", "ngram", "model", "mtp"}))
			Expect(d.Modes[1].Types[0]).To(Equal("ngram-mod"))
			Expect(d.Modes[2].Model).To(BeTrue())
			Expect(d.Modes[3].Types).To(Equal([]string{"draft-mtp"}))
			Expect(d.Modes[3].Needs).To(Equal(es.NeedsMTPHead))
			Expect(d.Modes[3].Auto).To(BeTrue())
		})

		It("writes spec_max_streams as a plain option that builds without paged serving ignore", func() {
			d := engine.Schema().Drafting
			Expect(d.Streams.Option).To(Equal("spec_max_streams"))
			res, err := engine.BuildCommand(es.Config{"options": []any{"spec_type:ngram-mod", "spec_max_streams:2"}})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Command).To(Equal("llama-server --spec-type ngram-mod"))
			Expect(res.Warnings).To(ConsistOf(ContainSubstring(`"spec_max_streams" is read by LocalAI`)))
		})

		It("maps a draft model file to -md and back", func() {
			res, err := engine.BuildCommand(es.Config{"draft_model": "small.gguf"})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Command).To(Equal("llama-server -md small.gguf"))
			for _, flag := range []string{"-md", "--model-draft", "--spec-draft-model"} {
				r, err := engine.ParseCommand("llama-server " + flag + " small.gguf")
				Expect(err).ToNot(HaveOccurred())
				Expect(r.Fields).To(Equal(es.Config{"draft_model": "small.gguf"}), flag)
			}
		})

		It("states the draft defaults of the pinned llama.cpp", func() {
			defaults := map[string]string{}
			for _, f := range engine.Schema().Flags {
				defaults[f.Key] = f.Default
			}
			Expect(defaults["spec_n_max"]).To(Equal("3"))
			Expect(defaults["spec_n_min"]).To(Equal("0"))
			Expect(defaults["spec_p_min"]).To(Equal("0"))
		})
	})
})
