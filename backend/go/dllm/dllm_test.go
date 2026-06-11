package main

import (
	"os"
	"sync"
	"testing"
	"unsafe"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDllm(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "dllm Backend Suite")
}

var (
	libLoadOnce sync.Once
	libLoadErr  error
)

// ensureLibLoaded mirrors main.go's bootstrap so a Go test can drive the
// C-ABI bridge without spinning up the gRPC server. The library path comes
// from DLLM_TEST_LIBRARY (gated specs Skip when it is unset).
func ensureLibLoaded() {
	libLoadOnce.Do(func() {
		libLoadErr = loadCAPI(os.Getenv("DLLM_TEST_LIBRARY"))
	})
}

// C-ABI binding smoke: drives the real libdllm.so against the tiny GGUF
// fixture from dllm.cpp (tests/fixtures/tiny_with_vocab.gguf). Gated on:
//
//	DLLM_TEST_LIBRARY   absolute path to libdllm.so
//	DLLM_TEST_TINY_MODEL absolute path to tiny_with_vocab.gguf
var _ = Describe("C-ABI binding", func() {
	BeforeEach(func() {
		if os.Getenv("DLLM_TEST_LIBRARY") == "" || os.Getenv("DLLM_TEST_TINY_MODEL") == "" {
			Skip("set DLLM_TEST_LIBRARY and DLLM_TEST_TINY_MODEL to run the C-ABI binding smoke")
		}
		ensureLibLoaded()
		Expect(libLoadErr).ToNot(HaveOccurred())
	})

	It("binds the 9 symbols and round-trips the tiny model", func() {
		Expect(cAbiVersion()).To(Equal(int32(1)))

		h := cLoad(os.Getenv("DLLM_TEST_TINY_MODEL"), "{}")
		Expect(h).ToNot(BeZero(), "dllm_capi_load of the tiny fixture")

		// Tiny fixture vocab: "hello" tokenizes to ids [2,18] (bos prepended
		// by the C side: vocab.add_bos).
		toks, err := cTokenizeJSON(h, "hello")
		Expect(err).ToNot(HaveOccurred())
		Expect(toks).To(Equal("[2,18]"))

		// Deterministic generation: an explicit non-negative seed seeds
		// mt19937, so two identical calls must produce identical text.
		out1, err := cGenerate(h, "hello", `{"n_predict":16,"seed":7}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(out1).ToNot(BeEmpty())
		// Cancel with no call in flight is dropped: each generate resets the
		// cancel flag on entry (header contract), so this must not affect
		// the next call. Also binds the 9th symbol; safe on NULL too.
		cCancel(h)
		cCancel(0)

		out2, err := cGenerate(h, "hello", `{"n_predict":16,"seed":7}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(out2).To(Equal(out1))

		// Streaming variant: same opts, blocks arrive via the purego
		// callback trampoline. The per-block detokenize can differ from the
		// seamless full-text decode at block boundaries, so only assert that
		// blocks arrived and were non-trivial, not byte equality with out1.
		var blocks []string
		var steps int
		err = cGenerateStream(h, "hello", `{"n_predict":16,"seed":7}`,
			func(text string) { blocks = append(blocks, text) },
			func(step, total int, preview string) { steps++ },
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(blocks).ToNot(BeEmpty())
		Expect(steps).To(BeNumerically(">", 0))

		// Load failure path: NULL ctx back, and last_error(NULL) returns the
		// static NULL-ctx message (there is no ctx to carry the real reason).
		bad := cLoad("/nonexistent/dllm-model.gguf", "{}")
		Expect(bad).To(BeZero())
		Expect(cLastError(0)).ToNot(BeEmpty())

		// Free is safe on a live handle and a NULL one (delete nullptr).
		cFree(h)
		cFree(0)
	})
})

// Ungated specs for the pure-Go helpers (no libdllm.so required).
var _ = Describe("buildOptsJSON", func() {
	It("renders flat scalars as a JSON object", func() {
		out, err := buildOptsJSON(map[string]any{
			"n_predict": 16,
			"seed":      int64(7),
			"eb_t_min":  0.5,
			"kv_cache":  "auto",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(MatchJSON(`{"n_predict":16,"seed":7,"eb_t_min":0.5,"kv_cache":"auto"}`))
	})

	It("renders an empty object for no options", func() {
		out, err := buildOptsJSON(nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(Equal("{}"))
	})

	It("rejects nested objects (the C-side scanner only reads flat scalars)", func() {
		_, err := buildOptsJSON(map[string]any{"sampler": map[string]any{"seed": 1}})
		Expect(err).To(HaveOccurred())
	})

	It("rejects arrays", func() {
		_, err := buildOptsJSON(map[string]any{"stop": []string{"a"}})
		Expect(err).To(HaveOccurred())
	})

	It("rejects booleans (the C-side scanner only understands numbers and strings)", func() {
		_, err := buildOptsJSON(map[string]any{"flag": true})
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("goStringFromCPtr", func() {
	It("copies a NUL-terminated buffer", func() {
		buf := []byte("dllm\x00")
		s := goStringFromCPtr(uintptr(unsafe.Pointer(&buf[0])))
		Expect(s).To(Equal("dllm"))
	})

	It("returns the empty string for NULL", func() {
		Expect(goStringFromCPtr(0)).To(Equal(""))
	})
})
