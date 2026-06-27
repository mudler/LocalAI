package finetune

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFinetune(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Finetune Suite")
}
