// SPDX-License-Identifier: MIT

package vram

import (
	"context"
	"errors"
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("VRAM probe error caching", func() {
	BeforeEach(func() {
		hfSizeCacheMu.Lock()
		hfSizeCacheData = make(map[string]hfSizeCacheEntry)
		hfSizeCacheMu.Unlock()
		SetGalleryGenerationFunc(nil)
	})

	AfterEach(func() {
		hfSizeCacheMu.Lock()
		hfSizeCacheData = make(map[string]hfSizeCacheEntry)
		hfSizeCacheMu.Unlock()
		SetGalleryGenerationFunc(nil)
	})

	DescribeTable("retries failed probes and caches the subsequent success", func(probeErr error) {
		ctx := context.Background()
		switch {
		case errors.Is(probeErr, context.Canceled):
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			ctx = canceled
		case errors.Is(probeErr, context.DeadlineExceeded):
			expired, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
			defer cancel()
			ctx = expired
		}

		By("retrying the Hugging Face repository lookup with a healthy context")
		fetches := 0
		fetchErr := probeErr
		fetch := func(ctx context.Context, _ string) (uint64, error) {
			fetches++
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			return 42, fetchErr
		}
		_, err := hfRepoWeightSizeWithFetcher(ctx, "cache-test/model", fetch)
		Expect(err).To(MatchError(probeErr))
		fetchErr = nil
		for range 2 {
			size, err := hfRepoWeightSizeWithFetcher(context.Background(), "cache-test/model", fetch)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(uint64(42)))
		}
		Expect(fetches).To(Equal(2))

		By("retrying the file size lookup with a healthy context")
		sizes := &countingSizeResolver{err: probeErr}
		resolver := newCachedSizeResolver(sizes, "", 0)
		_, err = resolver.ContentLength(ctx, "https://example.com/model.gguf")
		Expect(err).To(MatchError(probeErr))
		sizes.size, sizes.err = 42, nil
		for range 2 {
			size, err := resolver.ContentLength(context.Background(), "https://example.com/model.gguf")
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(int64(42)))
		}
		Expect(sizes.calls).To(Equal(2))

		By("retrying the GGUF metadata lookup with a healthy context")
		metadata := &countingGGUFReader{err: probeErr}
		reader := newCachedGGUFReader(metadata, "", 0)
		_, err = reader.ReadMetadata(ctx, "https://example.com/model.gguf")
		Expect(err).To(MatchError(probeErr))
		want := &GGUFMeta{BlockCount: 32, EmbeddingLength: 4096, HeadCount: 32, HeadCountKV: 8}
		metadata.meta, metadata.err = want, nil
		for range 2 {
			meta, err := reader.ReadMetadata(context.Background(), "https://example.com/model.gguf")
			Expect(err).NotTo(HaveOccurred())
			Expect(meta).To(Equal(want))
		}
		Expect(metadata.calls).To(Equal(2))
	},
		Entry("cancellation", context.Canceled),
		Entry("deadline exceeded", context.DeadlineExceeded),
		Entry("temporary network failure", &net.DNSError{Err: "temporary lookup failure", IsTemporary: true}),
		Entry("other probe failure", errors.New("probe failed")),
	)

	It("invalidates successful Hugging Face lookups when the gallery generation changes", func() {
		var generation uint64 = 1
		SetGalleryGenerationFunc(func() uint64 { return generation })
		fetches := 0
		fetch := func(context.Context, string) (uint64, error) {
			fetches++
			return uint64(fetches * 42), nil
		}
		for range 2 {
			size, err := hfRepoWeightSizeWithFetcher(context.Background(), "cache-test/model", fetch)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(uint64(42)))
		}
		generation++
		size, err := hfRepoWeightSizeWithFetcher(context.Background(), "cache-test/model", fetch)
		Expect(err).NotTo(HaveOccurred())
		Expect(size).To(Equal(uint64(84)))
		Expect(fetches).To(Equal(2))
	})
})
