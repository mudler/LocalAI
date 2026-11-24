package builtinskills_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBuiltinSkills(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Studio built-in skills")
}
