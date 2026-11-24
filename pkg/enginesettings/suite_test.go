package enginesettings_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEngineSettings(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "engine settings suite")
}
