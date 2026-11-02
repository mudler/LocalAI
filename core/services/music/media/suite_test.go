// SPDX-License-Identifier: MIT
package media_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestMusicMedia(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Bounded music WAV validation")
}
