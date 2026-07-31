// SPDX-License-Identifier: MIT

package tokens_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	tiktoken "github.com/pkoukk/tiktoken-go"
)

type testBPELoader struct{}

func (testBPELoader) LoadTiktokenBpe(string) (map[string]int, error) {
	ranks := make(map[string]int, 256)
	for value := 0; value < 256; value++ {
		ranks[string([]byte{byte(value)})] = value
	}
	for index, token := range []string{
		"he", "hel", "hell", "hello",
		"wo", "wor", "worl", "world",
		" w", " wo", " wor", " worl", " world",
	} {
		ranks[token] = 256 + index
	}
	return ranks, nil
}

func TestTokens(t *testing.T) {
	RegisterFailHandler(Fail)
	tiktoken.SetBpeLoader(testBPELoader{})
	RunSpecs(t, "Tokens Suite")
}
