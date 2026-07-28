package modelartifacts_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

// bypassedLocker grants the artifact lock to everyone who asks. It reproduces
// the state a real cluster was in during #10981: flock(2) on CIFS returned
// EACCES, both controller replicas concluded the lock was unusable, and both
// proceeded to materialize the same artifact at the same time. The lock is
// meant to prevent that, but a correctness property must not depend on a
// primitive that a network filesystem can silently take away.
type bypassedLocker struct{}

func (bypassedLocker) TryLock() (bool, error) { return true, nil }
func (bypassedLocker) Unlock() error          { return nil }

// rendezvousServer serves payload to every GET, but holds each response until
// concurrent GETs have arrived, then dribbles the body out in two chunks. That
// makes the two writers provably overlap inside the download rather than
// relying on scheduling luck, so the spec is deterministic rather than flaky.
func rendezvousServer(payload []byte, concurrent int32) *httptest.Server {
	var arrived atomic.Int32
	gate := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			// The resume probe is a HEAD. Answering without Accept-Ranges keeps
			// this server non-resumable, which is the harsher case: a writer
			// that finds a foreign partial discards it outright.
			w.WriteHeader(http.StatusOK)
			return
		}
		if arrived.Add(1) >= concurrent {
			once.Do(func() { close(gate) })
		}
		select {
		case <-gate:
		case <-time.After(10 * time.Second):
		}
		half := len(payload) / 2
		_, _ = w.Write(payload[:half])
		w.(http.Flusher).Flush()
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write(payload[half:])
	}))
	DeferCleanup(server.Close)
	return server
}

var _ = Describe("concurrent materialization without a working lock", func() {
	// The partial tree is shared mutable state. Before writer-unique partial
	// paths it was safe only because the lock held: two writers opened the same
	// blob with O_APPEND and interleaved into one file, and the resume probe
	// read the other writer's in-flight size. SHA verification caught the
	// damage only after both had burned the whole download.
	It("lets two writers materialize the same artifact concurrently without corrupting each other", func() {
		payload := make([]byte, 128*1024)
		for i := range payload {
			payload[i] = byte(i % 251)
		}
		sum := sha256.Sum256(payload)
		server := rendezvousServer(payload, 2)

		snapshot := hfapi.Snapshot{
			Endpoint: "https://huggingface.co", Repo: "owner/repo",
			RequestedRevision: "main", ResolvedRevision: "0123456789abcdef0123456789abcdef01234567",
			Files: []hfapi.SnapshotFile{{
				Path: "model.safetensors", Size: int64(len(payload)),
				LFSOID: hex.EncodeToString(sum[:]), URL: server.URL + "/model",
			}},
		}
		modelsPath := GinkgoT().TempDir()
		spec := modelartifacts.Spec{Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"}}

		type outcome struct {
			result modelartifacts.Result
			err    error
		}
		outcomes := make([]outcome, 2)
		var group sync.WaitGroup
		for writer := range outcomes {
			group.Add(1)
			go func() {
				defer GinkgoRecover()
				defer group.Done()
				// Separate managers stand in for separate processes: each one
				// draws its own writer identity at construction.
				manager := modelartifacts.NewManager(&fakeSnapshotResolver{snapshot: snapshot},
					modelartifacts.WithLocker(func(string) modelartifacts.Locker { return bypassedLocker{} }))
				result, err := manager.Ensure(context.Background(), modelsPath, spec)
				outcomes[writer] = outcome{result: result, err: err}
			}()
		}
		group.Wait()

		for writer, got := range outcomes {
			Expect(got.err).NotTo(HaveOccurred(),
				"writer %d must not be poisoned by its peer; two writers sharing a partial path is exactly the corruption this guards against", writer)
			Expect(os.ReadFile(filepath.Join(modelsPath, filepath.FromSlash(got.result.RelativePath), "model.safetensors"))).
				To(Equal(payload), "writer %d resolved to a snapshot whose bytes are not the artifact", writer)
		}

		// Exactly one snapshot is committed: the loser of the commit race
		// reconciles onto the winner's tree rather than publishing a rival.
		committed, err := filepath.Glob(filepath.Join(modelsPath, ".artifacts", "huggingface", "*"))
		Expect(err).NotTo(HaveOccurred())
		Expect(committed).To(HaveLen(1))

		// Both writers finished, so neither may leave a partial tree behind.
		leftovers, err := filepath.Glob(filepath.Join(modelsPath, ".artifacts", ".partial", "*"))
		Expect(err).NotTo(HaveOccurred())
		Expect(leftovers).To(BeEmpty(), "a completed writer must not leak its partial tree onto a shared volume")
	})
})
