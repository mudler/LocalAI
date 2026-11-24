package routes

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
)

var _ = Describe("installedBackendNames", func() {
	// A worker reports the concrete build with its alias on the same row; a
	// model or import that names the alias must see it as installed.
	It("reports each concrete backend under its alias too", func() {
		backends := gallery.SystemBackends{
			"cuda13-llama-cpp": {
				Name:     "cuda13-llama-cpp",
				Metadata: &gallery.BackendMetadata{Name: "cuda13-llama-cpp", Alias: "llama-cpp"},
			},
		}

		Expect(installedBackendNames(backends)).To(ConsistOf("cuda13-llama-cpp", "llama-cpp"))
	})

	It("reports backends without an alias or metadata by name only", func() {
		backends := gallery.SystemBackends{
			"piper":   {Name: "piper", Metadata: &gallery.BackendMetadata{Name: "piper"}},
			"whisper": {Name: "whisper"},
		}

		Expect(installedBackendNames(backends)).To(ConsistOf("piper", "whisper"))
	})
})
