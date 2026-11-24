// SPDX-License-Identifier: MIT
package contract

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestContract(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Addon contracts") }
