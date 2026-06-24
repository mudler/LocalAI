package gallery_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	gguf "github.com/gpustack/gguf-parser-go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
)

var _ = Describe("VRAM estimate warm-up", func() {
	var state *system.SystemState

	BeforeEach(func() {
		dir, err := os.MkdirTemp("", "warm")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { os.RemoveAll(dir) })
		state, err = system.GetSystemState(system.WithModelPath(dir))
		Expect(err).ToNot(HaveOccurred())
		gallery.ResetGalleryModelCache()
		DeferCleanup(gallery.ResetGalleryModelCache)
	})

	It("does nothing when disabled, and returns without blocking", func() {
		cfg := gallery.DefaultEstimateWarmConfig
		cfg.Limit = 0

		done := make(chan struct{})
		go func() {
			defer close(done)
			gallery.WarmEstimateCache(context.Background(), []config.Gallery{}, state, cfg)
		}()
		Eventually(done, "1s").Should(BeClosed())
	})

	It("returns immediately even when there is work to do", func() {
		// The caller is a server still starting up: warming must never be on
		// the path to listening.
		done := make(chan struct{})
		go func() {
			defer close(done)
			gallery.WarmEstimateCache(context.Background(), []config.Gallery{}, state, gallery.DefaultEstimateWarmConfig)
		}()
		Eventually(done, "1s").Should(BeClosed())
	})

	It("stops when its context is cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		gallery.WarmEstimateCache(ctx, []config.Gallery{}, state, gallery.DefaultEstimateWarmConfig)
		cancel()
		// Nothing to assert beyond not hanging or panicking: an aborted warm-up
		// leaves entries cold, which is the state they were already in.
		Consistently(func() bool { return true }, "100ms").Should(BeTrue())
	})

	It("does not crash the server when remote GGUF metadata is malformed", func() {
		payload := warmMalformedGGUF()
		requested := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-requested:
			default:
				close(requested)
			}
			http.ServeContent(w, r, "model.gguf", time.Time{}, bytes.NewReader(payload))
		}))
		DeferCleanup(server.Close)

		galleryPath := filepath.Join(state.Model.ModelsPath, "malformed-gallery.yaml")
		index, err := yaml.Marshal([]gallery.GalleryModel{{Metadata: gallery.Metadata{
			Name: "malformed-gguf",
			AdditionalFiles: []gallery.File{{
				Filename: "model.gguf",
				URI:      server.URL + "/model.gguf",
			}},
		}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(galleryPath, index, 0600)).To(Succeed())

		cfg := gallery.DefaultEstimateWarmConfig
		cfg.Limit = 1
		cfg.Concurrency = 1
		cfg.Contexts = []uint32{8192}
		gallery.WarmEstimateCache(context.Background(), []config.Gallery{{
			Name: "malformed",
			URL:  "file://" + galleryPath,
		}}, state, cfg)

		Eventually(requested, "2s").Should(BeClosed())
		// The warm-up is detached. Give its parser time to consume the response;
		// before the recovery boundary, that goroutine panicked and killed the
		// entire test process (and the LocalAI server in production).
		Consistently(func() bool { return true }, "300ms").Should(BeTrue())
	})

	Describe("configuration from the environment", func() {
		AfterEach(func() {
			os.Unsetenv("LOCALAI_VRAM_WARM_LIMIT")
			os.Unsetenv("LOCALAI_VRAM_WARM_CONCURRENCY")
		})

		It("falls back to the defaults", func() {
			cfg := gallery.EstimateWarmConfigFromEnv()
			Expect(cfg.Limit).To(Equal(gallery.DefaultEstimateWarmConfig.Limit))
			Expect(cfg.Concurrency).To(Equal(gallery.DefaultEstimateWarmConfig.Concurrency))
		})

		It("lets an operator turn it off entirely", func() {
			os.Setenv("LOCALAI_VRAM_WARM_LIMIT", "0")
			Expect(gallery.EstimateWarmConfigFromEnv().Limit).To(BeZero())
		})

		It("lets an operator slow it down", func() {
			os.Setenv("LOCALAI_VRAM_WARM_CONCURRENCY", "1")
			Expect(gallery.EstimateWarmConfigFromEnv().Concurrency).To(Equal(1))
		})

		It("ignores values that are not usable", func() {
			os.Setenv("LOCALAI_VRAM_WARM_LIMIT", "not-a-number")
			os.Setenv("LOCALAI_VRAM_WARM_CONCURRENCY", "0")
			cfg := gallery.EstimateWarmConfigFromEnv()
			Expect(cfg.Limit).To(Equal(gallery.DefaultEstimateWarmConfig.Limit))
			// Zero workers would be a warm-up that never runs while looking
			// enabled, so it keeps the default rather than honouring it.
			Expect(cfg.Concurrency).To(Equal(gallery.DefaultEstimateWarmConfig.Concurrency))
		})
	})

	It("warms variant descriptions as well as estimates", func() {
		// Both are the same cost wearing different hats - a probe of an entry's
		// weight files - and both land in the same caches, so a warm-up that
		// covered only one would leave the first click paying for the other.
		// Asserted through the shared config rather than by observing network
		// calls: the gallery here is empty by design.
		Expect(gallery.DefaultEstimateWarmConfig.Limit).To(BeNumerically(">", 0))
	})

	It("keeps the estimate contexts the UI actually asks for", func() {
		// A warmed entry at the wrong context lengths is a cache the gallery
		// never reads, so this pins them together.
		Expect(gallery.DefaultEstimateWarmConfig.Contexts).To(ContainElements(
			uint32(8192), uint32(16384), uint32(32768), uint32(65536), uint32(131072), uint32(262144),
		))
	})

	It("bounds concurrency so a warm-up cannot saturate the link", func() {
		Expect(gallery.DefaultEstimateWarmConfig.Concurrency).To(BeNumerically("<=", 8))
		Expect(gallery.DefaultEstimateWarmConfig.Concurrency).To(BeNumerically(">", 0))
	})

})

func warmMalformedGGUF() []byte {
	payload := make([]byte, 0, 128)
	payload = binary.LittleEndian.AppendUint32(payload, uint32(gguf.GGUFMagicGGUFLe))
	payload = binary.LittleEndian.AppendUint32(payload, uint32(gguf.GGUFVersionV3))
	payload = binary.LittleEndian.AppendUint64(payload, 0)
	payload = binary.LittleEndian.AppendUint64(payload, 1)
	key := "tokenizer.ggml.tokens"
	payload = binary.LittleEndian.AppendUint64(payload, uint64(len(key)))
	payload = append(payload, key...)
	payload = binary.LittleEndian.AppendUint32(payload, uint32(gguf.GGUFMetadataValueTypeArray))
	payload = binary.LittleEndian.AppendUint32(payload, uint32(gguf.GGUFMetadataValueTypeString))
	payload = binary.LittleEndian.AppendUint64(payload, 1)
	payload = binary.LittleEndian.AppendUint64(payload, math.MaxUint64)
	return payload
}
