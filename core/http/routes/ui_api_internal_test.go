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
