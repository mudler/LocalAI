// SPDX-License-Identifier: MIT
package haapi_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHaapi(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Home Assistant add-on haapi")
}
