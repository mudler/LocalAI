package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// prefetchNodes is the registry view of the frontend half: the nodes that run
// the model and every node.
type prefetchNodes struct{ running, all []nodes.BackendNode }

func (p prefetchNodes) FindNodesWithModel(context.Context, string) ([]nodes.BackendNode, error) {
	return p.running, nil
}
func (p prefetchNodes) List(context.Context) ([]nodes.BackendNode, error) { return p.all, nil }

type recordingObjects struct{ deleted []string }

func (r *recordingObjects) Delete(_ context.Context, key string) error {
	r.deleted = append(r.deleted, key)
	return nil
}

func sumOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func backendNode(id string) nodes.BackendNode {
	return nodes.BackendNode{ID: id, Name: id, NodeType: nodes.NodeTypeBackend, Status: nodes.StatusHealthy}
}

var _ = Describe("model.prefetch between a frontend and its workers", func() {
	var (
		bus    *testutil.FakeBus
		server *httptest.Server
		roots  map[string]string
		files  *nodes.UpgradeFiles
		objs   *recordingObjects
		ctx    context.Context
	)

	// worker serves model.prefetch for one node on the shared bus, the way a
	// worker process does, with its own models directory.
	worker := func(id string, objectStorage bool) {
		root := GinkgoT().TempDir()
		roots[id] = root
		cfg := &Config{ModelsPath: filepath.Join(root, "models")}
		if objectStorage {
			cfg.StorageURL = "http://object-storage.invalid"
		}
		p := newModelPrefetcher(cfg)
		// The test server listens on the loopback address, which the guard
		// of a real worker refuses.
		p.validateURL = nil
		Expect(p.register(newNATSControlServer(bus, id))).To(Succeed())
	}
	read := func(path string) string {
		GinkgoHelper()
		data, err := os.ReadFile(path)
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	}
	place := func(id string) string { return filepath.Join(roots[id], "models", "tiny", "tiny.gguf") }
	cachePlace := func(id string) string {
		return filepath.Join(roots[id], "cache", "models", "tiny", "tiny.gguf")
	}

	BeforeEach(func() {
		ctx = context.Background()
		bus = testutil.NewFakeBus()
		roots = map[string]string{}
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("v2"))
		}))
		DeferCleanup(server.Close)
		objs = &recordingObjects{}
	})

	newFiles := func(running []string, all ...string) {
		var p prefetchNodes
		for _, id := range running {
			p.running = append(p.running, backendNode(id))
		}
		for _, id := range all {
			p.all = append(p.all, backendNode(id))
		}
		files = &nodes.UpgradeFiles{Nodes: p, Sender: nodes.NATSModelFilePrefetcher{Bus: bus}, Objects: objs, StageTimeout: time.Minute}
	}

	It("subscribes the node's model.prefetch subject", func() {
		worker("a", false)
		reply, err := bus.Request(messaging.SubjectNodeModelPrefetch("a"), []byte(`not json`), time.Second)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(reply)).To(ContainSubstring("invalid request"))
	})

	It("has each worker download and verify its copy, then place it; a worker that did not stage drops its old copy", func() {
		worker("a", false)
		worker("b", false)
		newFiles([]string{"a"}, "a", "b")
		for _, id := range []string{"a", "b"} {
			Expect(os.MkdirAll(filepath.Dir(place(id)), 0750)).To(Succeed())
			Expect(os.WriteFile(place(id), []byte("v1"), 0600)).To(Succeed())
		}
		want := []workerctl.ModelPrefetchFile{{Filename: "tiny.gguf", URI: server.URL + "/tiny.gguf", SHA256: sumOf("v2")}}

		Expect(files.Stage(ctx, "tiny", want)).To(BeEmpty())
		Expect(read(place("a") + ".next")).To(Equal("v2"))
		Expect(read(place("a"))).To(Equal("v1"), "the copy in place is untouched until the commit")

		Expect(files.Commit(ctx, "tiny", want)).To(Succeed())
		Expect(read(place("a"))).To(Equal("v2"))
		Expect(place("a") + ".next").ToNot(BeAnExistingFile())
		Expect(place("b")).ToNot(BeAnExistingFile(), "the next load sends node b the frontend's new file")
		Expect(objs.deleted).To(ConsistOf("models/tiny/tiny.gguf"))
	})

	It("fails only on the worker whose download does not match the checksum", func() {
		worker("a", false)
		newFiles([]string{"a"}, "a")
		failed := files.Stage(ctx, "tiny", []workerctl.ModelPrefetchFile{{Filename: "tiny.gguf", URI: server.URL + "/tiny.gguf", SHA256: sumOf("other")}})
		Expect(failed).To(HaveKey("a"))
		Expect(place("a") + ".next").ToNot(BeAnExistingFile())
	})

	It("falls back for a worker that does not know the verb", func() {
		worker("a", false)
		newFiles([]string{"a", "old"}, "a", "old")
		failed := files.Stage(ctx, "tiny", []workerctl.ModelPrefetchFile{{Filename: "tiny.gguf", URI: server.URL + "/tiny.gguf", SHA256: sumOf("v2")}})
		Expect(failed).To(HaveKey("old"))
		Expect(failed).ToNot(HaveKey("a"))
		Expect(files.Commit(ctx, "tiny", []workerctl.ModelPrefetchFile{{Filename: "tiny.gguf"}})).To(Succeed(), "an old worker never fails the upgrade")
	})

	It("places the file in the object storage cache of a worker that has one, and evicts it on a rollback", func() {
		worker("a", true)
		newFiles([]string{"a"}, "a")
		want := []workerctl.ModelPrefetchFile{{Filename: "tiny.gguf", URI: server.URL + "/tiny.gguf", SHA256: sumOf("v2")}}
		Expect(files.Stage(ctx, "tiny", want)).To(BeEmpty())
		Expect(files.Commit(ctx, "tiny", want)).To(Succeed())
		Expect(read(cachePlace("a"))).To(Equal("v2"))

		files.Discard(ctx, "tiny", want, true)
		Expect(cachePlace("a")).ToNot(BeAnExistingFile())
		Expect(objs.deleted).To(Equal([]string{"models/tiny/tiny.gguf", "models/tiny/tiny.gguf"}))
	})

	It("loads the new file on a worker that missed the commit, and trusts a prefetched copy without hashing it", func() {
		worker("a", true)
		newFiles([]string{"a"}, "a", "missed")
		want := []workerctl.ModelPrefetchFile{{Filename: "tiny.gguf", URI: server.URL + "/tiny.gguf", SHA256: sumOf("v2")}}
		Expect(files.Stage(ctx, "tiny", want)).To(BeEmpty())
		Expect(files.Commit(ctx, "tiny", want)).To(Succeed())

		By("using the prefetched copy at the next load of worker a")
		store := &stagingObjectStore{payload: []byte("v2")}
		fmA, err := storage.NewFileManager(store, filepath.Join(roots["a"], "cache"))
		Expect(err).ToNot(HaveOccurred())
		got, err := ensureWorkerFile(ctx, fmA, nil, "models/tiny/tiny.gguf", sumOf("v2"))
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(cachePlace("a")))
		Expect(store.getCalls).To(BeZero())

		By("downloading again the old copy of a worker that never got the commit")
		missed := filepath.Join(GinkgoT().TempDir(), "cache")
		Expect(os.MkdirAll(filepath.Join(missed, "models", "tiny"), 0750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(missed, "models", "tiny", "tiny.gguf"), []byte("v1"), 0600)).To(Succeed())
		fmMissed, err := storage.NewFileManager(store, missed)
		Expect(err).ToNot(HaveOccurred())
		got, err = ensureWorkerFile(ctx, fmMissed, nil, "models/tiny/tiny.gguf", sumOf("v2"))
		Expect(err).ToNot(HaveOccurred())
		Expect(read(got)).To(Equal("v2"))
		Expect(store.getCalls).To(Equal(1))
	})

	It("drops only the staged copy when an upgrade fails before its commit", func() {
		worker("a", false)
		newFiles([]string{"a"}, "a")
		Expect(os.MkdirAll(filepath.Dir(place("a")), 0750)).To(Succeed())
		Expect(os.WriteFile(place("a"), []byte("v1"), 0600)).To(Succeed())
		want := []workerctl.ModelPrefetchFile{{Filename: "tiny.gguf", URI: server.URL + "/tiny.gguf", SHA256: sumOf("v2")}}
		Expect(files.Stage(ctx, "tiny", want)).To(BeEmpty())

		files.Discard(ctx, "tiny", want, false)
		Expect(place("a") + ".next").ToNot(BeAnExistingFile())
		Expect(read(place("a"))).To(Equal("v1"))
		Expect(objs.deleted).To(BeEmpty())
	})

	It("refuses a plain http URI that points into the network of the worker", func() {
		p := newModelPrefetcher(&Config{ModelsPath: GinkgoT().TempDir()})
		for _, uri := range []string{server.URL + "/tiny.gguf", "http://localhost/tiny.gguf", "http://169.254.169.254/latest"} {
			reply := p.serve(ctx, workerctl.ModelPrefetchRequest{Action: workerctl.PrefetchStage, ModelName: "tiny",
				Files: []workerctl.ModelPrefetchFile{{Filename: "tiny.gguf", URI: uri, SHA256: sumOf("v2")}}})
			Expect(reply.Success).To(BeFalse(), uri)
			Expect(reply.Error).To(ContainSubstring("does not download"), uri)
		}
	})

	It("uses one cache key for a file wherever it names it", func() {
		Expect(cacheKey("tiny", "sub/tiny.gguf")).To(Equal(storage.ModelKey("tiny/sub/tiny.gguf")))
	})

	It("refuses paths outside the model directory and files it cannot verify", func() {
		p := &modelPrefetcher{modelsPath: GinkgoT().TempDir(), download: downloadVerified}
		for _, f := range []workerctl.ModelPrefetchFile{
			{Filename: "../escape.gguf", URI: server.URL, SHA256: sumOf("v2")},
			{Filename: "tiny.gguf", URI: server.URL},
			{Filename: "tiny.gguf", URI: "file:///etc/passwd", SHA256: sumOf("v2")},
		} {
			reply := p.serve(ctx, workerctl.ModelPrefetchRequest{Action: workerctl.PrefetchStage, ModelName: "tiny", Files: []workerctl.ModelPrefetchFile{f}})
			Expect(reply.Success).To(BeFalse(), f.Filename+" "+f.URI)
		}
		reply := p.serve(ctx, workerctl.ModelPrefetchRequest{Action: "jump", ModelName: "tiny"})
		Expect(reply.Error).To(ContainSubstring("unknown action"))
	})
})
