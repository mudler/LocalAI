// SPDX-License-Identifier: MIT
package diagnostics

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestDiagnostics(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Diagnostics Suite") }
