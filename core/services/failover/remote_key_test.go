// SPDX-License-Identifier: MIT
package failover

import (
	"context"
	"errors"
	"net/http"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type remoteKeys map[string]config.ProxyRemoteKey

func (r remoteKeys) ResolveKey(_ context.Context, id string) (config.ProxyRemoteKey, error) {
	k, ok := r[id]
	if !ok {
		return config.ProxyRemoteKey{}, errors.New("not found")
	}
	return k, nil
}

var _ = Describe("Probe of a remote entry", func() {
	BeforeEach(func() {
		config.SetProxyKeyResolver(remoteKeys{
			"r1": {Key: "remote-key", Kind: "openai", Base: "https://api.example.com/v1"},
			"r2": {Key: "anthropic-key", Kind: "anthropic", Base: "https://api.anthropic.com"},
		})
		DeferCleanup(func() { config.SetProxyKeyResolver(nil) })
	})
	probe := func(c config.ModelConfig) (*http.Request, error) {
		req, err := http.NewRequest("GET", c.Proxy.UpstreamURL, nil)
		Expect(err).NotTo(HaveOccurred())
		return req, (&DefaultProber{}).authorize(req, c)
	}

	It("sends the key of the remote the entry names", func() {
		req, err := probe(config.ModelConfig{Backend: "cloud-proxy", Proxy: config.ProxyConfig{
			Remote: "r1", UpstreamURL: "https://api.example.com/v1/chat/completions"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Header.Get("Authorization")).To(Equal("Bearer remote-key"))

		req, err = probe(config.ModelConfig{Backend: "cloud-proxy", Proxy: config.ProxyConfig{
			Remote: "r2", Provider: config.ProxyProviderAnthropic, UpstreamURL: "https://api.anthropic.com/v1/messages"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Header.Get("x-api-key")).To(Equal("anthropic-key"))
	})
	It("does not send the key to another server", func() {
		req, err := probe(config.ModelConfig{Backend: "cloud-proxy", Proxy: config.ProxyConfig{
			Remote: "r1", UpstreamURL: "https://attacker.example.net/v1/chat/completions"}})
		Expect(err).To(MatchError(ContainSubstring("not an address of this openai remote")))
		Expect(req.Header.Get("Authorization")).To(BeEmpty())
	})
	It("fails when the remote is gone", func() {
		_, err := probe(config.ModelConfig{Backend: "cloud-proxy", Proxy: config.ProxyConfig{
			Remote: "gone", UpstreamURL: "https://api.example.com/v1/chat/completions"}})
		Expect(err).To(MatchError(ContainSubstring("key unavailable")))
	})
})
