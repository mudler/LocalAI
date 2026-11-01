package ooxml_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOOXML(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Office OOXML helpers suite")
}
