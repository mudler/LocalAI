// SPDX-License-Identifier: MIT
package embeddingmedia_test

import (
	"math"
	"reflect"
	"testing"

	"github.com/mudler/LocalAI/pkg/embeddingmedia"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEmbeddingMedia(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Embedding media contracts")
}

var _ = Describe("Multimodal contracts", func() {
	Describe("budget boundaries", func() {
		It("defines every aggregate limit in explicit units", func() {
			Expect(embeddingmedia.NewBudget()).To(Equal(embeddingmedia.Budget{
				MediaBytes: 32 << 20, TextBytes: 1 << 20, Items: 16, SourceParts: 128,
				Frames: 120, Pixels: 64_000_000, AudioBytes: 64 << 20,
				DurationMilliseconds: 60_000, PreparationMilliseconds: 60_000, TemporaryBytes: 256 << 20,
			}))
		})
		for _, field := range []string{"MediaBytes", "TextBytes", "Items", "SourceParts", "Frames", "Pixels", "AudioBytes", "DurationMilliseconds", "PreparationMilliseconds", "TemporaryBytes"} {
			It("rejects overflow without mutation and accepts boundary ±1 for "+field, func() {
				initial := embeddingmedia.NewBudget()
				limit := reflect.ValueOf(initial).FieldByName(field).Uint()
				for _, amount := range []uint64{limit - 1, limit, limit + 1, math.MaxUint64} {
					b := initial
					c := embeddingmedia.Consumption{}
					reflect.ValueOf(&c).Elem().FieldByName(field).SetUint(amount)
					err := b.Consume(c)
					if amount > limit {
						Expect(err).To(MatchError(&embeddingmedia.Error{Status: 413, Code: "embedding_resource_limit"}))
						Expect(b).To(Equal(initial))
					} else {
						Expect(err).NotTo(HaveOccurred())
						Expect(reflect.ValueOf(b).FieldByName(field).Uint()).To(Equal(limit - amount))
					}
				}
			})
		}
		It("never resets exhausted or zero budgets and validates atomically", func() {
			b := embeddingmedia.NewBudget()
			Expect(b.Consume(embeddingmedia.Consumption(b))).To(Succeed())
			Expect(b).To(Equal(embeddingmedia.Budget{}))
			Expect(b.Consume(embeddingmedia.Consumption{Items: 1})).To(HaveOccurred())
			b = embeddingmedia.NewBudget()
			original := b
			Expect(b.Consume(embeddingmedia.Consumption{Items: 1, TemporaryBytes: math.MaxUint64})).To(HaveOccurred())
			Expect(b).To(Equal(original))
		})
	})
})

var _ = Describe("Multimodal contracts", func() {
	It("fixes nonaggregate version 1 limits in explicit units", func() {
		Expect(embeddingmedia.MaxBodyBytes).To(Equal(uint64(48 << 20)))
		Expect(embeddingmedia.MaxPartDurationMilliseconds).To(Equal(uint64(30_000)))
		Expect(embeddingmedia.MaxImagePixels).To(Equal(uint64(16_000_000)))
		Expect(embeddingmedia.MaxAudioChannels).To(Equal(uint64(2)))
		Expect(embeddingmedia.MaxAudioSampleRateHz).To(Equal(uint64(192_000)))
		Expect(embeddingmedia.MaxFetchMilliseconds).To(Equal(uint64(30_000)))
		Expect(embeddingmedia.MaxConcurrentPreparations).To(Equal(uint64(2)))
	})
})

var _ = Describe("Multimodal contracts", func() {
	for name, limit := range map[string]uint64{
		"body bytes":                 embeddingmedia.MaxBodyBytes,
		"part duration milliseconds": embeddingmedia.MaxPartDurationMilliseconds,
		"image pixels":               embeddingmedia.MaxImagePixels,
		"audio channels":             embeddingmedia.MaxAudioChannels,
		"audio sample rate Hz":       embeddingmedia.MaxAudioSampleRateHz,
		"fetch milliseconds":         embeddingmedia.MaxFetchMilliseconds,
		"preparation slots":          embeddingmedia.MaxConcurrentPreparations,
	} {
		It("checks nonaggregate boundary ±1 for "+name, func() {
			for _, n := range []uint64{limit - 1, limit, limit + 1, math.MaxUint64} {
				err := embeddingmedia.CheckLimit(n, limit)
				if n > limit {
					Expect(err).To(HaveOccurred())
				} else {
					Expect(err).NotTo(HaveOccurred())
				}
			}
		})
	}
})
