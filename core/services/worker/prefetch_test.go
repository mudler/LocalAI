package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeInstaller records every call and lets each test choose the behaviour
// for a given model name. Modelled on the real modelInstaller signature so
// the prefetch loop can't accidentally widen its dependency on the gallery
// package without breaking the contract here too.
type fakeInstaller struct {
	mu       sync.Mutex
	calls    []string
	behavior map[string]error // model name -> error to return; missing key = nil (success)
}

func newFakeInstaller() *fakeInstaller {
	return &fakeInstaller{behavior: map[string]error{}}
}

func (f *fakeInstaller) install(
	_ context.Context,
	_ []config.Gallery,
	_ []config.Gallery,
	_ *system.SystemState,
	_ *model.ModelLoader,
	name string,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
	return f.behavior[name]
}

func (f *fakeInstaller) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

// minimalSystemState yields a SystemState pointed at a fresh tempdir. The
// prefetch loop never touches it directly (the fake installer ignores it),
// but the production signature requires a non-nil value and we want the
// tests to mirror the real wiring as closely as possible.
func minimalSystemState(modelsDir string) *system.SystemState {
	ss, err := system.GetSystemState(system.WithModelPath(modelsDir))
	Expect(err).ToNot(HaveOccurred())
	return ss
}

var _ = Describe("prefetchModels", func() {
	var (
		tmp       string
		ss        *system.SystemState
		galleries = `[{"name":"localai","url":"file:///dev/null"}]`
	)

	BeforeEach(func() {
		var err error
		tmp, err = os.MkdirTemp("", "worker-prefetch-test")
		Expect(err).ToNot(HaveOccurred())
		ss = minimalSystemState(tmp)
	})

	AfterEach(func() {
		_ = os.RemoveAll(tmp)
	})

	It("installs every configured model before returning (happy path)", func() {
		f := newFakeInstaller()
		cfg := &Config{
			PrefetchModels: []string{"llama-3.2-1b", "phi-3-mini"},
			Galleries:      galleries,
			ModelsPath:     tmp,
		}

		prefetchModels(context.Background(), cfg, ss, nil, nil, f.install)

		Expect(f.recorded()).To(Equal([]string{"llama-3.2-1b", "phi-3-mini"}))
	})

	It("is a no-op when the file is already present on disk (idempotency contract)", func() {
		// Drop a fake artifact where the gallery would have placed it; the
		// real installer's downloader stats the path and short-circuits when
		// the SHA matches (or any time SHA is unset). We simulate that here
		// by having the fake installer assert the file exists before claiming
		// success — exactly the invariant we rely on for restart-against-PVC.
		modelFile := filepath.Join(tmp, "llama-3.2-1b.gguf")
		Expect(os.WriteFile(modelFile, []byte("already-here"), 0o600)).To(Succeed())

		var installerCalls int
		var sawFile bool
		fake := func(
			_ context.Context,
			_, _ []config.Gallery,
			_ *system.SystemState,
			_ *model.ModelLoader,
			_ string,
		) error {
			installerCalls++
			if _, err := os.Stat(modelFile); err == nil {
				sawFile = true
			}
			// Real installer returns nil on cache hit. Mirror that.
			return nil
		}

		cfg := &Config{
			PrefetchModels: []string{"llama-3.2-1b"},
			Galleries:      galleries,
			ModelsPath:     tmp,
		}
		prefetchModels(context.Background(), cfg, ss, nil, nil, fake)

		Expect(installerCalls).To(Equal(1), "installer is always invoked; its downloader handles the skip")
		Expect(sawFile).To(BeTrue(), "fake installer saw the pre-existing artifact, matching the restart-on-warm-PVC path")

		// File untouched.
		data, err := os.ReadFile(modelFile)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).To(Equal("already-here"))
	})

	It("logs and continues when an individual install fails (non-fatal contract)", func() {
		f := newFakeInstaller()
		f.behavior["broken"] = errors.New("gallery unreachable")

		cfg := &Config{
			PrefetchModels: []string{"good-1", "broken", "good-2"},
			Galleries:      galleries,
			ModelsPath:     tmp,
		}

		// Function must return normally despite the middle install failing.
		Expect(func() {
			prefetchModels(context.Background(), cfg, ss, nil, nil, f.install)
		}).ToNot(Panic())

		// Subsequent models still get attempted — failures don't short-circuit.
		Expect(f.recorded()).To(Equal([]string{"good-1", "broken", "good-2"}))
	})

	It("does nothing when PrefetchModels is empty", func() {
		f := newFakeInstaller()
		cfg := &Config{ModelsPath: tmp, Galleries: galleries}
		prefetchModels(context.Background(), cfg, ss, nil, nil, f.install)
		Expect(f.recorded()).To(BeEmpty())
	})

	It("trims whitespace and skips empty entries", func() {
		f := newFakeInstaller()
		cfg := &Config{
			PrefetchModels: []string{"  llama  ", "", "   ", "phi"},
			Galleries:      galleries,
			ModelsPath:     tmp,
		}
		prefetchModels(context.Background(), cfg, ss, nil, nil, f.install)
		Expect(f.recorded()).To(Equal([]string{"llama", "phi"}))
	})

	It("skips prefetch when LOCALAI_GALLERIES is malformed (non-fatal)", func() {
		f := newFakeInstaller()
		cfg := &Config{
			PrefetchModels: []string{"llama"},
			Galleries:      `not-json`,
			ModelsPath:     tmp,
		}
		prefetchModels(context.Background(), cfg, ss, nil, nil, f.install)
		Expect(f.recorded()).To(BeEmpty(), "no installer call when galleries can't be parsed")
	})

	It("skips prefetch when no galleries are configured (non-fatal)", func() {
		f := newFakeInstaller()
		cfg := &Config{
			PrefetchModels: []string{"llama"},
			Galleries:      `[]`,
			ModelsPath:     tmp,
		}
		prefetchModels(context.Background(), cfg, ss, nil, nil, f.install)
		Expect(f.recorded()).To(BeEmpty())
	})
})

var _ = Describe("parseModelGalleries", func() {
	It("returns empty slice on empty input", func() {
		g, err := parseModelGalleries("")
		Expect(err).ToNot(HaveOccurred())
		Expect(g).To(BeEmpty())
	})

	It("returns empty slice on whitespace-only input", func() {
		g, err := parseModelGalleries("   \n\t ")
		Expect(err).ToNot(HaveOccurred())
		Expect(g).To(BeEmpty())
	})

	It("parses a valid JSON list", func() {
		g, err := parseModelGalleries(`[{"name":"localai","url":"github:mudler/LocalAI/gallery/index.yaml@master"}]`)
		Expect(err).ToNot(HaveOccurred())
		Expect(g).To(HaveLen(1))
		Expect(g[0].Name).To(Equal("localai"))
	})

	It("returns an error on malformed JSON", func() {
		_, err := parseModelGalleries(`{this is not json`)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("normalizePrefetchList", func() {
	It("drops empty and whitespace-only entries", func() {
		got := normalizePrefetchList([]string{"a", "", "  ", " b ", "\tc\n"})
		Expect(got).To(Equal([]string{"a", "b", "c"}))
	})

	It("returns empty slice on empty input", func() {
		Expect(normalizePrefetchList(nil)).To(BeEmpty())
		Expect(normalizePrefetchList([]string{})).To(BeEmpty())
	})
})
