package media_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMedia(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Studio media tools")
}
