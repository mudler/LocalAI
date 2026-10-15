package main

import (
	"testing"

	"github.com/mudler/xlog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLocalAIProxy(t *testing.T) {
	RegisterFailHandler(Fail)
	// The specs drive upstream failures on purpose; their warnings are
	// expected and would only bury real failures in the output.
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevelError, xlog.TextFormat))
	RunSpecs(t, "localai-proxy specs")
}
