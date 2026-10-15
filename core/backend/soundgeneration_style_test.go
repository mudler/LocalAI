package backend_test

import (
	"context"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("sound generation caption style", func() {
	DescribeTable("prepares audio.cpp style without changing caller parameters",
		func(engine, caption string, params, want map[string]string) {
			rec := newRecordingBackend()
			loader := newRecordingLoader(rec)
			cfg := identityModelCfg()
			cfg.Backend = engine
			appCfg := config.NewApplicationConfig(config.WithSystemState(&system.SystemState{}))
			appCfg.GeneratedContentDir = GinkgoT().TempDir()
			original := map[string]string{}
			for k, v := range params {
				original[k] = v
			}

			_, _, err := backend.SoundGeneration(context.Background(), "", nil, nil, nil, nil, nil, nil,
				caption, "[Verse]\nA quiet piano", nil, "", "", "", nil, params, loader, appCfg, cfg)
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.soundGenParams).To(Equal(want))
			if params != nil {
				Expect(params).To(Equal(original))
			}
		},
		Entry("Studio caption supplies the missing YuE2 style", "audio-cpp", "piano pop", nil,
			map[string]string{"style": "piano pop"}),
		Entry("keeps other options", "audio-cpp", "piano pop", map[string]string{"cot": "off"},
			map[string]string{"cot": "off", "style": "piano pop"}),
		Entry("keeps an explicit style", "audio-cpp", "piano pop", map[string]string{"style": "jazz"},
			map[string]string{"style": "jazz"}),
		Entry("keeps an explicitly empty style", "audio-cpp", "piano pop", map[string]string{"style": ""},
			map[string]string{"style": ""}),
		Entry("does not invent a style without a caption", "audio-cpp", "", nil, nil),
		Entry("does not add audio.cpp options to another backend", "ace-step", "piano pop", nil, nil),
	)
})
