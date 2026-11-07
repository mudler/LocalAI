// SPDX-License-Identifier: MIT
package sshclient_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSshclient(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "SSH add-on sshclient")
}
