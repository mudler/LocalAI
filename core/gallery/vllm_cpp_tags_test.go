package gallery_test

import (
	"fmt"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

// A gallery tag that names a capability is what users filter on, and
// known_usecases is what the server routes on. When they disagree, the entry
// is listed under a filter it cannot serve, or is hidden from one it can.
var _ = Describe("gallery/index.yaml vllm-cpp capability tags", func() {
	It("keeps capability tags and known_usecases in agreement", func() {
		entries, err := loadGalleryIndex()
		Expect(err).ToNot(HaveOccurred())

		tagToFlag := map[string]config.ModelConfigUsecase{
			"decisions":      config.FLAG_DECISIONS,
			"vision":         config.FLAG_VISION,
			"token-classify": config.FLAG_TOKEN_CLASSIFY,
			"scoring":        config.FLAG_SCORE,
		}

		var violations []string
		seen := 0
		for i := range entries {
			e := &entries[i]
			if backend, _ := e.Overrides["backend"].(string); backend != "vllm-cpp" {
				continue
			}
			seen++
			declared := e.GetKnownUsecases()
			for tag, flag := range tagToFlag {
				tagged := slices.Contains(e.Tags, tag)
				has := declared != nil && *declared&flag == flag
				if tagged != has {
					violations = append(violations, fmt.Sprintf("%s: tag %q present=%v but known_usecases declares it=%v", e.Name, tag, tagged, has))
				}
			}
		}
		Expect(seen).To(BeNumerically(">", 0))
		Expect(violations).To(BeEmpty())
	})
})
