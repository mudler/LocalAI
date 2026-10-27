package chv

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// fakeAPI serves the Cloud Hypervisor API answers this client relies on.
func fakeAPI(t *testing.T) string {
	t.Helper()
	sock := filepath.Join(shortDir(t), "api.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
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
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return sock
}

func TestAPI(t *testing.T) {
	a := newAPI(fakeAPI(t))
	ctx := context.Background()
	if v, err := a.ping(ctx); err != nil || v != "54.0.0" {
		t.Fatalf("ping = %q, %v", v, err)
	}
	if _, err := a.state(ctx); err == nil || !strings.Contains(err.Error(), "VM is not created") {
		t.Fatalf("state: %v", err)
	}
	if err := a.put(ctx, "vm.snapshot", snapshotConfig{DestinationURL: "file:///snap"}); err != nil {
		t.Fatal(err)
	}
	if err := a.put(ctx, "vmm.shutdown", nil); err != nil {
		t.Fatal(err)
	}
}
