package worker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// The frontend's S3 file stager against the REAL worker file-staging routes: a
// real object store both sides share, the real handlers mounted through the
// real nodes.AuthenticatedRoutes so the real bearer check runs first, reached
// by a real nodes.ControlClient over a real HTTP transport.
//
// It lives in this package for the same reason the control-client roundtrip
// does: the dependency runs worker -> nodes, so the frontend half cannot be
// exercised against the real handler from the other side without an import
// cycle. A spec on either side alone proves only that side agrees with itself;
// the path literals, the JSON shapes and the status codes are pinned together
// only here.
var _ = Describe("the frontend's file stager against the real worker", func() {
	const (
		token  = "s3cret-registration-token"
		nodeID = "staging-worker"
	)

	var (
		stager    *nodes.S3FileStager
		store     *storage.FilesystemStore
		workerFM  *storage.FileManager
		modelsDir string
		srvAddr   string
	)

	newStager := func(tok string) *nodes.S3FileStager {
		GinkgoHelper()
		frontendFM, err := storage.NewFileManager(store, GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		control := nodes.NewControlClient(func(string) func(context.Context, string, string) (net.Conn, error) {
			return func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", srvAddr)
			}
		}, tok)
		return nodes.NewS3FileStager(frontendFM, control)
	}

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		modelsDir = filepath.Join(dir, "worker", "models")
		Expect(os.MkdirAll(modelsDir, 0o750)).To(Succeed())

		var err error
		store, err = storage.NewFilesystemStore(filepath.Join(dir, "objectstore"))
		Expect(err).NotTo(HaveOccurred())

		cfg := &Config{ModelsPath: modelsDir}
		workerFM, err = storage.NewFileManager(store, filepath.Join(dir, "worker", "cache"))
		Expect(err).NotTo(HaveOccurred())

		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		srv, err := nodes.StartFileTransferServerWithRoutes(lis,
			filepath.Join(dir, "worker", "staging"), modelsDir, filepath.Join(dir, "worker", "data"),
			token, config.DefaultMaxUploadSize, nil,
			&nodes.AuthenticatedRoutes{
				Prefix: workerctl.Prefix,
				Register: func(mux *http.ServeMux) {
					cfg.RegisterFileControlRoutes(mux, workerFM)
				},
			})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = srv.Close() })

		srvAddr = lis.Addr().String()
		stager = newStager(token)
	})

	It("puts a frontend file in the store and has the worker fetch it", func() {
		local := filepath.Join(GinkgoT().TempDir(), "model.gguf")
		Expect(os.WriteFile(local, []byte("checkpoint bytes"), 0o600)).To(Succeed())

		remote, err := stager.EnsureRemote(context.Background(), nodeID, local, storage.ModelKey("rt/model.gguf"))
		Expect(err).NotTo(HaveOccurred())
		Expect(remote).To(BeAnExistingFile())
		Expect(os.ReadFile(remote)).To(Equal([]byte("checkpoint bytes")))
	})

	It("allocates a temp file on the worker", func() {
		remote, err := stager.AllocRemoteTemp(context.Background(), nodeID)
		Expect(err).NotTo(HaveOccurred())
		Expect(remote).To(BeAnExistingFile())
	})

	It("allocates and downloads into the SAME cache directory", func() {
		// The staging cache root is derived once and read by two things: the
		// FileManager caches downloads into it, and the temp verb allocates
		// inside it. Two derivations that drifted would each stay self
		// consistent, so nothing else in this suite would notice; what would
		// notice is an operator whose disk budget covers one of the two.
		local := filepath.Join(GinkgoT().TempDir(), "same-root.gguf")
		Expect(os.WriteFile(local, []byte("bytes"), 0o600)).To(Succeed())
		downloaded, err := stager.EnsureRemote(context.Background(), nodeID, local, storage.ModelKey("root/x.gguf"))
		Expect(err).NotTo(HaveOccurred())

		tmp, err := stager.AllocRemoteTemp(context.Background(), nodeID)
		Expect(err).NotTo(HaveOccurred())

		cacheRoot := filepath.Join(filepath.Dir(modelsDir), "cache")
		Expect(downloaded).To(HavePrefix(cacheRoot + string(filepath.Separator)))
		Expect(tmp).To(HavePrefix(cacheRoot + string(filepath.Separator)))
	})

	It("stages a worker file into the store", func() {
		remote := filepath.Join(modelsDir, "result.bin")
		Expect(os.WriteFile(remote, []byte("job output"), 0o600)).To(Succeed())

		Expect(stager.StageRemoteToStore(context.Background(), nodeID, remote, "data/rt/result.bin")).To(Succeed())
		exists, err := store.Exists(context.Background(), "data/rt/result.bin")
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeTrue())
	})

	It("fetches a worker file back through the store", func() {
		remote := filepath.Join(modelsDir, "fetched.bin")
		Expect(os.WriteFile(remote, []byte("fetch me"), 0o600)).To(Succeed())
		dst := filepath.Join(GinkgoT().TempDir(), "local.bin")

		Expect(stager.FetchRemote(context.Background(), nodeID, remote, dst)).To(Succeed())
		Expect(os.ReadFile(dst)).To(Equal([]byte("fetch me")))
	})

	It("lists a worker directory whose listing outgrows any bus payload", func() {
		big := filepath.Join(modelsDir, "wide")
		Expect(os.MkdirAll(big, 0o750)).To(Succeed())
		for i := range 4000 {
			name := fmt.Sprintf("shard-%030d.safetensors", i)
			Expect(os.WriteFile(filepath.Join(big, name), []byte("x"), 0o600)).To(Succeed())
		}

		files, err := stager.ListRemoteDir(context.Background(), nodeID, "models/wide")
		Expect(err).NotTo(HaveOccurred())
		Expect(files).To(HaveLen(4000))
	})

	It("reports the worker's own refusal as the worker's answer", func() {
		_, err := stager.ListRemoteDir(context.Background(), nodeID, "../../../etc")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("backend listdir failed"))
		// It said something about a file, not about the route, so it must not
		// be wearing the umbrella that stops a caller acting on it.
		Expect(errors.Is(err, nodes.ErrWorkerUnroutable)).To(BeFalse())
	})

	// One rule, stated once per verb: every file-staging RPC goes through the
	// control client, so a 401 is a failure of the ROUTE and never the worker
	// saying a file is not there. Pinned at each verb because each verb writes
	// the call out for itself, and a single verb that reached the worker some
	// other way would still leave the other five green.
	DescribeTable("reports a rejected token as unroutable, never as a verdict about a file",
		func(call func(*nodes.S3FileStager) error) {
			wrong := newStager("not-the-token")
			err := call(wrong)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, nodes.ErrWorkerUnroutable)).To(BeTrue(), "got %v", err)
			Expect(cluster.IsWorkerAnswer(err)).To(BeFalse())
			Expect(errors.Is(err, nodes.ErrWorkerControlUnsupported)).To(BeFalse())
		},
		Entry("ensure", func(s *nodes.S3FileStager) error {
			local := filepath.Join(GinkgoT().TempDir(), "m.gguf")
			Expect(os.WriteFile(local, []byte("x"), 0o600)).To(Succeed())
			_, err := s.EnsureRemote(context.Background(), nodeID, local, storage.ModelKey("auth/m.gguf"))
			return err
		}),
		Entry("temp", func(s *nodes.S3FileStager) error {
			_, err := s.AllocRemoteTemp(context.Background(), nodeID)
			return err
		}),
		Entry("listdir", func(s *nodes.S3FileStager) error {
			_, err := s.ListRemoteDir(context.Background(), nodeID, "models/wide")
			return err
		}),
		Entry("stage to store", func(s *nodes.S3FileStager) error {
			return s.StageRemoteToStore(context.Background(), nodeID, filepath.Join(modelsDir, "x"), "data/x")
		}),
		Entry("fetch", func(s *nodes.S3FileStager) error {
			return s.FetchRemote(context.Background(), nodeID, filepath.Join(modelsDir, "x"),
				filepath.Join(GinkgoT().TempDir(), "out"))
		}),
		Entry("fetch by key", func(s *nodes.S3FileStager) error {
			return s.FetchRemoteByKey(context.Background(), nodeID, "data/x",
				filepath.Join(GinkgoT().TempDir(), "out"))
		}),
	)
})
