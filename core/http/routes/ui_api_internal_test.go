package routes

import (
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/onsi/gomega"
)

func TestUsecaseFiltersIncludes3D(t *testing.T) {
	g := gomega.NewWithT(t)
	g.Expect(usecaseFilters[config.Usecase3D]).To(gomega.Equal(config.FLAG_3D))
}

// GET /api/backends/usecases projects each backend's PossibleUsecases through
// usecaseFilters, and the gallery greys out any filter a selected backend does
// not report. llama-cpp serves Qwen3-TTS, so the TTS filter has to survive that
// projection or the gallery hides the very entries the backend can run.
func TestBackendUsecasesReportsTTSForLlamaCpp(t *testing.T) {
	g := gomega.NewWithT(t)

	var keys []string
	for _, uc := range config.BackendCapabilities["llama-cpp"].PossibleUsecases {
		if _, ok := usecaseFilters[uc]; ok {
			keys = append(keys, uc)
		}
	}

	g.Expect(keys).To(gomega.ContainElement(config.UsecaseTTS))
	g.Expect(keys).To(gomega.ContainElement(config.UsecaseChat))
}
