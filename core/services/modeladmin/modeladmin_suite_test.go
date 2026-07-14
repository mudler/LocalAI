package modeladmin

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestModelAdmin(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "modeladmin test suite")
}
