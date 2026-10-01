package voicerecognition_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVoiceRecognition(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "VoiceRecognition Suite")
}
