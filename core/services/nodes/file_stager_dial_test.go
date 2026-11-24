package nodes

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("HTTPFileStager dialing", func() {
	// recordingDialer sends every node to the listener named in routes and
	// records which node each dial was for, so a spec can tell apart the node
	// the stager asked for from the address it would have dialled itself.
	recordingDialer := func(routes map[string]string) (WorkerNetDialerFor, func() []string) {
		var mu sync.Mutex
		var dials []string
		dialFor := func(nodeID string) func(context.Context, string, string) (net.Conn, error) {
			return func(ctx context.Context, network, _ string) (net.Conn, error) {
				mu.Lock()
				dials = append(dials, nodeID)
				mu.Unlock()
				return (&net.Dialer{}).DialContext(ctx, network, routes[nodeID])
			}
		}
		return dialFor, func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), dials...)
		}
	}

	It("reaches the worker through the dialer of that node and reuses one client per node", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete || r.URL.Path != "/v1/files/ephemeral/request-id/audio/input.wav" {
				http.Error(w, "unexpected request", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		DeferCleanup(srv.Close)

		dialFor, dials := recordingDialer(map[string]string{"n1": srv.Listener.Addr().String()})
		// httpAddrFor names a host that cannot resolve: only the dialer can reach it.
		stager := NewHTTPFileStager(func(string) (string, error) { return "n1.worker.invalid:80", nil }, "tok", dialFor)

		key := "ephemeral/request-id/audio/input.wav"
		Expect(stager.ReleaseRemote(context.Background(), "n1", key)).To(Succeed())
		Expect(stager.ReleaseRemote(context.Background(), "n1", key)).To(Succeed())

		Expect(dials()).To(Equal([]string{"n1"}), "two calls to one node must reuse one client and one connection")
	})

	It("keeps two nodes that report the same address on their own dialers", func() {
		// Each fake worker answers HEAD with 404 (no copy yet) and a PUT with
		// the path it stored, prefixed with its own name, so the returned path
		// shows which worker actually received the bytes.
		worker := func(name string) *httptest.Server {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodHead:
					w.WriteHeader(http.StatusNotFound)
				case http.MethodPut:
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]string{"local_path": "/" + name + r.URL.Path})
				default:
					http.Error(w, "unexpected request", http.StatusBadRequest)
				}
			}))
			DeferCleanup(srv.Close)
			return srv
		}
		srvA, srvB := worker("a"), worker("b")

		dialFor, dials := recordingDialer(map[string]string{
			"n1": srvA.Listener.Addr().String(),
			"n2": srvB.Listener.Addr().String(),
		})
		stager := NewHTTPFileStager(func(string) (string, error) { return "shared.worker.invalid:80", nil }, "", dialFor)

		localPath := filepath.Join(GinkgoT().TempDir(), "model.bin")
		Expect(os.WriteFile(localPath, []byte("weights"), 0o600)).To(Succeed())

		pathA, err := stager.EnsureRemote(context.Background(), "n1", localPath, "models/model.bin")
		Expect(err).NotTo(HaveOccurred())
		pathB, err := stager.EnsureRemote(context.Background(), "n2", localPath, "models/model.bin")
		Expect(err).NotTo(HaveOccurred())

		Expect(pathA).To(Equal("/a/v1/files/models/model.bin"))
		Expect(pathB).To(Equal("/b/v1/files/models/model.bin"))
		Expect(dials()).To(ConsistOf("n1", "n2"), "the probe, resume HEAD and PUT of one call share that node's connection")
	})
})
