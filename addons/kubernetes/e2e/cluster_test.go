//go:build kubernetes_e2e

// SPDX-License-Identifier: MIT
package e2e_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	adminToken = "e2e-admin-token-0123456789"
	teamToken  = "e2e-team-token-0123456789"
)

// cluster is a running etcd and kube-apiserver.
type cluster struct {
	dir    string
	server string
	ca     []byte
	procs  []*exec.Cmd
	logs   []string
}

func freePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func (c *cluster) start(name string, args ...string) {
	log := filepath.Join(c.dir, name+".log")
	f, err := os.Create(log)
	Expect(err).NotTo(HaveOccurred())
	cmd := exec.Command(filepath.Join(os.Getenv("KUBE_ASSETS"), name), args...)
	cmd.Stdout, cmd.Stderr = f, f
	Expect(cmd.Start()).To(Succeed())
	c.procs = append(c.procs, cmd)
	c.logs = append(c.logs, log)
}

func (c *cluster) stop() {
	for i := len(c.procs) - 1; i >= 0; i-- {
		_ = c.procs[i].Process.Kill()
		_, _ = c.procs[i].Process.Wait()
	}
}

// printLogs shows the end of the process logs when a spec failed.
func (c *cluster) printLogs() {
	for _, l := range c.logs {
		b, _ := os.ReadFile(l)
		if len(b) > 4000 {
			b = b[len(b)-4000:]
		}
		fmt.Fprintf(os.Stderr, "==> %s\n%s\n", l, b)
	}
}

func writeKeyPair(dir string) (string, string) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).NotTo(HaveOccurred())
	priv := filepath.Join(dir, "sa.key")
	pub := filepath.Join(dir, "sa.pub")
	Expect(os.WriteFile(priv, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600)).To(Succeed())
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(pub, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600)).To(Succeed())
	return priv, pub
}

func startCluster() *cluster {
	assets := os.Getenv("KUBE_ASSETS")
	if assets == "" {
		Fail("set KUBE_ASSETS to a folder with etcd and kube-apiserver, for example from: go run sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.22 use 1.37.0 --bin-dir <dir> -p path")
	}
	c := &cluster{dir: GinkgoT().TempDir()}
	// Registered first, so the processes stop even when the start fails.
	DeferCleanup(func() {
		if CurrentSpecReport().Failed() {
			c.printLogs()
		}
		c.stop()
	})
	etcdPort, peerPort, apiPort := freePort(), freePort(), freePort()
	etcdURL := fmt.Sprintf("http://127.0.0.1:%d", etcdPort)
	c.start("etcd", "--data-dir", filepath.Join(c.dir, "etcd"), "--listen-client-urls", etcdURL, "--advertise-client-urls", etcdURL,
		"--listen-peer-urls", fmt.Sprintf("http://127.0.0.1:%d", peerPort), "--unsafe-no-fsync=true")
	priv, pub := writeKeyPair(c.dir)
	tokens := filepath.Join(c.dir, "tokens.csv")
	Expect(os.WriteFile(tokens, []byte(adminToken+",e2e-admin,1,system:masters\n"+teamToken+",e2e-team,2,system:authenticated\n"), 0o600)).To(Succeed())
	certs := filepath.Join(c.dir, "certs")
	c.start("kube-apiserver", "--etcd-servers", etcdURL, "--secure-port", fmt.Sprint(apiPort), "--bind-address", "127.0.0.1",
		"--advertise-address", "127.0.0.1", "--endpoint-reconciler-type", "none", "--cert-dir", certs, "--token-auth-file", tokens, "--authorization-mode", "RBAC",
		"--service-account-issuer", "https://kubernetes.default.svc", "--service-account-key-file", pub,
		"--service-account-signing-key-file", priv, "--service-cluster-ip-range", "10.0.0.0/24",
		"--disable-admission-plugins", "ServiceAccount", "--allow-privileged=true")
	// The add-on refuses a loopback address, so the specs name the server
	// by host name; the serving certificate of the API server includes it.
	c.server = fmt.Sprintf("https://localhost:%d", apiPort)
	// The self-signed serving certificate covers 127.0.0.1; it appears once
	// the server starts.
	Eventually(func() error {
		b, err := os.ReadFile(filepath.Join(certs, "apiserver.crt"))
		c.ca = b
		return err
	}, 60*time.Second, 200*time.Millisecond).Should(Succeed())
	pool := x509.NewCertPool()
	Expect(pool.AppendCertsFromPEM(c.ca)).To(BeTrue())
	hc := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	Eventually(func() int {
		req, _ := http.NewRequest(http.MethodGet, c.server+"/readyz", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		res, err := hc.Do(req)
		if err != nil {
			return 0
		}
		_ = res.Body.Close()
		return res.StatusCode
	}, 60*time.Second, 250*time.Millisecond).Should(Equal(200))
	return c
}

func (c *cluster) admin() kubernetes.Interface {
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: c.server, BearerToken: adminToken, TLSClientConfig: rest.TLSClientConfig{CAData: c.ca}})
	Expect(err).NotTo(HaveOccurred())
	return cs
}

// kubeconfig is the credential a person pastes: CA data and a token, one
// context named e2e.
func (c *cluster) kubeconfig(token string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: e2e
  cluster:
    server: %s
    certificate-authority-data: %s
contexts:
- name: e2e
  context:
    cluster: e2e
    user: e2e
current-context: e2e
users:
- name: e2e
  user:
    token: %s
`, c.server, base64.StdEncoding.EncodeToString(c.ca), token)
}

var bg = context.Background()
