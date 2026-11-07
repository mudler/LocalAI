package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakePython is a shell script that stands in for the interpreter. It makes
// the files the real tool makes, from its --out argument, and counts its runs.
func fakePython(dir string, fail bool) (string, string) {
	counter := filepath.Join(dir, "runs")
	script := filepath.Join(dir, "fakepython")
	body := `#!/bin/sh
echo run >> "` + counter + `"
tool=$(basename "$1")
out=""
while [ $# -gt 0 ]; do if [ "$1" = "--out" ]; then out=$2; fi; shift; done
echo "building $tool into $out"
`
	if fail {
		body += "echo 'boom: no numpy' >&2\nexit 3\n"
	} else {
		body += `mkdir -p "$out/tokenizer"
echo '{}' > "$out/tokenizer/vocab.json"
echo x > "$out/tokenizer/chat_template.jinja"
if [ "$tool" = iq_pack.py ]; then echo x > "$out/index.txt"; echo x > "$out/native_experts.txt"; fi
`
	}
	Expect(os.WriteFile(script, []byte(body), 0o755)).To(Succeed())
	return script, counter
}

func runs(counter string) int {
	b, _ := os.ReadFile(counter)
	return strings.Count(string(b), "run")
}

// packModel makes a model directory with the first shard and a Strata tree
// that has the tool scripts.
func packModel() (dir, root string) {
	dir = filepath.Join(GinkgoT().TempDir(), "model")
	Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(dir, "m-00001-of-00002.gguf"), []byte("x"), 0o644)).To(Succeed())
	root = GinkgoT().TempDir()
	Expect(os.MkdirAll(filepath.Join(root, "tools"), 0o755)).To(Succeed())
	for _, n := range []string{"iq_pack.py", "strata_tokenizer.py"} {
		Expect(os.WriteFile(filepath.Join(root, "tools", n), nil, 0o644)).To(Succeed())
	}
	return dir, root
}

func loadPackModel(s *Strata, dir, root string, opts ...string) error {
	exe, _ := os.Executable()
	s.newTokenizer = func(string, string, string, string, time.Duration) (Tokenizer, error) { return &fakeTokenizer{}, nil }
	s.startEngine = func(c engineConfig) (*engine, error) {
		c.Env = append(c.Env, "STRATA_STUB_MODE=ok")
		return startEngine(c)
	}
	return s.Load(&pb.ModelOptions{ModelFile: dir, ContextSize: 2048,
		Options: append([]string{"engine_path:" + exe, "strata_root:" + root, "ready_timeout:5s",
			"native:m-00001-of-00002.gguf"}, opts...)})
}

var _ = Describe("pack build", func() {
	It("builds the pack on the first load and never again", func() {
		dir, root := packModel()
		py, counter := fakePython(GinkgoT().TempDir(), false)
		s := NewStrata()
		DeferCleanup(func() { _ = s.Free() })
		Expect(loadPackModel(s, dir, root, "python:"+py, "pack_gguf:m-00001-of-00002.gguf", "pack_args:--compat-bf16")).To(Succeed())
		Expect(runs(counter)).To(Equal(1))
		Expect(filepath.Join(dir, "pack", "native_experts.txt")).To(BeAnExistingFile())
		Expect(dir + "/pack.partial").ToNot(BeADirectory())

		Expect(loadPackModel(s, dir, root, "python:"+py, "pack_gguf:m-00001-of-00002.gguf")).To(Succeed())
		Expect(runs(counter)).To(Equal(1), "a finished pack must not be built again")
	})

	It("passes the shard, the output and the extra arguments to the tool", func() {
		dir, root := packModel()
		var got packBuild
		s := NewStrata()
		s.buildPack = func(b packBuild) error {
			got = b
			return runPackBuildStub(b)
		}
		DeferCleanup(func() { _ = s.Free() })
		Expect(loadPackModel(s, dir, root, "pack_gguf:m-00001-of-00002.gguf", "pack_args:--compat-bf16 --x", "pack_timeout:5m")).To(Succeed())
		Expect(got.gguf).To(Equal(filepath.Join(dir, "m-00001-of-00002.gguf")))
		Expect(got.dest).To(Equal(filepath.Join(dir, "pack")))
		Expect(got.script).To(Equal(filepath.Join(root, "tools", "iq_pack.py")))
		Expect(got.args).To(Equal([]string{"--compat-bf16", "--x"}))
		Expect(got.timeout).To(Equal(5 * time.Minute))
		Expect(got.python).To(Equal("python3"))
	})

	It("reports a failing tool with its last lines and leaves no pack", func() {
		dir, root := packModel()
		py, _ := fakePython(GinkgoT().TempDir(), true)
		s := NewStrata()
		err := loadPackModel(s, dir, root, "python:"+py, "pack_gguf:m-00001-of-00002.gguf")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("boom: no numpy"))
		Expect(filepath.Join(dir, "pack")).ToNot(BeADirectory())
		Expect(filepath.Join(dir, "pack.partial")).ToNot(BeADirectory())
	})

	It("does not touch an unfinished pack it did not make", func() {
		dir, root := packModel()
		Expect(os.MkdirAll(filepath.Join(dir, "pack", "tokenizer"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "pack", "tokenizer", "vocab.json"), []byte("{}"), 0o644)).To(Succeed())
		py, counter := fakePython(GinkgoT().TempDir(), false)
		s := NewStrata()
		err := loadPackModel(s, dir, root, "python:"+py, "pack_gguf:m-00001-of-00002.gguf")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not a finished pack"))
		Expect(runs(counter)).To(Equal(0))
		Expect(filepath.Join(dir, "pack", "tokenizer", "vocab.json")).To(BeAnExistingFile())
	})

	It("is off with build_pack:false", func() {
		dir, root := packModel()
		py, counter := fakePython(GinkgoT().TempDir(), false)
		s := NewStrata()
		err := loadPackModel(s, dir, root, "python:"+py, "pack_gguf:m-00001-of-00002.gguf", "build_pack:false")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not a Strata pack directory"))
		Expect(runs(counter)).To(Equal(0))
	})

	It("names a missing tool and a missing GGUF", func() {
		dir, _ := packModel()
		s := NewStrata()
		err := loadPackModel(s, dir, GinkgoT().TempDir(), "pack_gguf:m-00001-of-00002.gguf")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("pack tool"))
		_, root := packModel()
		err = loadPackModel(NewStrata(), dir, root, "pack_gguf:nope.gguf")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("pack_gguf"))
	})

	It("rejects a bad tool name and the pack option beside pack_gguf", func() {
		dir, root := packModel()
		err := loadPackModel(NewStrata(), dir, root, "pack_gguf:m-00001-of-00002.gguf", "pack_tool:other")
		Expect(err).To(MatchError(ContainSubstring("pack_tool")))
		err = loadPackModel(NewStrata(), dir, root, "pack_gguf:m-00001-of-00002.gguf", "pack:x")
		Expect(err).To(MatchError(ContainSubstring("do not set the pack option")))
	})

	It("exports only the tokenizer and leaves --pack off the engine line", func() {
		dir, root := packModel()
		py, _ := fakePython(GinkgoT().TempDir(), false)
		s := NewStrata()
		var args []string
		DeferCleanup(func() { _ = s.Free() })
		exe, _ := os.Executable()
		s.newTokenizer = func(string, string, string, string, time.Duration) (Tokenizer, error) { return &fakeTokenizer{}, nil }
		s.startEngine = func(c engineConfig) (*engine, error) {
			args = c.Args
			c.Env = append(c.Env, "STRATA_STUB_MODE=ok")
			return startEngine(c)
		}
		Expect(s.Load(&pb.ModelOptions{ModelFile: dir, ContextSize: 1024, Options: []string{
			"engine_path:" + exe, "strata_root:" + root, "python:" + py, "ready_timeout:5s",
			"native:m-00001-of-00002.gguf", "pack_gguf:m-00001-of-00002.gguf", "pack_tool:tokenizer"}})).To(Succeed())
		Expect(strings.Join(args, " ")).To(Equal(fmt.Sprintf("--native %s --max-context 1024", filepath.Join(dir, "m-00001-of-00002.gguf"))))
		Expect(filepath.Join(dir, "pack", "tokenizer", "vocab.json")).To(BeAnExistingFile())
		Expect(filepath.Join(dir, "pack", "native_experts.txt")).ToNot(BeAnExistingFile())
	})
})

// runPackBuildStub makes a finished pack without running anything.
func runPackBuildStub(b packBuild) error {
	for _, m := range packMarkers(b.tool) {
		p := filepath.Join(b.dest, m)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			return err
		}
	}
	return nil
}
