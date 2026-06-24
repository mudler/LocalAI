package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestNemoSpeech(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "nemo-speech-cpp Backend Suite")
}
