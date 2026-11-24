package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// nativeModel makes a model directory with a two-file split GGUF set, the
// vision projector and a finished pack. native is true for the pack the iq tool
// makes, false for a pack with only a tokenizer.
func nativeModel(native bool) string {
	dir := filepath.Join(GinkgoT().TempDir(), "model")
	files := []string{
		"m-00001-of-00002.gguf", "m-00002-of-00002.gguf", "mmproj-m-BF16.gguf",
		filepath.Join("pack", "tokenizer", "vocab.json"),
		filepath.Join("pack", "tokenizer", "chat_template.jinja"),
	}
	if native {
		files = append(files, filepath.Join("pack", "native_experts.txt"), filepath.Join("pack", "index.txt"))
	}
	for _, f := range files {
		p := filepath.Join(dir, f)
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte("x"), 0o644)).To(Succeed())
	}
	return dir
}

func nativeLoad(dir string, opts ...string) (loadConfig, error) {
	exe, _ := os.Executable()
	return parseLoad(&pb.ModelOptions{ModelFile: dir, ContextSize: 2048,
		Options: append([]string{"engine_path:" + exe}, opts...)})
}

func nativeArgs(dir string, opts ...string) string {
	c, err := nativeLoad(dir, opts...)
	Expect(err).ToNot(HaveOccurred())
	return strings.Join(c.engineArgs(), " ")
}

var _ = Describe("native (IQ) pack", func() {
	It("adds --spec and --prefill to a native pack", func() {
		dir := nativeModel(true)
		c, err := nativeLoad(dir, "native:m-00001-of-00002.gguf", "ple_gguf:m-00002-of-00002.gguf")
		Expect(err).ToNot(HaveOccurred())
		Expect(c.nativePack).To(BeTrue())
		Expect(strings.Join(c.engineArgs(), " ")).To(Equal(strings.Join([]string{
			"--pack", filepath.Join(dir, "pack"),
			"--native", filepath.Join(dir, "m-00001-of-00002.gguf"),
			"--ple-gguf", filepath.Join(dir, "m-00002-of-00002.gguf"),
			"--max-context", "2048", "--spec", "4", "--prefill", "auto"}, " ")))
	})

	It("uses the first shard when native names a later one", func() {
		dir := nativeModel(true)
		Expect(nativeArgs(dir, "native:m-00002-of-00002.gguf")).To(ContainSubstring(
			"--native " + filepath.Join(dir, "m-00001-of-00002.gguf") + " "))
	})

	It("takes the first shard from pack_gguf when native is not set", func() {
		dir := nativeModel(true)
		Expect(nativeArgs(dir, "pack_gguf:m-00001-of-00002.gguf", "build_pack:false")).To(ContainSubstring(
			"--native " + filepath.Join(dir, "m-00001-of-00002.gguf") + " "))
		// A later shard in pack_gguf leads to the first one as well.
		Expect(nativeArgs(dir, "pack_gguf:m-00002-of-00002.gguf", "build_pack:false")).To(ContainSubstring(
			"--native " + filepath.Join(dir, "m-00001-of-00002.gguf") + " "))
	})

	It("finds the one split model in the directory, not the projector", func() {
		dir := nativeModel(true)
		Expect(nativeArgs(dir)).To(ContainSubstring("--native " + filepath.Join(dir, "m-00001-of-00002.gguf") + " "))
	})

	It("asks for the native option when it cannot find the model", func() {
		dir := nativeModel(true)
		Expect(os.Remove(filepath.Join(dir, "m-00001-of-00002.gguf"))).To(Succeed())
		_, err := nativeLoad(dir)
		Expect(err).To(MatchError(ContainSubstring("native option")))
	})

	It("keeps the spec and prefill the user sets", func() {
		dir := nativeModel(true)
		args := nativeArgs(dir, "native:m-00001-of-00002.gguf", "engine_args:--spec 3 --prefill 2048 --expert-cache auto")
		Expect(strings.Count(args, "--spec")).To(Equal(1))
		Expect(strings.Count(args, "--prefill")).To(Equal(1))
		Expect(args).To(HaveSuffix("--spec 3 --prefill 2048 --expert-cache auto"))

		// Only the missing one is added.
		args = nativeArgs(dir, "native:m-00001-of-00002.gguf", "engine_args:--prefill=auto:32768")
		Expect(args).To(ContainSubstring("--spec 4"))
		Expect(strings.Count(args, "--prefill")).To(Equal(1))
	})

	It("treats a pack that is still to be built as native", func() {
		dir := nativeModel(true)
		Expect(os.RemoveAll(filepath.Join(dir, "pack"))).To(Succeed())
		root := GinkgoT().TempDir()
		args := nativeArgs(dir, "native:m-00001-of-00002.gguf", "pack_gguf:m-00001-of-00002.gguf", "strata_root:"+root)
		Expect(args).To(ContainSubstring("--spec 4 --prefill auto"))
	})

	It("leaves a pack without native experts as it was", func() {
		dir := nativeModel(false)
		c, err := nativeLoad(dir, "native:m-00002-of-00002.gguf", "engine_args:--mtp 2")
		Expect(err).ToNot(HaveOccurred())
		Expect(c.nativePack).To(BeFalse())
		Expect(strings.Join(c.engineArgs(), " ")).To(Equal(strings.Join([]string{
			"--pack", filepath.Join(dir, "pack"),
			"--native", filepath.Join(dir, "m-00002-of-00002.gguf"),
			"--max-context", "2048", "--mtp", "2"}, " ")))
	})

	It("leaves the tokenizer-only pack tool as it was", func() {
		dir := nativeModel(true)
		args := nativeArgs(dir, "native:m-00001-of-00002.gguf", "pack_gguf:m-00001-of-00002.gguf", "pack_tool:tokenizer")
		Expect(args).ToNot(ContainSubstring("--spec"))
		Expect(args).ToNot(ContainSubstring("--prefill"))
		Expect(args).ToNot(ContainSubstring("--pack"))
	})

	It("starts the engine that refuses an incomplete command line", func() {
		dir := nativeModel(true)
		exe, _ := os.Executable()
		load := func(extra ...string) error {
			s := NewStrata()
			s.newTokenizer = func(string, string, string, string, time.Duration) (Tokenizer, error) { return &fakeTokenizer{}, nil }
			s.startEngine = func(c engineConfig) (*engine, error) {
				c.Env = append(c.Env, "STRATA_STUB_MODE=native")
				return startEngine(c)
			}
			DeferCleanup(func() { _ = s.Free() })
			return s.Load(&pb.ModelOptions{ModelFile: dir, ContextSize: 2048,
				Options: append([]string{"engine_path:" + exe, "strata_root:" + GinkgoT().TempDir(), "ready_timeout:5s", "native:m-00002-of-00002.gguf"}, extra...)})
		}
		Expect(load()).To(Succeed())
		// The old behaviour: with --spec 1 the stub engine exits as the real one does.
		err := load("engine_args:--spec 1")
		Expect(err).To(MatchError(ContainSubstring("is a native (IQ) pack")))
	})
})
