package ext4mk_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestExt4mk(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Ext4mk Suite")
}
