package downloader

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"syscall"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/mudler/LocalAI/pkg/oci"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("linkOrCopyTree", func() {
	It("hard-links a directory", func() {
		src := GinkgoT().TempDir()
		dst := filepath.Join(GinkgoT().TempDir(), "out")
		Expect(os.MkdirAll(filepath.Join(src, "sub"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(src, "config.json"), []byte("{}"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(src, "sub", "model.safetensors"), []byte("w"), 0o644)).To(Succeed())

		Expect(linkOrCopyTree(src, dst)).To(Succeed())

		got, err := os.ReadFile(filepath.Join(dst, "sub", "model.safetensors"))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(got)).To(Equal("w"))

		// A model shared with llmman's store should cost its bytes once.
		var a, b syscall.Stat_t
		Expect(syscall.Stat(filepath.Join(src, "config.json"), &a)).To(Succeed())
		Expect(syscall.Stat(filepath.Join(dst, "config.json"), &b)).To(Succeed())
		Expect(b.Ino).To(Equal(a.Ino))
	})

	It("handles a single-file payload", func() {
		// A GGUF payload resolves to the file itself, not a directory.
		src := filepath.Join(GinkgoT().TempDir(), "model.gguf")
		Expect(os.WriteFile(src, []byte("gguf"), 0o644)).To(Succeed())
		dst := filepath.Join(GinkgoT().TempDir(), "nested", "model.gguf")

		Expect(linkOrCopyTree(src, dst)).To(Succeed())

		got, err := os.ReadFile(dst)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(got)).To(Equal("gguf"))
	})

	It("overwrites an existing file", func() {
		src := filepath.Join(GinkgoT().TempDir(), "model.gguf")
		Expect(os.WriteFile(src, []byte("new"), 0o644)).To(Succeed())
		dst := filepath.Join(GinkgoT().TempDir(), "model.gguf")
		Expect(os.WriteFile(dst, []byte("stale"), 0o644)).To(Succeed())

		Expect(linkOrCopyTree(src, dst)).To(Succeed())

		got, err := os.ReadFile(dst)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(got)).To(Equal("new"))
	})

	It("reports a missing source", func() {
		Expect(linkOrCopyTree(filepath.Join(GinkgoT().TempDir(), "gone"), GinkgoT().TempDir())).ToNot(Succeed())
	})
})

var _ = Describe("modelPackProgress", func() {
	type call struct {
		file, written, total string
		pct                  float64
	}

	report := func(completed, total int64) call {
		var got call
		modelPackProgress("ref", func(file, written, size string, pct float64) {
			got = call{file, written, size, pct}
		})("pulling", completed, total)
		return got
	}

	It("fills the written, total and percentage slots", func() {
		Expect(report(512, 1024)).To(Equal(call{"ref", "512 B", "1.0 KiB", 50}))
	})

	It("leaves total and percentage empty when the daemon reports no size", func() {
		Expect(report(0, 0)).To(Equal(call{"ref", "0 B", "", 0}))
	})

	It("tolerates a nil callback", func() {
		Expect(func() { modelPackProgress("ref", nil)("pulling", 1, 2) }).ToNot(Panic())
	})
})

// recordingVerifier remembers the reference it was asked to verify.
type recordingVerifier struct{ refs []string }

func (v *recordingVerifier) VerifyImage(_ context.Context, ref string) error {
	v.refs = append(v.refs, ref)
	return nil
}

var _ = Describe("ModelPack acquisition", func() {
	var (
		host      string
		digest    string
		modelDir  string
		pulled    []string
		resolved  string
		verifier  *recordingVerifier
		fetchInto string
	)

	// pushModelPack publishes a manifest whose config mediaType marks it as a
	// ModelPack artifact and returns its digest.
	pushModelPack := func(repo string) {
		reg := httptest.NewServer(registry.New())
		DeferCleanup(reg.Close)
		u, err := url.Parse(reg.URL)
		Expect(err).ToNot(HaveOccurred())
		host = u.Host

		img := mutate.ConfigMediaType(empty.Image, types.MediaType(oci.ModelPackConfigMediaType))
		ref, err := name.ParseReference(host + "/" + repo + ":v1")
		Expect(err).ToNot(HaveOccurred())
		Expect(remote.Write(ref, img)).To(Succeed())
		d, err := img.Digest()
		Expect(err).ToNot(HaveOccurred())
		digest = d.String()
	}

	BeforeEach(func() {
		pulled, verifier = nil, &recordingVerifier{}

		daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/version":
				_ = json.NewEncoder(w).Encode(map[string]any{"version": "0.1.0"})
			case "/api/pull":
				var req map[string]string
				_ = json.NewDecoder(r.Body).Decode(&req)
				pulled = append(pulled, req["model"])
				_, _ = w.Write([]byte("{\"status\":\"success\"}\n"))
			}
		}))
		DeferCleanup(daemon.Close)
		GinkgoT().Setenv("LLMMAN_HOST", daemon.URL)

		// Stands in for `llmman resolve --no-pull`, recording what it was asked for.
		modelDir = GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(modelDir, "model.gguf"), []byte("gguf"), 0o644)).To(Succeed())
		resolved = filepath.Join(GinkgoT().TempDir(), "resolved-ref")
		script := filepath.Join(GinkgoT().TempDir(), "llmman")
		Expect(os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' \"$3\" > "+resolved+"\nprintf '{\"path\":\""+modelDir+"\"}\\n'\n"), 0o755)).To(Succeed())
		GinkgoT().Setenv("LOCALAI_LLMMAN_BIN", script)

		fetchInto = filepath.Join(GinkgoT().TempDir(), "models")
	})

	It("hands the digest-pinned reference to llmman, not the tag", func() {
		pushModelPack("org/model")

		err := URI("oci://"+host+"/org/model:v1").DownloadFileWithContext(
			context.Background(), fetchInto, "", 1, 1, nil, WithImageVerifier(verifier))
		Expect(err).ToNot(HaveOccurred())

		pinned := host + "/org/model@" + digest
		Expect(verifier.refs).To(Equal([]string{pinned}))
		Expect(pulled).To(Equal([]string{pinned}))
		got, err := os.ReadFile(resolved)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(got)).To(Equal(pinned))
		Expect(filepath.Join(fetchInto, "model.gguf")).To(BeARegularFile())
	})

	DescribeTable("installs a single-file ModelPack without replacing its destination directory",
		func(directoryTarget bool) {
			pushModelPack("org/model")
			src := filepath.Join(modelDir, "model.gguf")
			script := filepath.Join(GinkgoT().TempDir(), "llmman")
			output, err := json.Marshal(map[string]string{"path": src})
			Expect(err).ToNot(HaveOccurred())
			Expect(os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' '"+string(output)+"'\n"), 0o755)).To(Succeed())
			GinkgoT().Setenv("LOCALAI_LLMMAN_BIN", script)

			dir := GinkgoT().TempDir()
			sentinel := filepath.Join(dir, "existing-model")
			Expect(os.WriteFile(sentinel, []byte("keep"), 0o644)).To(Succeed())
			dst := filepath.Join(dir, "renamed.gguf")
			want := dst
			if directoryTarget {
				dst, want = dir, filepath.Join(dir, "model.gguf")
			}
			Expect(URI("oci://"+host+"/org/model:v1").DownloadFileWithContext(
				context.Background(), dst, "", 1, 1, nil)).To(Succeed())
			got, err := os.ReadFile(want)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(got)).To(Equal("gguf"))
			Expect(sentinel).To(BeARegularFile())
		},
		Entry("explicit filename", false),
		Entry("existing directory", true),
	)

	It("pins the reference without a verifier too", func() {
		pushModelPack("org/model")

		Expect(URI("oci://"+host+"/org/model:v1").DownloadFileWithContext(
			context.Background(), fetchInto, "", 1, 1, nil)).To(Succeed())

		Expect(pulled).To(Equal([]string{host + "/org/model@" + digest}))
	})
})
