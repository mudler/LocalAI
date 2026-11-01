package deck_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDeck(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Deck Suite")
}
