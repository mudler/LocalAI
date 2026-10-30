// SPDX-License-Identifier: MIT
package main

import (
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"path/filepath"
)

var _ = Describe("Proxy credential sources", func() {
	It("resolves direct, absent, env and file credentials", func() {
		key, err := resolveAPIKey("direct-canary", "", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(key).To(Equal("direct-canary"))
		key, err = resolveAPIKey("", "", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(key).To(BeEmpty())
		DeferCleanup(os.Unsetenv, "LOCALAI_TEST_PROXY_CREDENTIAL")
		Expect(os.Setenv("LOCALAI_TEST_PROXY_CREDENTIAL", "env-canary")).To(Succeed())
		key, err = resolveAPIKey("", "LOCALAI_TEST_PROXY_CREDENTIAL", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(key).To(Equal("env-canary"))
		dir, err := os.MkdirTemp("", "proxy-credentials-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, dir)
		path := filepath.Join(dir, "key")
		Expect(os.WriteFile(path, []byte("file-canary\n"), 0600)).To(Succeed())
		key, err = resolveAPIKey("", "", path)
		Expect(err).NotTo(HaveOccurred())
		Expect(key).To(Equal("file-canary"))
		_, err = resolveAPIKey("", "", path+"-missing")
		Expect(err).To(HaveOccurred())
	})
	It("rejects every conflicting source without exposing the key", func() {
		for _, args := range [][3]string{{"secret-canary", "ENV", ""}, {"secret-canary", "", "/missing"}, {"", "ENV", "/missing"}, {"secret-canary", "ENV", "/missing"}} {
			_, err := resolveAPIKey(args[0], args[1], args[2])
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).NotTo(ContainSubstring("secret-canary"))
		}
	})
	It("loads direct credentials from protobuf options", func() {
		proxy := NewLocalAIProxy()
		Expect(proxy.Load(&pb.ModelOptions{Model: "alias", Proxy: &pb.ProxyOptions{UpstreamUrl: "http://example.invalid", ApiKey: "direct-canary"}})).To(Succeed())
		Expect(proxy.cfg.Load().apiKey).To(Equal("direct-canary"))
	})
})
