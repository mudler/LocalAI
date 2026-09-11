package galleryop_test

import (
	"github.com/mudler/LocalAI/core/services/galleryop"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("JSON model preloads", func() {
	It("identifies PRELOAD_MODELS when the value is not a JSON array", func() {
		err := galleryop.ApplyGalleryFromString(nil, nil, false, false, nil, nil, "true", false)

		Expect(err).To(MatchError(And(
			ContainSubstring("PRELOAD_MODELS/--preload-models"),
			ContainSubstring("expected a JSON array"),
		)))
	})

	It("rejects a null PRELOAD_MODELS value", func() {
		err := galleryop.ApplyGalleryFromString(nil, nil, false, false, nil, nil, "null", false)

		Expect(err).To(MatchError(And(
			ContainSubstring("PRELOAD_MODELS/--preload-models"),
			ContainSubstring("expected a JSON array"),
		)))
	})

	It("accepts an empty JSON array", func() {
		Expect(galleryop.ApplyGalleryFromString(nil, nil, false, false, nil, nil, "[]", false)).To(Succeed())
	})
})
