package config

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/xlog"
)

// For a backend in managedArtifactBackends the legacy path is not a graceful
// degradation: the backend consumes a snapshot directory, so falling back means
// it downloads the whole repo itself, in-band, inside LoadModel on every load.
// That reliably blows the remote-load deadline, and the operator sees only a
// timeout unless the cause was logged loudly enough to survive a default log
// level. #10981 traced a 57 GB in-band pull back to a warn logged 40 minutes
// earlier.
var _ = Describe("preload artifact fallback severity", func() {
	const inferredConfig = `
name: managed
backend: transformers
parameters: {model: owner/repo}
`

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
		modelsPath := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(modelsPath, "managed.yaml"), []byte(inferredConfig), 0644)).To(Succeed())

		fake := &companionMaterializer{fail: "owner/repo"}
		loader := NewModelConfigLoader(modelsPath, WithArtifactMaterializer(fake))
		Expect(loader.LoadModelConfigsFromPath(modelsPath)).To(Succeed())

		// Behavior is unchanged: the degradation is still non-fatal.
		Expect(loader.PreloadWithContext(context.Background(), modelsPath)).To(Succeed())

		logged := captured.String()
		Expect(logged).To(ContainSubstring("managed"))
		Expect(logged).To(ContainSubstring("transformers"))
		Expect(logged).To(ContainSubstring("in-band"))
	})
})
