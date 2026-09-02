package nodes

import (
	"net"
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("extra authenticated routes on the worker HTTP server", func() {
	const token = "s3cr3t"

	var (
		srv  *http.Server
		base string
	)

	BeforeEach(func() {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		dir := GinkgoT().TempDir()
		srv, err = StartFileTransferServerWithRoutes(lis, dir, dir, dir, token, 0, nil,
			&AuthenticatedRoutes{
				Prefix: "/v1/control/",
				Register: func(mux *http.ServeMux) {
					mux.HandleFunc("/v1/control/ping", func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusOK)
						_, _ = w.Write([]byte("pong"))
					})
				},
			})
		Expect(err).NotTo(HaveOccurred())
		base = "http://" + lis.Addr().String()
		DeferCleanup(func() { ShutdownFileTransferServer(srv) })
	})

	get := func(path, bearer string) *http.Response {
		GinkgoHelper()
		req, err := http.NewRequest(http.MethodGet, base+path, nil)
		Expect(err).NotTo(HaveOccurred())
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = resp.Body.Close() })
		return resp
	}

	It("serves a registered extra route to a caller carrying the token", func() {
		Expect(get("/v1/control/ping", token).StatusCode).To(Equal(http.StatusOK))
	})

	It("refuses an unauthenticated request on an extra route", func() {
		// The control plane must not be a second authentication path: an extra
		// route that forgot its own check would be an unauthenticated command
		// on the boundary the tunnel exposes.
		Expect(get("/v1/control/ping", "").StatusCode).To(Equal(http.StatusUnauthorized))
	})

	It("refuses a wrong token on an extra route", func() {
		Expect(get("/v1/control/ping", "wrong").StatusCode).To(Equal(http.StatusUnauthorized))
	})

	It("checks the token before the route exists, so an unknown control path leaks nothing", func() {
		Expect(get("/v1/control/no-such-verb", "").StatusCode).To(Equal(http.StatusUnauthorized))
	})

	It("leaves the file routes reachable alongside the extra ones", func() {
		Expect(get("/healthz", "").StatusCode).To(Equal(http.StatusOK))
	})

	It("mounts nothing when no route set is given", func() {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		dir := GinkgoT().TempDir()
		bare, err := StartFileTransferServerWithRoutes(lis, dir, dir, dir, token, 0, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { ShutdownFileTransferServer(bare) })

		req, err := http.NewRequest(http.MethodGet, "http://"+lis.Addr().String()+"/v1/control/ping", nil)
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = resp.Body.Close() })
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
		Expect(strings.TrimSpace(resp.Status)).NotTo(BeEmpty())
	})
})
