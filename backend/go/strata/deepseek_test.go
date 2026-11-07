package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// dsModel makes a DeepSeek model directory: the GGUF files the entries
// download and a Strata tree with the DeepSeek pack tool.
func dsModel() (dir, root string) {
	dir = filepath.Join(GinkgoT().TempDir(), "model")
	Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
	for _, n := range []string{"set-00001-of-00006.gguf", "set-00002-of-00006.gguf", "engram.gguf", "meta.gguf", "dspark.gguf", "profile.bin"} {
		Expect(os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644)).To(Succeed())
	}
	root = GinkgoT().TempDir()
	Expect(os.MkdirAll(filepath.Join(root, "tools", "dsv41"), 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(root, "tools", "dsv41", "pack_gguf.py"), nil, 0o644)).To(Succeed())
	return dir, root
}

// fakeDSPython stands in for the interpreter. It writes its arguments and its
// working directory to a file and makes the files pack_gguf.py makes.
func fakeDSPython(dir string) (script, record string) {
	record = filepath.Join(dir, "record")
	script = filepath.Join(dir, "fakepython")
	body := `#!/bin/sh
echo "cwd=$(pwd)" > "` + record + `"
echo "args=$*" >> "` + record + `"
out=""
while [ $# -gt 0 ]; do if [ "$1" = "--out" ]; then out=$2; fi; shift; done
mkdir -p "$out/tokenizer"
echo x > "$out/ds4.txt"; echo x > "$out/tensors.txt"; echo x > "$out/engram.bin"
echo '{}' > "$out/tokenizer/vocab.json"; echo x > "$out/tokenizer/chat_template.jinja"
`
	Expect(os.WriteFile(script, []byte(body), 0o755)).To(Succeed())
	return script, record
}

var dsPackArgs = "pack_args:--shards set-00001-of-00006.gguf set-00002-of-00006.gguf --engram engram.gguf --engram-meta meta.gguf --dspark-gguf dspark.gguf --dspark-noise-token 128799 --dspark-markov-rank 256"

var _ = Describe("deepseek41 family", func() {
	loadOpts := func(dir string, extra ...string) *pb.ModelOptions {
		exe, _ := os.Executable()
		return &pb.ModelOptions{ModelFile: dir, ContextSize: 8192, MainGPU: "1",
			Options: append([]string{"engine_path:" + exe, "strata_root:/unused", "family:deepseek41", "ready_timeout:5s"}, extra...)}
	}

	It("starts the DeepSeek engine with its own flags", func() {
		dir, _ := dsModel()
		pack := filepath.Join(dir, "pack")
		Expect(os.MkdirAll(filepath.Join(pack, "tokenizer"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(pack, "tokenizer", "vocab.json"), []byte("{}"), 0o644)).To(Succeed())
		c, err := parseLoad(loadOpts(dir, "expert_profile:profile.bin", "experts:5", "mmap_experts:true", "engine_args:--dspark off --prefill-chunk 4096"))
		Expect(err).ToNot(HaveOccurred())
		want := "--pack " + pack + " --expert-profile " + dir + "/profile.bin --max-context 8192 --gpus 1 --vision off --dspark auto --experts 5 --mmap-experts on --dspark off --prefill-chunk 4096"
		Expect(strings.Join(c.engineArgs(), " ")).To(Equal(want))
		Expect(c.family).To(Equal(familyDeepSeek41))
	})

	It("keeps the Qwen command line unchanged without the option", func() {
		dir, _ := dsModel()
		Expect(os.MkdirAll(filepath.Join(dir, "pack", "tokenizer"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "pack", "tokenizer", "vocab.json"), []byte("{}"), 0o644)).To(Succeed())
		exe, _ := os.Executable()
		c, err := parseLoad(&pb.ModelOptions{ModelFile: dir, ContextSize: 8192, MainGPU: "1", Options: []string{"engine_path:" + exe, "family:qwen", "mmap_experts:true"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(strings.Join(c.engineArgs(), " ")).To(Equal("--pack " + filepath.Join(dir, "pack") + " --max-context 8192 --gpu 1 --mmap-experts"))
	})

	It("reads the engine from its own variable, not the Qwen one", func() {
		dir, _ := dsModel()
		Expect(os.MkdirAll(filepath.Join(dir, "pack", "tokenizer"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "pack", "tokenizer", "vocab.json"), []byte("{}"), 0o644)).To(Succeed())
		exe, _ := os.Executable()
		GinkgoT().Setenv("STRATA_ENGINE_PATH", exe)
		GinkgoT().Setenv("STRATA_DSV41_ENGINE_PATH", "")
		_, err := parseLoad(&pb.ModelOptions{ModelFile: dir, Options: []string{"family:deepseek41"}})
		Expect(err).To(MatchError(ContainSubstring("STRATA_DSV41_ENGINE_PATH")))
		GinkgoT().Setenv("STRATA_DSV41_ENGINE_PATH", exe)
		c, err := parseLoad(&pb.ModelOptions{ModelFile: dir, Options: []string{"family:deepseek41"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(c.enginePath).To(Equal(exe))
	})

	It("refuses options that belong to the other engine", func() {
		dir, _ := dsModel()
		Expect(os.MkdirAll(filepath.Join(dir, "pack", "tokenizer"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "pack", "tokenizer", "vocab.json"), []byte("{}"), 0o644)).To(Succeed())
		for opt, want := range map[string]string{
			"native:set-00001-of-00006.gguf": "native",
			"ple_io:ram":                     "ple_io",
			"mmproj:meta.gguf":               "images are not supported",
			"vision_exe:/bin/true":           "images are not supported",
			"family:other":                   "family",
			"dspark:maybe":                   "dspark",
			"experts:4":                      "experts",
			"mmap_experts:sometimes":         "mmap_experts",
			"pack_tool:iq_pack":              "pack_tool",
		} {
			o := loadOpts(dir, opt)
			if strings.HasPrefix(opt, "family:") {
				o = loadOpts(dir)
				o.Options = append(o.Options[:2:2], opt)
				o.Options = append(o.Options, "ready_timeout:5s")
			}
			if strings.HasPrefix(opt, "pack_tool:") {
				o = loadOpts(dir, opt, "pack_gguf:set-00001-of-00006.gguf")
			}
			_, err := parseLoad(o)
			Expect(err).To(HaveOccurred(), opt)
			Expect(err.Error()).To(ContainSubstring(want), opt)
		}
		// The DeepSeek pack tool belongs to the DeepSeek family only.
		exe, _ := os.Executable()
		_, err := parseLoad(&pb.ModelOptions{ModelFile: dir, Options: []string{"engine_path:" + exe, "pack_gguf:set-00001-of-00006.gguf", "pack_tool:dsv41"}})
		Expect(err).To(MatchError(ContainSubstring("family:deepseek41")))
	})

	It("builds the pack with the DeepSeek tool in the model directory", func() {
		dir, root := dsModel()
		py, record := fakeDSPython(GinkgoT().TempDir())
		s := NewStrata()
		var gotArgs []string
		s.newTokenizer = func(string, string, string, string, time.Duration) (Tokenizer, error) { return &fakeTokenizer{}, nil }
		s.startEngine = func(c engineConfig) (*engine, error) {
			gotArgs = c.Args
			c.Env = append(c.Env, "STRATA_STUB_MODE=ok")
			return startEngine(c)
		}
		DeferCleanup(func() { _ = s.Free() })
		Expect(s.Load(loadOpts(dir, "strata_root:"+root, "python:"+py, "pack_gguf:set-00001-of-00006.gguf", dsPackArgs))).To(Succeed())

		b, err := os.ReadFile(record)
		Expect(err).ToNot(HaveOccurred())
		rec := string(b)
		real, _ := filepath.EvalSymlinks(dir)
		Expect(rec).To(ContainSubstring("cwd=" + real))
		Expect(rec).To(ContainSubstring("pack_gguf.py --model " + dir + " --out " + filepath.Join(dir, "pack.partial") + " --shards set-00001-of-00006.gguf set-00002-of-00006.gguf --engram engram.gguf --engram-meta meta.gguf --dspark-gguf dspark.gguf --dspark-noise-token 128799 --dspark-markov-rank 256"))
		Expect(filepath.Join(dir, "pack", "ds4.txt")).To(BeAnExistingFile())
		Expect(filepath.Join(dir, "pack.partial")).ToNot(BeADirectory())
		Expect(strings.Join(gotArgs, " ")).To(HavePrefix("--pack " + filepath.Join(dir, "pack") + " --max-context 8192 --gpus 1 --vision off --dspark auto"))
	})

	It("does not take a Qwen pack as a finished DeepSeek pack", func() {
		dir, _ := dsModel()
		// What the Qwen tool leaves is not enough: the DeepSeek tool's files are needed.
		for _, f := range []string{"native_experts.txt", "index.txt", filepath.Join("tokenizer", "vocab.json")} {
			p := filepath.Join(dir, "pack", f)
			Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
			Expect(os.WriteFile(p, []byte("x"), 0o644)).To(Succeed())
		}
		Expect(packComplete(filepath.Join(dir, "pack"), packToolIQ)).To(BeTrue())
		Expect(packComplete(filepath.Join(dir, "pack"), packToolDSV41)).To(BeFalse())
	})

	It("drops the end id from the output, and refuses images", func() {
		dir, _ := dsModel()
		pack := filepath.Join(dir, "pack")
		Expect(os.MkdirAll(filepath.Join(pack, "tokenizer"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(pack, "tokenizer", "vocab.json"), []byte("{}"), 0o644)).To(Succeed())
		s := NewStrata()
		s.newTokenizer = func(string, string, string, string, time.Duration) (Tokenizer, error) { return &fakeTokenizer{}, nil }
		s.startEngine = func(c engineConfig) (*engine, error) {
			c.Env = append(c.Env, "STRATA_STUB_MODE=ok", "STRATA_STUB_TOKENS=104,105,1")
			return startEngine(c)
		}
		DeferCleanup(func() { _ = s.Free() })
		Expect(s.Load(loadOpts(dir, "strata_root:/unused"))).To(Succeed())
		reply, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{Prompt: "q", Tokens: 16})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(reply.Message)).To(Equal("hi"))
		_, err = s.PredictRichContext(context.Background(), &pb.PredictOptions{Prompt: defaultMediaMarker, Images: []string{"x"}, Tokens: 4})
		Expect(err).To(MatchError(ContainSubstring("deepseek41")))
	})

	It("passes the family to the tokenizer, takes its end id and keeps the tools' key order", func() {
		dir, _ := dsModel()
		pack := filepath.Join(dir, "pack")
		Expect(os.MkdirAll(filepath.Join(pack, "tokenizer"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(pack, "tokenizer", "vocab.json"), []byte("{}"), 0o644)).To(Succeed())
		ft := &eosTokenizer{id: 104}
		var family string
		s := NewStrata()
		s.newTokenizer = func(_, _, _, f string, _ time.Duration) (Tokenizer, error) { family = f; return ft, nil }
		s.startEngine = func(c engineConfig) (*engine, error) {
			c.Env = append(c.Env, "STRATA_STUB_MODE=ok", "STRATA_STUB_TOKENS=105,104")
			return startEngine(c)
		}
		DeferCleanup(func() { _ = s.Free() })
		Expect(s.Load(loadOpts(dir, "strata_root:/unused"))).To(Succeed())
		Expect(family).To(Equal(familyDeepSeek41))
		Expect(s.eos).To(Equal(map[int]bool{104: true}))

		tools := `[{"type":"function","function":{"name":"f","parameters":{"type":"object","required":["a"],"properties":{"a":{"type":"string"}}}}}]`
		reply, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{
			UseTokenizerTemplate: true, Tokens: 8, Tools: tools,
			Messages: []*pb.Message{{Role: "user", Content: "q"}}})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(reply.Message)).To(Equal("i"))
		// "required" comes before "properties": a decoded map would put it after.
		Expect(ft.lastReq.Tools).To(Equal(json.RawMessage(tools)))
	})
})

// eosTokenizer is the fake tokenizer with an end id, as the helper reports it.
type eosTokenizer struct {
	fakeTokenizer
	id int
}

func (e *eosTokenizer) EOSID() (int, bool) { return e.id, true }
