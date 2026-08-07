// SPDX-License-Identifier: MIT

package importers_test

import (
	"context"
	"encoding/json"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
)

type fixtureMetadata struct {
	details *hfapi.ModelDetails
	err     error
	calls   []string
}

func (f *fixtureMetadata) GetModelDetails(repo string) (*hfapi.ModelDetails, error) {
	f.calls = append(f.calls, repo)
	return f.details, f.err
}

var _ = Describe("DiscoverModelConfigWithOptions", func() {
	It("uses fixture metadata without creating a live client", func() {
		metadata := &fixtureMetadata{details: &hfapi.ModelDetails{
			ModelID:     "fixture/whisper",
			PipelineTag: "automatic-speech-recognition",
			Files:       []hfapi.ModelFile{{Path: "ggml-model.bin"}},
		}}
		config, err := importers.DiscoverModelConfigWithOptions(context.Background(), "hf://fixture/whisper", json.RawMessage(`{}`), importers.DiscoverOptions{HuggingFace: metadata})
		Expect(err).NotTo(HaveOccurred())
		Expect(config.Name).NotTo(BeEmpty())
		Expect(metadata.calls).To(Equal([]string{"fixture/whisper"}))
	})

	It("does not invoke metadata after cancellation", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		metadata := &fixtureMetadata{err: errors.New("must not be returned")}
		_, err := importers.DiscoverModelConfigWithOptions(ctx, "hf://fixture/model", json.RawMessage(`{}`), importers.DiscoverOptions{HuggingFace: metadata})
		Expect(err).To(MatchError(context.Canceled))
		Expect(metadata.calls).To(BeEmpty())
	})
})
