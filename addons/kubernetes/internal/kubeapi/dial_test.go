// SPDX-License-Identifier: MIT
package kubeapi_test

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/rest"
)

var _ = Describe("the address a client dials", func() {
	It("refuses an address of the host or of its local link, after DNS", func() {
		for _, a := range []string{"127.0.0.1:443", "127.9.9.9:6443", "[::1]:443", "[::ffff:127.0.0.1]:443", "0.0.0.0:443", "0.1.2.3:443",
			"169.254.169.254:80", "[fe80::1%eth0]:443", "224.0.0.1:443", "[ff02::1]:443", "[::]:443",
			"[64:ff9b::7f00:1]:443", "[64:ff9b::a9fe:a9fe]:443", "[64:ff9b:1::7f00:1]:443", "[::7f00:1]:443", "not-an-address"} {
			Expect(errors.Is(kubeapi.CheckDial(a, false), kubeapi.ErrLocalServer)).To(BeTrue(), a)
		}
		for _, a := range []string{"10.0.0.1:6443", "192.168.1.10:443", "[2001:db8::1]:443", "[64:ff9b::a00:1]:443", "100.64.0.1:443"} {
			Expect(kubeapi.CheckDial(a, false)).To(Succeed(), a)
		}
	})

	It("allows loopback only when a test asks for it, and never a link-local address", func() {
		Expect(kubeapi.CheckDial("127.0.0.1:6443", true)).To(Succeed())
		Expect(kubeapi.CheckDial("[::1]:6443", true)).To(Succeed())
		Expect(errors.Is(kubeapi.CheckDial("169.254.169.254:80", true), kubeapi.ErrLocalServer)).To(BeTrue())
	})

	It("refuses a host name that resolves to loopback before any request leaves", func() {
		var hits atomic.Int32
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
		defer srv.Close()
		ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
		port := srv.URL[strings.LastIndex(srv.URL, ":"):]
		cfg := &rest.Config{Host: "https://localhost" + port, BearerToken: "t", TLSClientConfig: rest.TLSClientConfig{CAData: ca, ServerName: "example.com"}}
		c, err := kubeapi.NewFactory()(cfg)
		Expect(err).NotTo(HaveOccurred())
		_, err = c.Typed.Discovery().ServerVersion()
		Expect(errors.Is(err, kubeapi.ErrLocalServer)).To(BeTrue(), "%v", err)
		_, err = c.Exec.Exec(context.Background(), "team-a", "web", "app", []string{"true"})
		Expect(errors.Is(err, kubeapi.ErrLocalServer)).To(BeTrue(), "%v", err)
		Expect(hits.Load()).To(BeZero())
	})
})
