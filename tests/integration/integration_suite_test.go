package integration_test

import (
	"os"
	"testing"

	"github.com/mudler/xlog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLocalAI(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text"))
	RegisterFailHandler(Fail)
	RunSpecs(t, "LocalAI test suite")
}
