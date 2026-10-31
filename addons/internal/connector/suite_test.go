// SPDX-License-Identifier: MIT
package connector_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestConnector(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Connector add-on kit")
}
