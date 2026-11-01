// SPDX-License-Identifier: MIT
package memory

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestMemory(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Capture reducer") }
