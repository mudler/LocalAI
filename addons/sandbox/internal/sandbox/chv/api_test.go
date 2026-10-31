package chv

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeAPI serves the Cloud Hypervisor API answers this client relies on.
func fakeAPI() string {
	GinkgoHelper()
	sock := filepath.Join(shortDir(), "api.sock")
	l, err := net.Listen("unix", sock)
	Expect(err).To(Succeed())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/vmm.ping", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"build_version":"v54.0","version":"54.0.0","pid":1,"features":["kvm"]}`)
	})
	mux.HandleFunc("GET /api/v1/vm.info", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `["Error from API","The VM info is not available","VM is not created"]`)
	})
	mux.HandleFunc("PUT /api/v1/vm.snapshot", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"destination_url":"file:///snap"}` || r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("PUT /api/v1/vmm.shutdown", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(l) }()
	DeferCleanup(func() { _ = srv.Close() })
	return sock
}

var _ = Describe("Cloud Hypervisor API client", func() {
	It("pings, reads the VM state and sends snapshot and shutdown requests", func() {
		a := newAPI(fakeAPI())
		ctx := context.Background()
		v, err := a.ping(ctx)
		Expect(err).To(Succeed())
		Expect(v).To(Equal("54.0.0"))
		_, err = a.state(ctx)
		Expect(err).To(MatchError(ContainSubstring("VM is not created")))
		Expect(a.put(ctx, "vm.snapshot", snapshotConfig{DestinationURL: "file:///snap"})).To(Succeed())
		Expect(a.put(ctx, "vmm.shutdown", nil)).To(Succeed())
	})
})
