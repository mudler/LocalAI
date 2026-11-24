package skills

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSkills(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Studio skills")
}
