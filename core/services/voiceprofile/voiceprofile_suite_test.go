package voiceprofile_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVoiceProfile(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Voice profile service suite")
}
