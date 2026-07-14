package modelartifacts_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

var _ = Describe("artifact storage primitives", func() {
	baseSpec := func() modelartifacts.Spec {
		return modelartifacts.Spec{
			Name:   "model",
			Target: "model",
			Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo", Revision: "main"},
			Resolved: &modelartifacts.Resolved{
				Endpoint: "https://huggingface.co",
				Revision: "0123456789abcdef0123456789abcdef01234567",
			},
		}
	}

	It("creates a stable path-safe cache identity", func() {
		first, err := modelartifacts.CacheKey(baseSpec())
		Expect(err).NotTo(HaveOccurred())
		second := baseSpec()
		second.Source.AllowPatterns = []string{"*.json", "*.safetensors"}
		third := second
		third.Source.AllowPatterns = []string{"*.safetensors", "*.json"}
		secondKey, err := modelartifacts.CacheKey(second)
		Expect(err).NotTo(HaveOccurred())
		thirdKey, err := modelartifacts.CacheKey(third)
		Expect(err).NotTo(HaveOccurred())
		Expect(first).To(MatchRegexp(`^[0-9a-f]{64}$`))
		Expect(secondKey).To(Equal(thirdKey))
		Expect(secondKey).NotTo(Equal(first))
	})

	It("places all state below the models artifact root", func() {
		spec := baseSpec()
		key, err := modelartifacts.CacheKey(spec)
		Expect(err).NotTo(HaveOccurred())
		spec.Resolved.CacheKey = key
		layout, err := modelartifacts.LayoutFor("/models", spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(layout.Final).To(Equal(filepath.Join("/models", ".artifacts", "huggingface", key)))
		Expect(layout.Snapshot).To(Equal(filepath.Join(layout.Final, "snapshot")))
		relative, err := modelartifacts.RelativeSnapshotPath(key)
		Expect(err).NotTo(HaveOccurred())
		Expect(relative).To(Equal(filepath.Join(".artifacts", "huggingface", key, "snapshot")))
		_, err = modelartifacts.RelativeSnapshotPath("sha256:" + key)
		Expect(err).To(MatchError(ContainSubstring("invalid artifact cache key")))
	})

	DescribeTable("rejects hostile Hub paths",
		func(candidate string) { Expect(modelartifacts.ValidateRelativeHubPath(candidate)).NotTo(Succeed()) },
		Entry("absolute", "/etc/passwd"),
		Entry("parent", "nested/../escape"),
		Entry("backslash", `nested\escape`),
		Entry("NUL", "bad\x00path"),
		Entry("empty component", "nested//file"),
	)

	It("reads a complete versioned manifest", func() {
		spec := baseSpec()
		key, err := modelartifacts.CacheKey(spec)
		Expect(err).NotTo(HaveOccurred())
		spec.Resolved.CacheKey = key
		encoded, err := json.Marshal(modelartifacts.Manifest{
			Version:  modelartifacts.ManifestVersion,
			Artifact: spec,
			Files: []modelartifacts.ManifestFile{{
				Path:   "nested/model.safetensors",
				Size:   11,
				SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			}},
		})
		Expect(err).NotTo(HaveOccurred())
		fileName := filepath.Join(GinkgoT().TempDir(), "manifest.json")
		Expect(os.WriteFile(fileName, encoded, 0o600)).To(Succeed())

		manifest, err := modelartifacts.ReadManifest(fileName)
		Expect(err).NotTo(HaveOccurred())
		Expect(manifest.Artifact.Resolved.CacheKey).To(Equal(key))
		Expect(manifest.Files).To(HaveLen(1))
	})

	It("reports progress only through the request-scoped sink", func() {
		event := modelartifacts.ProgressEvent{
			Phase:        modelartifacts.PhaseDownloading,
			Artifact:     "model",
			File:         "weights.safetensors",
			CurrentBytes: 7,
			TotalBytes:   11,
		}
		var received []modelartifacts.ProgressEvent
		ctx := modelartifacts.WithProgressSink(context.Background(), func(update modelartifacts.ProgressEvent) {
			received = append(received, update)
		})
		modelartifacts.ReportProgress(ctx, event)
		modelartifacts.ReportProgress(context.Background(), event)
		Expect(received).To(Equal([]modelartifacts.ProgressEvent{event}))
	})
})
