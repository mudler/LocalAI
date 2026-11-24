// SPDX-License-Identifier: MIT
package transport_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTransport(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Telegram transport")
}
