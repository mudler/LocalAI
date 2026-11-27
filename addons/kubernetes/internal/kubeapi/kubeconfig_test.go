// SPDX-License-Identifier: MIT
package kubeapi_test

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	server  = "https://cluster.example:6443"
	caPEM   = "-----BEGIN CERTIFICATE-----\nMIIBfake\n-----END CERTIFICATE-----\n"
	tokenKC = "kube-token-very-secret-0123"
)

var conn = kubeapi.Conn{Server: server, Context: "prod"}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// kubeconfig writes a one-context kubeconfig. cluster and user are extra
// lines under the cluster and the user.
func kubeconfig(cluster, user string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: c1
  cluster:
    server: %s
%s
- name: other
  cluster:
    server: https://other.example:6443
contexts:
- name: prod
  context:
    cluster: c1
    user: u1
    namespace: team-a
- name: elsewhere
  context:
    cluster: other
    user: u1
current-context: prod
users:
- name: u1
  user:
%s
`, server, cluster, user)
}

var (
	caLine    = "    certificate-authority-data: " + b64(caPEM)
	tokenLine = "    token: " + tokenKC
)

var _ = Describe("a pasted kubeconfig", func() {
	It("builds a config for the pinned context with token and CA data", func() {
		cfg, err := kubeapi.ParseKubeconfig(kubeconfig(caLine, tokenLine), conn)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Host).To(Equal(server))
		Expect(cfg.BearerToken).To(Equal(tokenKC))
		Expect(string(cfg.TLSClientConfig.CAData)).To(Equal(caPEM))
		Expect(cfg.UserAgent).To(Equal("localai-kubernetes"))
	})

	It("builds a config from embedded client cert and key", func() {
		user := "    client-certificate-data: " + b64("CERT") + "\n    client-key-data: " + b64("KEY")
		cfg, err := kubeapi.ParseKubeconfig(kubeconfig(caLine, user), conn)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(cfg.TLSClientConfig.CertData)).To(Equal("CERT"))
		Expect(string(cfg.TLSClientConfig.KeyData)).To(Equal("KEY"))
	})

	It("refuses exec and auth-provider plugins", func() {
		for field, user := range map[string]string{
			"exec":          "    exec:\n      apiVersion: client.authentication.k8s.io/v1\n      command: aws\n      interactiveMode: Never",
			"auth-provider": "    auth-provider:\n      name: oidc",
		} {
			_, err := kubeapi.ParseKubeconfig(kubeconfig(caLine, user), conn)
			Expect(errors.Is(err, kubeapi.ErrUnsafeKubeconfig)).To(BeTrue(), field)
			Expect(err.Error()).To(ContainSubstring(field))
		}
	})

	It("refuses a kubeconfig that names a local file without opening it", func() {
		dir := GinkgoT().TempDir()
		file := filepath.Join(dir, "locked")
		Expect(os.WriteFile(file, []byte("x"), 0o000)).To(Succeed())
		for field, kc := range map[string]string{
			"certificate-authority": kubeconfig("    certificate-authority: "+file, tokenLine),
			"client-certificate":    kubeconfig(caLine, "    client-certificate: "+file+"\n    client-key-data: "+b64("KEY")),
			"client-key":            kubeconfig(caLine, "    client-certificate-data: "+b64("CERT")+"\n    client-key: "+file),
			"tokenFile":             kubeconfig(caLine, "    tokenFile: "+file),
		} {
			_, err := kubeapi.ParseKubeconfig(kc, conn)
			Expect(errors.Is(err, kubeapi.ErrUnsafeKubeconfig)).To(BeTrue(), field)
			Expect(err.Error()).To(ContainSubstring(field))
			Expect(errors.Is(err, os.ErrPermission)).To(BeFalse(), field)
			Expect(err.Error()).NotTo(ContainSubstring("permission denied"), field)
			Expect(err.Error()).NotTo(ContainSubstring(dir), field)
		}
	})

	It("refuses proxy-url, insecure-skip-tls-verify and basic auth", func() {
		for field, kc := range map[string]string{
			"proxy-url":                kubeconfig(caLine+"\n    proxy-url: http://127.0.0.1:3128", tokenLine),
			"insecure-skip-tls-verify": kubeconfig("    insecure-skip-tls-verify: true", tokenLine),
			"username":                 kubeconfig(caLine, "    username: admin\n    password: hunter2"),
		} {
			_, err := kubeapi.ParseKubeconfig(kc, conn)
			Expect(errors.Is(err, kubeapi.ErrUnsafeKubeconfig)).To(BeTrue(), field)
			Expect(err.Error()).To(ContainSubstring(field))
			Expect(err.Error()).NotTo(ContainSubstring("hunter2"))
		}
	})

	It("refuses a context that is missing and a context whose server is not the key's", func() {
		_, err := kubeapi.ParseKubeconfig(kubeconfig(caLine, tokenLine), kubeapi.Conn{Server: server, Context: "nope"})
		Expect(errors.Is(err, kubeapi.ErrNoContext)).To(BeTrue())
		_, err = kubeapi.ParseKubeconfig(kubeconfig(caLine, tokenLine), kubeapi.Conn{Server: server, Context: "elsewhere"})
		Expect(errors.Is(err, kubeapi.ErrServerMismatch)).To(BeTrue())
	})

	It("checks only the pinned context", func() {
		kc := strings.Replace(kubeconfig(caLine, tokenLine), "    server: https://other.example:6443", "    server: https://other.example:6443\n    certificate-authority: /etc/ca.crt", 1)
		_, err := kubeapi.ParseKubeconfig(kc, conn)
		Expect(err).NotTo(HaveOccurred())
	})

	It("refuses more than the host passes and text that is not a kubeconfig", func() {
		_, err := kubeapi.ParseKubeconfig(kubeconfig(caLine, tokenLine)+"#"+strings.Repeat("x", 16<<10), conn)
		Expect(errors.Is(err, kubeapi.ErrTooLarge)).To(BeTrue())
		_, err = kubeapi.ParseKubeconfig("{not yaml: [", conn)
		Expect(errors.Is(err, kubeapi.ErrUnsafeKubeconfig)).To(BeTrue())
	})

	It("never puts the token in an error", func() {
		for _, c := range []kubeapi.Conn{{Server: server, Context: "nope"}, {Server: server, Context: "elsewhere"}} {
			_, err := kubeapi.ParseKubeconfig(kubeconfig(caLine, tokenLine), c)
			Expect(err.Error()).NotTo(ContainSubstring(tokenKC))
		}
	})

	It("lists the contexts whose server and data it accepts", func() {
		Expect(kubeapi.Contexts(kubeconfig(caLine, tokenLine))).To(ConsistOf(conn, kubeapi.Conn{Server: "https://other.example:6443", Context: "elsewhere"}))
		kc := strings.Replace(kubeconfig(caLine, tokenLine), "    server: https://other.example:6443", "    server: https://other.example:6443\n    certificate-authority: /etc/ca.crt", 1)
		Expect(kubeapi.Contexts(kc)).To(ConsistOf(conn))
	})
})
