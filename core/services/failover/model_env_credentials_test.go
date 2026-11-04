// SPDX-License-Identifier: MIT
package failover

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http"
)

var _ = Describe("Model env credentials", func() {
	It("authorizes with a direct credential or frontend fallback", func() {
		p := &DefaultProber{EnvLookup: func(string) string { return "frontend-key" }}
		req, err := http.NewRequest("GET", "http://example.invalid", nil)
		Expect(err).NotTo(HaveOccurred())
		c := config.ModelConfig{Proxy: config.ProxyConfig{APIKey: "direct-key"}}
		Expect(p.authorize(req, c)).To(Succeed())
		Expect(req.Header.Get("Authorization")).To(Equal("Bearer direct-key"))
		c.Proxy = config.ProxyConfig{APIKeyEnv: "KEY"}
		Expect(p.authorize(req, c)).To(Succeed())
		Expect(req.Header.Get("Authorization")).To(Equal("Bearer frontend-key"))
	})
	It("uses model env before the frontend env, including an explicit empty value", func() {
		p := &DefaultProber{EnvLookup: func(string) string { return "frontend-key" }}
		c := config.ModelConfig{Environment: map[string]string{"ANTHROPIC_API_KEY": "model-key"}}
		c.Proxy.APIKeyEnv = "ANTHROPIC_API_KEY"
		req, err := http.NewRequest("GET", "http://example.invalid", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(p.authorize(req, c)).To(Succeed())
		Expect(req.Header.Get("Authorization")).To(Equal("Bearer model-key"))
		c.Environment["ANTHROPIC_API_KEY"] = ""
		Expect(p.authorize(req, c)).NotTo(Succeed())
	})
})
