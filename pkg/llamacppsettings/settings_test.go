package llamacppsettings_test

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	ls "github.com/mudler/LocalAI/pkg/llamacppsettings"
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

func inklingSettings() ls.Settings {
	var s ls.Settings
	Expect(yaml.Unmarshal([]byte(inklingYAML), &s)).To(Succeed())
	return s
}

func parse(cmd string) ls.ParseResult {
	r, err := ls.ParseCommand(cmd)
	Expect(err).ToNot(HaveOccurred())
	return r
}

var _ = Describe("Tokenize", func() {
	It("handles quotes and escapes without expanding anything", func() {
		toks, ops, err := ls.Tokenize(`a "b c" 'd $HOME' e\ f "g\"h" "i\.j"`)
		Expect(err).ToNot(HaveOccurred())
		Expect(toks).To(Equal([]string{"a", "b c", "d $HOME", "e f", `g"h`, `i\.j`}))
		Expect(ops).To(BeEmpty())
	})
	It("joins lines that end with a backslash", func() {
		toks, _, err := ls.Tokenize("llama-server \\\n  -c 10 \\\n  -t 2\n")
		Expect(err).ToNot(HaveOccurred())
		Expect(toks).To(Equal([]string{"llama-server", "-c", "10", "-t", "2"}))
	})
	It("reports unterminated quotes", func() {
		_, _, err := ls.Tokenize(`a "b`)
		Expect(err).To(HaveOccurred())
		_, _, err = ls.Tokenize(`a 'b`)
		Expect(err).To(HaveOccurred())
	})
	It("keeps operators as text and flags them", func() {
		toks, ops, err := ls.Tokenize(`-c 1; rm -rf x $(id)`)
		Expect(err).ToNot(HaveOccurred())
		Expect(toks).To(ContainElement("1;"))
		Expect(toks).To(ContainElement("$(id)"))
		Expect(ops).To(HaveLen(2))
	})
})

var _ = Describe("ParseCommand", func() {
	It("turns the Inkling command into the expected model YAML", func() {
		r := parse(inklingCommand)
		Expect(r.Unmapped).To(BeEmpty())
		Expect(r.Warnings).To(BeEmpty())
		got := r.Fields
		got.Options = r.Options
		Expect(got).To(Equal(inklingSettings()))
	})

	It("accepts the =, short and on/off forms", func() {
		r := parse(`llama-server --ctx-size=4096 -ngl 20 --flash-attn off -fit --no-jinja --mlock -b 512 -np 2 --temperature=0.7`)
		Expect(r.Unmapped).To(BeEmpty())
		Expect(*r.Fields.ContextSize).To(Equal(4096))
		Expect(*r.Fields.GPULayers).To(Equal(20))
		Expect(*r.Fields.FlashAttention).To(Equal("off"))
		Expect(*r.Fields.Parameters.Batch).To(Equal(512))
		Expect(*r.Fields.Parameters.Temperature).To(Equal(0.7))
		Expect(r.Options).To(Equal([]string{"use_jinja:false", "parallel:2", "fit:on"}))
		Expect(*r.Fields.MMlock).To(BeTrue())
	})

	It("reads a bare -fa as on and does not eat the next flag", func() {
		r := parse(`llama-server -fa -t 4`)
		Expect(*r.Fields.FlashAttention).To(Equal("on"))
		Expect(*r.Fields.Threads).To(Equal(4))
	})

	It("lets the last of repeated flags win", func() {
		r := parse(`-c 1000 -c 2000`)
		Expect(*r.Fields.ContextSize).To(Equal(2000))
	})

	DescribeTable("maps --load-mode",
		func(mode string, mmap, mlock *bool, options []string) {
			r := parse("llama-server --load-mode " + mode)
			Expect(r.Fields.MMap).To(Equal(mmap))
			Expect(r.Fields.MMlock).To(Equal(mlock))
			Expect(r.Options).To(Equal(options))
		},
		Entry("none", "none", ptr(false), nil, []string{}),
		Entry("mmap", "mmap", ptr(true), nil, []string{}),
		Entry("mlock", "mlock", ptr(false), ptr(true), []string{}),
		Entry("mmap_mlock", "mmap_mlock", ptr(true), ptr(true), []string{}),
		Entry("direct_io", "direct_io", nil, nil, []string{"direct_io:true"}),
	)

	It("warns about load-mode auto and rejects unknown modes", func() {
		r := parse(`--load-mode auto`)
		Expect(r.Fields.MMap).To(BeNil())
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
		Expect(r.Fields.ContextSize).To(BeNil())
		Expect(r.Fields.CacheTypeK).To(BeEmpty())
	})

	It("does not run shell syntax", func() {
		r := parse("llama-server -c 10; touch /nonexistent/x $(id) `id`")
		Expect(r.Unmapped).ToNot(BeEmpty())
		Expect(r.Warnings).ToNot(BeEmpty())
	})

	It("warns about stray arguments and model paths", func() {
		r := parse(`llama-server stray -m /models/a.gguf`)
		Expect(r.Warnings).To(HaveLen(2))
		Expect(r.Fields.Parameters.Model).To(Equal("/models/a.gguf"))
	})

	It("passes several -ot patterns through and describes them", func() {
		r := parse(`-ot "blk\.3\.ffn_.*=CUDA0" -ot "\.ffn_.*_exps.*=CPU" --override-tensor x=CPU`)
		Expect(r.Options).To(HaveLen(3))
		Expect(r.Unmapped).To(BeEmpty())
		Expect(r.Placements).To(HaveLen(3))
		Expect(r.Placements[0].Placement).To(BeNil())
		Expect(r.Placements[1].Placement.Mode).To(Equal(ls.ExpertsCPUAll))
	})

	It("rejects an oversized command", func() {
		_, err := ls.ParseCommand(strings.Repeat("a ", ls.MaxCommandLength))
		Expect(err).To(HaveOccurred())
	})

	It("rejects NUL bytes", func() {
		_, err := ls.ParseCommand("llama-server\x00 -c 1")
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
		b := ls.BuildCommand(inklingSettings())
		Expect(b.Warnings).To(BeEmpty())
		Expect(b.Command).To(Equal(`llama-server -m Inkling-2-Mini-UD-Q3_K_XL.gguf --ctx-size 18000 -ngl 99 -t 6 ` +
			`-fa on -ctk q8_0 -ctv q8_0 --load-mode none --jinja -np 1 -ub 64 -tb 12 -fit off --no-op-offload ` +
			`--temp 1.0 --top-p 1.0 --top-k 0 --min-p 0.0 -ot "blk\.([0-6]|1[89]|2[0-3])\.ffn_.*_exps.*=CPU"`))
		r := parse(b.Command)
		Expect(r.Unmapped).To(BeEmpty())
		got := r.Fields
		got.Options = r.Options
		Expect(got).To(Equal(inklingSettings()))
	})

	It("round-trips the original command through parse and build and parse", func() {
		first := parse(inklingCommand)
		in := first.Fields
		in.Options = first.Options
		second := parse(ls.BuildCommand(in).Command)
		Expect(second.Fields).To(Equal(first.Fields))
		Expect(second.Options).To(Equal(first.Options))
	})

	It("builds nothing but the binary for an empty config", func() {
		Expect(ls.BuildCommand(ls.Settings{}).Command).To(Equal("llama-server"))
	})

	It("writes load modes", func() {
		Expect(ls.BuildCommand(ls.Settings{MMap: ptr(true)}).Command).To(Equal("llama-server --load-mode mmap"))
		Expect(ls.BuildCommand(ls.Settings{MMlock: ptr(true)}).Command).To(Equal("llama-server --load-mode mlock"))
		Expect(ls.BuildCommand(ls.Settings{MMap: ptr(true), MMlock: ptr(true)}).Command).To(Equal("llama-server --load-mode mmap_mlock"))
		Expect(ls.BuildCommand(ls.Settings{Options: []string{"direct_io:true"}}).Command).To(Equal("llama-server --load-mode direct_io"))
	})

	It("understands option aliases and bare booleans", func() {
		b := ls.BuildCommand(ls.Settings{Options: []string{"jinja", "n_parallel:2", "no_op_offload:false", "fit_params:off"}})
		Expect(b.Command).To(Equal("llama-server --jinja -np 2 -fit off --op-offload"))
	})

	It("warns about options that have no flag and bad values", func() {
		b := ls.BuildCommand(ls.Settings{Options: []string{"context_shift:true", "parallel:x"}})
		Expect(b.Warnings).To(HaveLen(2))
		Expect(b.Command).To(Equal("llama-server"))
	})

	It("quotes values that need it and survives a parse", func() {
		in := ls.Settings{Parameters: &ls.Parameters{Model: `my model $x "q".gguf`}, Options: []string{`--chat-template:a b\`}}
		r := parse(ls.BuildCommand(in).Command)
		Expect(r.Fields.Parameters.Model).To(Equal(`my model $x "q".gguf`))
		Expect(r.Options).To(Equal([]string{`--chat-template:a b\`}))
	})

	It("does not modify its input", func() {
		in := inklingSettings()
		before, _ := json.Marshal(in)
		ls.BuildCommand(in)
		after, _ := json.Marshal(in)
		Expect(after).To(Equal(before))
		Expect(ls.BuildCommand(ls.Settings{}).Command).To(Equal("llama-server"))
	})
})

var _ = Describe("Flags table", func() {
	It("has unique keys, CLI spellings and docs", func() {
		keys, cli := map[string]bool{}, map[string]bool{}
		for _, f := range ls.Flags {
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

func ptr[T any](v T) *T { return &v }

var _ = Describe("Tensor placement", func() {
	match := func(pattern string, n int) bool {
		expr, _, _ := strings.Cut(pattern, "=CPU")
		return regexp.MustCompile(expr).MatchString("blk." + strconv.Itoa(n) + ".ffn_down_exps.weight")
	}

	It("builds the regex of the Inkling example from ranges", func() {
		rs, err := ls.ParseLayerRanges("0-6,18-23")
		Expect(err).ToNot(HaveOccurred())
		p := ls.Placement{Mode: ls.ExpertsCPULayers, Layers: rs}
		got, err := p.Pattern()
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(`blk\.([0-6]|1[89]|2[0-3])\.ffn_.*_exps.*=CPU`))
	})

	It("keeps all experts on the CPU", func() {
		o, err := ls.Placement{Mode: ls.ExpertsCPUAll}.Option()
		Expect(err).ToNot(HaveOccurred())
		Expect(o).To(Equal(`-ot:\.ffn_.*_exps.*=CPU`))
		Expect(match(o[4:], 7)).To(BeTrue())
		Expect(regexp.MustCompile(`\.ffn_.*_exps.*`).MatchString("blk.1.ffn_gate_shexp.weight")).To(BeFalse())
	})

	DescribeTable("writes ranges",
		func(spec, group string) {
			rs, err := ls.ParseLayerRanges(spec)
			Expect(err).ToNot(HaveOccurred())
			got, err := ls.Placement{Mode: ls.ExpertsCPULayers, Layers: rs}.Pattern()
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
			rs, err := ls.ParseLayerRanges(spec)
			Expect(err).ToNot(HaveOccurred())
			want := map[int]bool{}
			for _, r := range rs {
				for n := r.From; n <= r.To; n++ {
					want[n] = true
				}
			}
			pat, err := ls.Placement{Mode: ls.ExpertsCPULayers, Layers: rs}.Pattern()
			Expect(err).ToNot(HaveOccurred())
			for n := 0; n <= 1200; n++ {
				Expect(match(pat, n)).To(Equal(want[n]), "spec %s layer %d", spec, n)
			}
		}
	})

	It("keeps the first N layers on the GPU", func() {
		for _, n := range []int{1, 7, 9, 10, 18, 19, 20, 99, 100, 345} {
			pat, err := ls.Placement{Mode: ls.FirstLayersGPU, FirstGPULayers: n}.Pattern()
			Expect(err).ToNot(HaveOccurred())
			Expect(pat).ToNot(ContainSubstring(","))
			for l := 0; l <= 1500; l++ {
				Expect(match(pat, l)).To(Equal(l >= n), "n %d layer %d (%s)", n, l, pat)
			}
		}
	})

	It("uses a bounded range when the layer count is known", func() {
		got, err := ls.Placement{Mode: ls.FirstLayersGPU, FirstGPULayers: 8, TotalLayers: 24}.Pattern()
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(`blk\.([89]|1[0-9]|2[0-3])\.ffn_.*_exps.*=CPU`))
		_, err = ls.Placement{Mode: ls.FirstLayersGPU, FirstGPULayers: 24, TotalLayers: 24}.Pattern()
		Expect(err).To(HaveOccurred())
	})

	It("treats zero GPU layers as all experts on the CPU", func() {
		got, _ := ls.Placement{Mode: ls.FirstLayersGPU}.Pattern()
		Expect(got).To(Equal(`\.ffn_.*_exps.*=CPU`))
	})

	It("rejects bad input", func() {
		for _, spec := range []string{"a", "5-3", "-1", "1000", "1-x"} {
			_, err := ls.ParseLayerRanges(spec)
			Expect(err).To(HaveOccurred(), spec)
		}
		_, err := ls.Placement{Mode: ls.ExpertsCPULayers}.Pattern()
		Expect(err).To(HaveOccurred())
		_, err = ls.Placement{Mode: "nope"}.Pattern()
		Expect(err).To(HaveOccurred())
		_, err = ls.Placement{Mode: ls.FirstLayersGPU, FirstGPULayers: 5000}.Pattern()
		Expect(err).To(HaveOccurred())
	})

	It("formats ranges", func() {
		rs, _ := ls.ParseLayerRanges("18-23, 5,0-6")
		Expect(ls.FormatLayerRanges(rs)).To(Equal("0-6,18-23"))
	})

	It("recognises what it generates", func() {
		cases := []ls.Placement{
			{Mode: ls.ExpertsCPUAll},
			{Mode: ls.ExpertsCPULayers, Layers: []ls.LayerRange{{0, 6}, {18, 23}}},
			{Mode: ls.ExpertsCPULayers, Layers: []ls.LayerRange{{5, 5}}},
			{Mode: ls.ExpertsCPULayers, Layers: []ls.LayerRange{{98, 101}, {200, 200}}},
			{Mode: ls.FirstLayersGPU, FirstGPULayers: 12},
			{Mode: ls.FirstLayersGPU, FirstGPULayers: 105},
		}
		for _, c := range cases {
			o, err := c.Option()
			Expect(err).ToNot(HaveOccurred())
			got, ok := ls.ParseOption(o)
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
			_, ok := ls.ParseOption(o)
			Expect(ok).To(BeFalse(), o)
		}
	})

	It("does not compile huge groups", func() {
		_, ok := ls.ParsePattern(`blk\.(` + strings.Repeat("1|", 5000) + `1)\.ffn_.*_exps.*=CPU`)
		Expect(ok).To(BeFalse())
	})
})
