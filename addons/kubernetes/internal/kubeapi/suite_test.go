// SPDX-License-Identifier: MIT
package kubeapi_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestKubeAPI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Kubernetes add-on API client")
}
