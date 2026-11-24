package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const inklingStrata = "Inkling-2-Mini-SQ-10.strata"
const inklingGGUF = "Inkling-2-Mini-UD-Q4_K_XL.gguf"

// inklingDir makes a model directory with the given files.
func inklingDir(files ...string) string {
	dir := GinkgoT().TempDir()
	if len(files) == 0 {
		files = []string{inklingStrata, inklingVisionFile}
	}
	for _, f := range files {
		Expect(os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600)).To(Succeed())
	}
	return dir
}

func inklingOpts(dir, native string, extra ...string) *pb.ModelOptions {
	exe, _ := os.Executable()
	return &pb.ModelOptions{
		ModelFile: dir,
		Options:   append([]string{"family:inkling", "engine_path:" + exe, "strata_root:/unused", "native:" + native}, extra...),
	}
}

var _ = Describe("inkling family options", func() {
	It("finds the vision file next to a Strata build and builds the start line", func() {
		dir := inklingDir()
		c, err := parseLoad(inklingOpts(dir, inklingStrata))
		Expect(err).ToNot(HaveOccurred())
		Expect(c.family).To(Equal(familyInkling))
		Expect(c.inkling.isGGUF()).To(BeFalse())
		c.inkling.applyInfo(map[string]string{"drafter": "dspark", "images": "1"})
		Expect(c.engineArgs()).To(Equal([]string{
			"--native", filepath.Join(dir, inklingStrata),
			"--max-context", "32768",
			"--vision", filepath.Join(dir, inklingVisionFile),
			"--draft", "dspark",
		}))
		Expect(c.env).To(ContainElement("STRATA_ENGINE_FAMILY=inkling"))
	})

	It("passes the DSpark drafter file of a GGUF model", func() {
		dir := inklingDir(inklingGGUF, inklingDrafterFile)
		c, err := parseLoad(inklingOpts(dir, inklingGGUF, "vram_experts_gb:3.5", "ram_experts_gb:20"))
		Expect(err).ToNot(HaveOccurred())
		Expect(c.inkling.isGGUF()).To(BeTrue())
		Expect(c.inkling.vision).To(BeEmpty())
		Expect(c.inkling.infoArgs()).To(Equal([]string{"--info", filepath.Join(dir, inklingGGUF), "--dspark", filepath.Join(dir, inklingDrafterFile)}))
		c.inkling.applyInfo(map[string]string{"drafter": "dspark"})
		args := strings.Join(c.engineArgs(), " ")
		Expect(args).To(ContainSubstring("--dspark " + filepath.Join(dir, inklingDrafterFile)))
		Expect(args).To(ContainSubstring("--vram-experts-gb 3.5 --ram-experts-gb 20"))
		Expect(args).To(ContainSubstring("--draft dspark"))
	})

	It("starts a GGUF model without a drafter when the file is missing", func() {
		c, err := parseLoad(inklingOpts(inklingDir(inklingGGUF), inklingGGUF))
		Expect(err).ToNot(HaveOccurred())
		Expect(c.inkling.drafter).To(BeEmpty())
		Expect(strings.Join(c.engineArgs(), " ")).To(ContainSubstring("--draft off"))
	})

	It("never sends the Qwen flags and leaves out what the engine cannot do", func() {
		c, err := parseLoad(inklingOpts(inklingDir(), inklingStrata, "engine_args:--parallel 4"))
		Expect(err).ToNot(HaveOccurred())
		c.ctxSize = 8192
		Expect(c.inkling.applyInfo(nil)).To(HaveLen(2))
		args := strings.Join(c.engineArgs(), " ")
		Expect(args).To(ContainSubstring("--max-context 8192"))
		Expect(args).To(HaveSuffix("--parallel 4"))
		for _, f := range []string{"--pack", "--vision", "--engram", "--vram-reserve-mib"} {
			Expect(args).ToNot(ContainSubstring(f))
		}
	})

	It("lets the user's own --draft stand", func() {
		c, err := parseLoad(inklingOpts(inklingDir(), inklingStrata, "engine_args:--draft off"))
		Expect(err).ToNot(HaveOccurred())
		c.inkling.applyInfo(map[string]string{"drafter": "dspark"})
		Expect(strings.Count(strings.Join(c.engineArgs(), " "), "--draft")).To(Equal(1))
	})

	It("can turn the drafter and the vision file off", func() {
		c, err := parseLoad(inklingOpts(inklingDir(), inklingStrata, "drafter:off", "vision:off"))
		Expect(err).ToNot(HaveOccurred())
		Expect(c.inkling.applyInfo(map[string]string{"drafter": "dspark", "images": "1"})).To(BeEmpty())
		Expect(strings.Join(c.engineArgs(), " ")).To(ContainSubstring("--draft off"))
		Expect(c.inkling.vision).To(BeEmpty())
	})

	It("refuses a named drafter file that is missing", func() {
		_, err := parseLoad(inklingOpts(inklingDir(inklingGGUF), inklingGGUF, "dspark:nope.gguf"))
		Expect(err).To(MatchError(ContainSubstring("dspark option")))
	})

	It("picks up tokenizer files beside the model", func() {
		dir := inklingDir(inklingGGUF, inklingTokenizerFile, inklingTemplateFile)
		c, err := parseLoad(inklingOpts(dir, inklingGGUF))
		Expect(err).ToNot(HaveOccurred())
		Expect(c.inkling.tokenizerJSON).To(Equal(filepath.Join(dir, inklingTokenizerFile)))
		Expect(c.inkling.chatTemplate).To(Equal(filepath.Join(dir, inklingTemplateFile)))
	})

	DescribeTable("refuses a bad configuration",
		func(files []string, extra []string, want string) {
			opts := inklingOpts(inklingDir(files...), inklingStrata, extra...)
			if want == "native option" {
				opts.Options = []string{"family:inkling", opts.Options[1], "strata_root:/unused"}
			}
			_, err := parseLoad(opts)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(want))
		},
		Entry("no native option", nil, nil, "native option"),
		Entry("model file missing", []string{inklingVisionFile}, nil, "native option"),
		Entry("vision on without a file", []string{inklingStrata}, []string{"vision:on"}, "vision file is missing"),
		Entry("bad drafter", nil, []string{"drafter:mtp"}, "drafter"),
		Entry("bad expert budget", nil, []string{"ram_experts_gb:lots"}, "ram_experts_gb"),
		Entry("named tokenizer file missing", nil, []string{"tokenizer_json:nope.json"}, "tokenizer_json option"),
	)
})

var _ = Describe("inkling tokenizer sources", func() {
	It("keeps the parts of a manifest the tokenizer tool reads", func() {
		m, hasTok, ok, err := inklingManifest([]byte(`{"config":{"a":1},"tokenizer_json":"{}","chat_template":"t","tensors":[1]}`))
		Expect(err).ToNot(HaveOccurred())
		Expect(hasTok).To(BeTrue())
		Expect(ok).To(BeTrue())
		Expect(string(m)).ToNot(ContainSubstring("tensors"))

		_, hasTok, ok, err = inklingManifest([]byte(`{"config":{},"tokenizer_json":null}`))
		Expect(err).ToNot(HaveOccurred())
		Expect(hasTok).To(BeFalse())
		Expect(ok).To(BeFalse())
	})

	It("reads the manifest of a Strata build first", func() {
		dir := GinkgoT().TempDir()
		native := filepath.Join(dir, inklingStrata)
		writeStrata(native, `{"config":{},"tokenizer_json":"{}","chat_template":"t"}`)
		args, stdin, err := inklingScriptArgs(inklingTokenizerJob{native: native})
		Expect(err).ToNot(HaveOccurred())
		Expect(args).To(Equal([]string{"manifest", ""}))
		Expect(string(stdin)).To(ContainSubstring("chat_template"))
	})

	It("takes a missing tokenizer from the file beside the build", func() {
		dir := GinkgoT().TempDir()
		native := filepath.Join(dir, inklingStrata)
		writeStrata(native, `{"config":{},"chat_template":"t"}`)
		tj := filepath.Join(dir, inklingTokenizerFile)
		args, _, err := inklingScriptArgs(inklingTokenizerJob{native: native, tokenizerJSON: tj})
		Expect(err).ToNot(HaveOccurred())
		Expect(args).To(Equal([]string{"manifest", tj}))
	})

	It("uses the two files for a GGUF and says what is missing", func() {
		dir := GinkgoT().TempDir()
		native := filepath.Join(dir, inklingGGUF)
		Expect(os.WriteFile(native, []byte("x"), 0o600)).To(Succeed())
		_, _, err := inklingScriptArgs(inklingTokenizerJob{native: native})
		Expect(err).To(MatchError(ContainSubstring("no tokenizer")))
		args, stdin, err := inklingScriptArgs(inklingTokenizerJob{native: native, tokenizerJSON: "/t.json", chatTemplate: "/c.jinja"})
		Expect(err).ToNot(HaveOccurred())
		Expect(args).To(Equal([]string{"files", "/t.json", "/c.jinja"}))
		Expect(stdin).To(BeNil())
	})

	It("rebuilds the tokenizer when a source file changes", func() {
		dir := GinkgoT().TempDir()
		native := filepath.Join(dir, inklingGGUF)
		tj := filepath.Join(dir, inklingTokenizerFile)
		Expect(os.WriteFile(native, []byte("abc"), 0o600)).To(Succeed())
		Expect(os.WriteFile(tj, []byte("{}"), 0o600)).To(Succeed())
		job := inklingTokenizerJob{native: native, tokenizerJSON: tj}
		pack := inklingPackDir(native)
		Expect(filepath.Base(pack)).To(Equal("Inkling-2-Mini-UD-Q4_K_XL.tokenizer"))
		stamp, err := inklingStamp(job)
		Expect(err).ToNot(HaveOccurred())
		Expect(os.MkdirAll(filepath.Join(pack, "tokenizer"), 0o750)).To(Succeed())
		for _, f := range inklingTokenizerFiles {
			Expect(os.WriteFile(filepath.Join(pack, "tokenizer", f), []byte("x"), 0o600)).To(Succeed())
		}
		Expect(os.WriteFile(filepath.Join(pack, "source.txt"), []byte(stamp), 0o600)).To(Succeed())
		Expect(inklingTokenizerComplete(pack, stamp)).To(BeTrue())
		Expect(os.WriteFile(tj, []byte("{ }"), 0o600)).To(Succeed())
		newStamp, _ := inklingStamp(job)
		Expect(inklingTokenizerComplete(pack, newStamp)).To(BeFalse())
	})
})

// loadedInkling loads an Inkling model through Load with the stub engine and
// stubbed tokenizer and --info seams.
func loadedInkling(info map[string]string, env []string, extra ...string) (*Strata, *fakeTokenizer, engineConfig) {
	dir := inklingDir()
	ft := &fakeTokenizer{}
	s := NewStrata()
	var seen engineConfig
	var job inklingTokenizerJob
	s.prepareInklingTokenizer = func(j inklingTokenizerJob) error { job = j; return nil }
	s.probeInklingInfo = func(string, []string, []string) (map[string]string, error) { return info, nil }
	s.newTokenizer = func(_, _, pack, family string, _ time.Duration) (Tokenizer, error) {
		Expect(family).To(Equal(familyInkling))
		Expect(pack).To(Equal(job.pack))
		return ft, nil
	}
	s.startEngine = func(c engineConfig) (*engine, error) {
		c.Env = append(c.Env, "STRATA_STUB_MODE=ok")
		c.Env = append(c.Env, env...)
		seen = c
		return startEngine(c)
	}
	opts := inklingOpts(dir, inklingStrata, append([]string{"ready_timeout:5s"}, extra...)...)
	opts.ContextSize = 2048
	Expect(s.Load(opts)).To(Succeed())
	DeferCleanup(func() { _ = s.Free() })
	Expect(job.native).To(Equal(filepath.Join(dir, inklingStrata)))
	return s, ft, seen
}

var _ = Describe("inkling backend", func() {
	It("starts the engine with the Inkling line and stops on the Inkling end token", func() {
		s, _, seen := loadedInkling(map[string]string{"drafter": "dspark", "images": "1"}, []string{"STRATA_STUB_TOKENS=72,105,200006"})
		Expect(strings.Join(seen.Args, " ")).To(ContainSubstring("--draft dspark"))
		Expect(strings.Join(seen.Args, " ")).To(ContainSubstring("--max-context 2048"))
		r, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{
			UseTokenizerTemplate: true, Messages: []*pb.Message{{Role: "user", Content: "hello"}}, Tokens: 20,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(r.Message)).To(Equal("Hi"))
	})

	It("sends a picture as an image= key", func() {
		log := filepath.Join(GinkgoT().TempDir(), "gen.log")
		s, _, _ := loadedInkling(map[string]string{"images": "1"}, []string{"STRATA_STUB_LOG=" + log, "STRATA_STUB_TOKENS=65"})
		_, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{
			UseTokenizerTemplate: true, Tokens: 5, Images: []string{tinyPNG()},
			Messages: []*pb.Message{{Role: "user", Content: "what is this <__media__>"}},
		})
		Expect(err).ToNot(HaveOccurred())
		b, _ := os.ReadFile(log)
		line := strings.TrimSpace(string(b))
		Expect(line).To(HavePrefix("GEN 5 image="))
		path := strings.TrimPrefix(strings.Fields(line)[2], "image=")
		_, statErr := os.Stat(path)
		Expect(os.IsNotExist(statErr)).To(BeTrue())
	})

	It("refuses a picture when the engine cannot read pictures", func() {
		s, _, _ := loadedInkling(map[string]string{"drafter": "dspark"}, nil)
		_, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{
			UseTokenizerTemplate: true, Tokens: 5, Images: []string{tinyPNG()},
			Messages: []*pb.Message{{Role: "user", Content: "x <__media__>"}},
		})
		Expect(err).To(MatchError(ContainSubstring("cannot read images")))
	})

	It("refuses logprobs, which the engine does not have", func() {
		s, _, _ := loadedInkling(nil, nil)
		_, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{Prompt: "x", Tokens: 5, Logprobs: 1, TopLogprobs: 2})
		Expect(err).To(MatchError(ContainSubstring("logprobs")))
	})

	It("starts without the drafter and the picture reader when the engine cannot be asked", func() {
		_, _, seen := loadedInkling(nil, nil)
		Expect(strings.Join(seen.Args, " ")).ToNot(ContainSubstring("--draft dspark"))
		Expect(strings.Join(seen.Args, " ")).ToNot(ContainSubstring("--vision"))
	})

	It("writes the image placeholder of the chat template for a plain prompt", func() {
		Expect(inklingImageText).To(Equal("<|content_image|><|unused_200054|>"))
	})
})
