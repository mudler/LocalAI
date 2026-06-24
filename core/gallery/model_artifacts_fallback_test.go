package gallery_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
)

// Install-time degradation has the same consequence as preload-time
// degradation: a managed backend that never gets its snapshot downloads the
// repo itself on every load. See #10981.
var _ = Describe("install artifact fallback severity", func() {
	var captured *bytes.Buffer

	BeforeEach(func() {
		// Capture at error level so a warn emission is filtered out and the
		// assertion fails on severity, not on wording.
		captured = &bytes.Buffer{}
		handler := slog.NewTextHandler(captured, &slog.HandlerOptions{Level: slog.LevelError})
		xlog.SetLogger(xlog.NewLoggerWithHandler(handler, xlog.LogLevelError))
	})

	AfterEach(func() {
		// xlog exposes no getter for the package logger, so restore the same
		// default the suite entrypoint installs rather than the prior value.
		xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text"))
	})

	It("logs at error, and names the in-band download, when a managed backend degrades", func() {
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		fake := &fakeArtifactMaterializer{err: errors.New("materialization refused")}
		definition := &gallery.ModelConfig{Name: "legacy", ConfigFile: `
backend: transformers
parameters:
  model: owner/legacy
`}

		// Behavior is unchanged: the install still succeeds on the legacy path.
		_, err = gallery.InstallModel(context.Background(), state, "legacy", definition, nil, nil, false,
			gallery.WithArtifactMaterializer(fake))
		Expect(err).NotTo(HaveOccurred())

		logged := captured.String()
		Expect(logged).To(ContainSubstring("legacy"))
		Expect(logged).To(ContainSubstring("transformers"))
		Expect(logged).To(ContainSubstring("in-band"))
	})
})
