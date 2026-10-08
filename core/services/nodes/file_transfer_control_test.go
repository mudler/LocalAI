package nodes

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

var _ = Describe("The control plane on the file transfer server", func() {
	freeAddr := func() string {
		GinkgoHelper()
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		addr := l.Addr().String()
		Expect(l.Close()).To(Succeed())
		return addr
	}

	start := func(token string, control http.Handler) string {
		GinkgoHelper()
		addr := freeAddr()
		dir := GinkgoT().TempDir()
		srv, err := StartFileTransferServerWithControl(addr, dir, dir, dir, token, 1<<20, nil, nil, control)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { ShutdownFileTransferServer(srv) })
		Eventually(func() error {
			c, err := net.DialTimeout("tcp", addr, time.Second)
			if err == nil {
				_ = c.Close()
			}
			return err
		}).Should(Succeed())
		return "http://" + addr
	}

	do := func(url, token string) (int, string) {
		GinkgoHelper()
		req, err := http.NewRequest(http.MethodPost, url, strings.NewReader("{}"))
		Expect(err).ToNot(HaveOccurred())
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	It("serves every path under the prefix to a caller that holds the token", func() {
		control := http.NewServeMux()
		control.HandleFunc(workerctl.PathOf(workerctl.VerbBackendList), func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, "listed")
		})
		base := start("secret", control)

		status, body := do(base+workerctl.PathOf(workerctl.VerbBackendList), "secret")
		Expect(status).To(Equal(http.StatusOK))
		Expect(body).To(Equal("listed"))
	})

	It("refuses a caller with no token or the wrong one, before any verb runs", func() {
		var ran int
		control := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran++ })
		base := start("secret", control)

		for _, token := range []string{"", "wrong"} {
			status, _ := do(base+workerctl.PathOf(workerctl.VerbNodeStop), token)
			Expect(status).To(Equal(http.StatusUnauthorized), "token %q", token)
		}
		Expect(ran).To(BeZero())
	})

	It("takes verbs that are registered after the server started", func() {
		control := http.NewServeMux()
		base := start("secret", control)
		status, _ := do(base+workerctl.PathOf(workerctl.VerbModelOp), "secret")
		Expect(status).To(Equal(http.StatusNotFound), "nothing serves the verb yet")

		control.HandleFunc(workerctl.PathOf(workerctl.VerbModelOp), func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
		status, _ = do(base+workerctl.PathOf(workerctl.VerbModelOp), "secret")
		Expect(status).To(Equal(http.StatusNoContent))
	})

	It("serves no control path when the worker has no control plane", func() {
		base := start("secret", nil)
		status, _ := do(base+workerctl.PathOf(workerctl.VerbNodeStop), "secret")
		Expect(status).To(Equal(http.StatusNotFound))
	})

	It("keeps the probes open, with a control plane mounted", func() {
		base := start("secret", http.NewServeMux())
		resp, err := http.Get(base + "/healthz")
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
	})
})
