// SPDX-License-Identifier: MIT
package config

import (
	"bytes"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
	"log/slog"
)

var _ = Describe("Proxy credentials", func() {
	It("preserves and resolves a direct admin credential", func() {
		var p ProxyConfig
		Expect(json.Unmarshal([]byte(`{"api_key":"fake-direct-key"}`), &p)).To(Succeed())
		key, err := p.ResolveAPIKey(nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(key).To(Equal("fake-direct-key"))
		encoded, err := json.Marshal(p)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).To(ContainSubstring(`"api_key":"fake-direct-key"`))
	})
	It("rejects direct credentials combined with another source", func() {
		var c ModelConfig
		Expect(json.Unmarshal([]byte(`{"proxy":{"api_key":"fake-key","api_key_env":"OTHER"}}`), &c)).To(Succeed())
		_, err := c.Validate()
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Proxy credential logging", func() {
	It("keeps secrets out of structured config logs without changing storage", func() {
		var output bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&output, nil))
		c := ModelConfig{Proxy: ProxyConfig{APIKey: "fake-direct-secret"}, Environment: map[string]string{"KEY": "fake-env-secret"}}
		logger.Info("config", "config", c, "proxy", c.Proxy)
		Expect(output.String()).NotTo(ContainSubstring("fake-direct-secret"))
		Expect(output.String()).NotTo(ContainSubstring("fake-env-secret"))
		data, err := yaml.Marshal(c)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("fake-direct-secret"))
	})
})
