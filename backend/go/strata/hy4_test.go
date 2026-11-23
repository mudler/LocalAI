package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// hy4Dir makes a model directory with the three Hy4-Flash files.
func hy4Dir(files ...string) string {
	dir := GinkgoT().TempDir()
	if len(files) == 0 {
		files = []string{"Hy4-Flash-SQ-64.strata", hy4EngramFile, hy4VisionFile}
	}
	for _, f := range files {
		Expect(os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600)).To(Succeed())
	}
	return dir
}

func hy4Opts(dir string, extra ...string) *pb.ModelOptions {
	exe, _ := os.Executable()
	return &pb.ModelOptions{
		ModelFile: dir,
		Options:   append([]string{"family:hy4", "engine_path:" + exe, "strata_root:/unused", "native:Hy4-Flash-SQ-64.strata"}, extra...),
	}
}

// writeStrata writes a .strata file whose Section 0 is the given JSON.
func writeStrata(path string, manifest string) {
	enc, err := zstd.NewWriter(nil)
	Expect(err).ToNot(HaveOccurred())
	comp := enc.EncodeAll([]byte(manifest), nil)
	head := make([]byte, 32)
	copy(head, strataMagic)
	binary.LittleEndian.PutUint64(head[16:], 32)
	binary.LittleEndian.PutUint64(head[24:], uint64(len(comp)))
	Expect(os.WriteFile(path, append(head, comp...), 0o600)).To(Succeed())
}

var _ = Describe("hy4 family options", func() {
	It("finds the three files next to the build and builds the start line", func() {
		dir := hy4Dir()
		c, err := parseLoad(hy4Opts(dir))
		Expect(err).ToNot(HaveOccurred())
		Expect(c.family).To(Equal(familyHy4))
		Expect(c.hy4.native).To(Equal(filepath.Join(dir, "Hy4-Flash-SQ-64.strata")))
		c.hy4.applyInfo(map[string]string{"drafter": "dfly2", "images": "1"})
		Expect(c.engineArgs()).To(Equal([]string{
			"--native", filepath.Join(dir, "Hy4-Flash-SQ-64.strata"),
			"--engram", filepath.Join(dir, hy4EngramFile),
			"--max-context", "131072",
			"--vision", filepath.Join(dir, hy4VisionFile),
			"--draft", "dfly2",
		}))
		Expect(c.env).To(ContainElement("STRATA_ENGINE_FAMILY=hy4"))
	})

	It("never sends the Qwen flags and leaves out what the engine cannot do", func() {
		c, err := parseLoad(hy4Opts(hy4Dir(), "vram_experts_gb:3.5", "ram_experts_gb:50", "engine_args:--chunk 512"))
		Expect(err).ToNot(HaveOccurred())
		c.ctxSize = 8192
		Expect(c.hy4.applyInfo(nil)).To(HaveLen(2))
		args := strings.Join(c.engineArgs(), " ")
		Expect(args).To(ContainSubstring("--max-context 8192 --vram-experts-gb 3.5 --ram-experts-gb 50 --chunk 512"))
		for _, f := range []string{"--pack", "--vision", "--draft", "--vram-reserve-mib"} {
			Expect(args).ToNot(ContainSubstring(f))
		}
	})

	It("lets the user's own --draft stand", func() {
		c, err := parseLoad(hy4Opts(hy4Dir(), "engine_args:--draft off"))
		Expect(err).ToNot(HaveOccurred())
		c.hy4.applyInfo(map[string]string{"drafter": "dfly2"})
		Expect(strings.Count(strings.Join(c.engineArgs(), " "), "--draft")).To(Equal(1))
	})

	It("can turn the drafter and the vision file off", func() {
		c, err := parseLoad(hy4Opts(hy4Dir(), "drafter:off", "vision:off"))
		Expect(err).ToNot(HaveOccurred())
		Expect(c.hy4.applyInfo(map[string]string{"drafter": "dfly2", "images": "1"})).To(BeEmpty())
		Expect(strings.Join(c.engineArgs(), " ")).ToNot(ContainSubstring("--draft"))
		Expect(c.hy4.vision).To(BeEmpty())
	})

	It("starts without a vision file when none is installed", func() {
		c, err := parseLoad(hy4Opts(hy4Dir("Hy4-Flash-SQ-64.strata", hy4EngramFile)))
		Expect(err).ToNot(HaveOccurred())
		Expect(c.hy4.vision).To(BeEmpty())
	})

	DescribeTable("refuses a bad configuration",
		func(files []string, extra []string, want string) {
			opts := hy4Opts(hy4Dir(files...), extra...)
			if want == "native option" {
				opts.Options = []string{"family:hy4", opts.Options[1], "strata_root:/unused"}
			}
			_, err := parseLoad(opts)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(want))
		},
		Entry("no native option", nil, nil, "native option"),
		Entry("no Engram file", []string{"Hy4-Flash-SQ-64.strata"}, nil, "Engram"),
		Entry("vision on without a file", []string{"Hy4-Flash-SQ-64.strata", hy4EngramFile}, []string{"vision:on"}, "vision file is missing"),
		Entry("bad drafter", nil, []string{"drafter:mtp"}, "drafter"),
		Entry("bad expert budget", nil, []string{"ram_experts_gb:lots"}, "ram_experts_gb"),
	)

	It("keeps the family list honest", func() {
		_, err := parseLoad(&pb.ModelOptions{Options: []string{"family:other", "engine_path:/x"}})
		Expect(err).To(MatchError(ContainSubstring("qwen, hy4 or inkling")))
	})
})

var _ = Describe("hy4 engine facts", func() {
	It("reads the --info line", func() {
		info := parseHy4Info("noise\narch=hy4flash layers=42 drafter=dfly2 images=1\n")
		Expect(info).To(HaveKeyWithValue("drafter", "dfly2"))
		Expect(info).To(HaveKeyWithValue("images", "1"))
		Expect(parseHy4Info("nothing here")).To(BeNil())
	})

	It("escapes a picture path the way the protocol wants", func() {
		Expect(encodeHy4Path("/a b/c%d.png")).To(Equal("/a%20b/c%25d.png"))
	})
})

var _ = Describe("hy4 model file", func() {
	It("reads Section 0", func() {
		p := filepath.Join(GinkgoT().TempDir(), "m.strata")
		writeStrata(p, `{"config":{"a":1},"tokenizer_json":"{}","chat_template":"t","tensors":[1,2,3]}`)
		raw, err := readStrataSection0(p)
		Expect(err).ToNot(HaveOccurred())
		trimmed, err := tokenizerManifest(raw)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(trimmed)).To(ContainSubstring("chat_template"))
		Expect(string(trimmed)).ToNot(ContainSubstring("tensors"))
	})

	It("refuses a file that is not a .strata file or is cut short", func() {
		dir := GinkgoT().TempDir()
		bad := filepath.Join(dir, "bad.strata")
		Expect(os.WriteFile(bad, bytes.Repeat([]byte("z"), 64), 0o600)).To(Succeed())
		_, err := readStrataSection0(bad)
		Expect(err).To(MatchError(ContainSubstring("STRATA magic")))

		good := filepath.Join(dir, "good.strata")
		writeStrata(good, `{}`)
		b, _ := os.ReadFile(good)
		Expect(os.WriteFile(good, b[:len(b)-3], 0o600)).To(Succeed())
		_, err = readStrataSection0(good)
		Expect(err).To(MatchError(ContainSubstring("cut short")))
	})

	It("refuses a manifest without the tokenizer", func() {
		_, err := tokenizerManifest([]byte(`{"config":{}}`))
		Expect(err).To(MatchError(ContainSubstring("tokenizer_json")))
	})

	It("keeps a tokenizer per build, and rebuilds it when the file changes", func() {
		dir := GinkgoT().TempDir()
		native := filepath.Join(dir, "Hy4-Flash-SQ-64.strata")
		Expect(os.WriteFile(native, []byte("abc"), 0o600)).To(Succeed())
		pack := hy4PackDir(native)
		Expect(filepath.Base(pack)).To(Equal("Hy4-Flash-SQ-64.tokenizer"))
		stamp, err := hy4Stamp(native)
		Expect(err).ToNot(HaveOccurred())
		Expect(hy4TokenizerComplete(pack, stamp)).To(BeFalse())
		Expect(os.MkdirAll(filepath.Join(pack, "tokenizer"), 0o750)).To(Succeed())
		for _, f := range hy4TokenizerFiles {
			Expect(os.WriteFile(filepath.Join(pack, "tokenizer", f), []byte("x"), 0o600)).To(Succeed())
		}
		Expect(os.WriteFile(filepath.Join(pack, "source.txt"), []byte(stamp), 0o600)).To(Succeed())
		Expect(hy4TokenizerComplete(pack, stamp)).To(BeTrue())
		Expect(os.WriteFile(native, []byte("abcd"), 0o600)).To(Succeed())
		newStamp, _ := hy4Stamp(native)
		Expect(hy4TokenizerComplete(pack, newStamp)).To(BeFalse())
	})
})

// loadedHy4 loads a Hy4 model through Load with the stub engine and stubbed
// tokenizer and --info seams.
func loadedHy4(info map[string]string, env []string, extra ...string) (*Strata, *fakeTokenizer, engineConfig) {
	dir := hy4Dir()
	ft := &fakeTokenizer{}
	s := NewStrata()
	var seen engineConfig
	var job hy4TokenizerJob
	s.prepareHy4Tokenizer = func(j hy4TokenizerJob) error { job = j; return nil }
	s.probeHy4Info = func(string, string, []string) (map[string]string, error) { return info, nil }
	s.newTokenizer = func(_, _, pack, family string, _ time.Duration) (Tokenizer, error) {
		Expect(family).To(Equal(familyHy4))
		Expect(pack).To(Equal(job.pack))
		return ft, nil
	}
	s.startEngine = func(c engineConfig) (*engine, error) {
		c.Env = append(c.Env, "STRATA_STUB_MODE=ok")
		c.Env = append(c.Env, env...)
		seen = c
		return startEngine(c)
	}
	opts := hy4Opts(dir, append([]string{"ready_timeout:5s"}, extra...)...)
	opts.ContextSize = 2048
	Expect(s.Load(opts)).To(Succeed())
	DeferCleanup(func() { _ = s.Free() })
	Expect(job.native).To(Equal(filepath.Join(dir, "Hy4-Flash-SQ-64.strata")))
	return s, ft, seen
}

func tinyPNG() string {
	var b bytes.Buffer
	Expect(png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)))).To(Succeed())
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

var _ = Describe("hy4 backend", func() {
	It("starts the engine with the Hy4 line and stops on the Hy4 end tokens", func() {
		s, _, seen := loadedHy4(map[string]string{"drafter": "dfly2", "images": "1"}, []string{"STRATA_STUB_TOKENS=72,105,120025"})
		Expect(strings.Join(seen.Args, " ")).To(ContainSubstring("--draft dfly2"))
		Expect(strings.Join(seen.Args, " ")).To(ContainSubstring("--max-context 2048"))
		r, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{
			UseTokenizerTemplate: true, Messages: []*pb.Message{{Role: "user", Content: "hello"}}, Tokens: 20,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(r.Message)).To(Equal("Hi"))
	})

	It("sends a picture as an image= key and a plain GEN line", func() {
		log := filepath.Join(GinkgoT().TempDir(), "gen.log")
		s, _, _ := loadedHy4(map[string]string{"images": "1"}, []string{"STRATA_STUB_LOG=" + log, "STRATA_STUB_TOKENS=65"})
		_, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{
			UseTokenizerTemplate: true, Tokens: 5, Images: []string{tinyPNG()},
			Messages: []*pb.Message{{Role: "user", Content: "what is this <__media__>"}},
		})
		Expect(err).ToNot(HaveOccurred())
		b, _ := os.ReadFile(log)
		line := strings.TrimSpace(string(b))
		Expect(line).To(HavePrefix("GEN 5 image="))
		Expect(line).To(MatchRegexp(`image=\S+\.png `))
		// The file is gone once the request is done.
		path := strings.TrimPrefix(strings.Fields(line)[2], "image=")
		_, statErr := os.Stat(path)
		Expect(os.IsNotExist(statErr)).To(BeTrue())
	})

	It("refuses a picture when the engine cannot read pictures", func() {
		s, _, _ := loadedHy4(map[string]string{"drafter": "dfly2"}, nil)
		_, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{
			UseTokenizerTemplate: true, Tokens: 5, Images: []string{tinyPNG()},
			Messages: []*pb.Message{{Role: "user", Content: "x <__media__>"}},
		})
		Expect(err).To(MatchError(ContainSubstring("cannot read images")))
	})

	It("refuses logprobs, which the engine does not have", func() {
		s, _, _ := loadedHy4(nil, nil)
		_, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{Prompt: "x", Tokens: 5, Logprobs: 1, TopLogprobs: 2})
		Expect(err).To(MatchError(ContainSubstring("logprobs")))
	})

	It("starts without the drafter when the engine cannot be asked", func() {
		_, _, seen := loadedHy4(nil, nil)
		Expect(strings.Join(seen.Args, " ")).ToNot(ContainSubstring("--draft"))
		Expect(strings.Join(seen.Args, " ")).ToNot(ContainSubstring("--vision"))
	})
})
