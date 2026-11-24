// SPDX-License-Identifier: MIT
package mcptransport

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestMCPTransport(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Remote MCP transport") }
