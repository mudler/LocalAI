package gallery_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

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

// artifacts: is a model-config key, so the installer only sees it inside
// overrides:. At the top level of an entry it is silently dropped, the
// installed config keeps a bare HF repo id as its model, and a backend that
// does not infer artifacts (vllm-cpp among them) fails the first load with
// "model path not found" while the install itself reported success.
var _ = Describe("gallery/index.yaml artifacts placement", func() {
	It("declares artifacts under overrides, never at the entry top level", func() {
		data, err := os.ReadFile(filepath.Join("..", "..", "gallery", "index.yaml"))
		Expect(err).ToNot(HaveOccurred())
		var raw []map[string]any
		Expect(yaml.Unmarshal(data, &raw)).To(Succeed())

		var misplaced []string
		for _, e := range raw {
			if _, ok := e["artifacts"]; ok {
				misplaced = append(misplaced, fmt.Sprint(e["name"]))
			}
		}
		Expect(misplaced).To(BeEmpty())
	})
})
