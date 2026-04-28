package mcp

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMCP(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "core/http/endpoints/mcp test suite")
}
