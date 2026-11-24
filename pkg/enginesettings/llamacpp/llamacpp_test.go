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
		r := parse(`llama-server --rope-freq-base 10000 --cont-batching --grp-attn-n=4`)
		Expect(r.Options).To(Equal([]string{"--rope-freq-base:10000", "--cont-batching", "--grp-attn-n:4"}))
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
		b := build(es.Config{"options": []string{"context_shift:true", "parallel:x"}})
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
		Expect(s.Capabilities).To(ConsistOf(es.CapabilityCommandLine, es.CapabilityTensorPlacement))
		Expect(s.Binary).To(Equal("llama-server"))
		Expect(s.Example).To(HavePrefix("llama-server "))
		Expect(s.Placement.Option).To(Equal("-ot"))
		Expect(s.Placement.Aliases).To(ContainElement("--override-tensor"))
		Expect(s.Placement.Group).To(Equal(lc.GroupPlacement))
		Expect(s.Combined).To(HaveLen(1))
		Expect(s.Combined[0].Keys).To(Equal([]string{"mmap", "mmlock", "direct_io"}))
		Expect(s.Presets).To(HaveLen(3))
		Expect(s.Presets[0].Placement.Mode).To(Equal(es.ExpertsCPUAll))
		Expect(s.Presets[0].Settings["gpu_layers"]).To(Equal(999))
		Expect(s.ReservedOptions).To(ContainElement("n_cpu_moe"))
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
})
