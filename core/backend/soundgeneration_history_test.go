package backend_test

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/audio"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("sound generation history", func() {
	It("preserves previous recordings after FLAC extension normalization", func() {
		rec := newRecordingBackend()
		loader := newRecordingLoader(rec)
		appCfg := config.NewApplicationConfig(config.WithSystemState(&system.SystemState{}))
		appCfg.GeneratedContentDir = GinkgoT().TempDir()
		cfg := identityModelCfg()

		generate := func(content []byte) string {
			rec.soundGenOutput = content
			path, _, err := backend.SoundGeneration(context.Background(), "music", nil, nil, nil, nil, nil, nil, "", "", nil, "", "", "", nil, nil, loader, appCfg, cfg)
			Expect(err).ToNot(HaveOccurred())
			// The endpoint detects the backend's format and renames its .wav
			// destination. Only the FLAC signature is needed for detection.
			finalPath, contentType := audio.NormalizeAudioFile(path)
			Expect(contentType).To(Equal("audio/flac"))
			Expect(filepath.Ext(finalPath)).To(Equal(".flac"))
			return finalPath
		}

		firstAudio := []byte("fLaC first recording fixture")
		secondAudio := []byte("fLaC second recording fixture")
		firstPath := generate(firstAudio)
		secondPath := generate(secondAudio)

		Expect(secondPath).ToNot(Equal(firstPath))
		first, err := os.ReadFile(firstPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(first).To(Equal(firstAudio))
		second, err := os.ReadFile(secondPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(second).To(Equal(secondAudio))
	})
})
