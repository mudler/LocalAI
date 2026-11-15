package oauthtoken_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOAuthToken(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "OAuth token client")
}
