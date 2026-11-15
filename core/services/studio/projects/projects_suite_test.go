package projects_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestProjects(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Projects")
}
