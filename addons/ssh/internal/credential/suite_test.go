// SPDX-License-Identifier: MIT
package credential_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCredential(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "SSH add-on credential")
}
