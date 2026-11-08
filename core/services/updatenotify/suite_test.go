package updatenotify_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestUpdateNotify(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Update notifications suite")
}
