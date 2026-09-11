package nodes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// The S3 file stager's half of the file-staging contract: which verb each
// method addresses, and whose budget bounds it.
//
// The stager is reached through the real ControlClient over a real HTTP
// transport onto a scripted worker, because what these specs are about is
// transport behaviour: a double that never dials anything cannot fail the way
// a spent budget or a rejected route fails.
var _ = Describe("the S3 file stager's control RPCs", func() {
	const nodeID = "stager-node"

	var (
		workers *scriptedControlWorkers
		stager  *S3FileStager
		local   string
	)

	BeforeEach(func() {
		workers = newScriptedControlWorkers()

		dir := GinkgoT().TempDir()
		store, err := storage.NewFilesystemStore(filepath.Join(dir, "objectstore"))
		Expect(err).NotTo(HaveOccurred())
		fm, err := storage.NewFileManager(store, filepath.Join(dir, "cache"))
		Expect(err).NotTo(HaveOccurred())
		stager = NewS3FileStager(fm, workers.controlClient())

		local = filepath.Join(dir, "model.gguf")
		Expect(os.WriteFile(local, []byte("weights"), 0o600)).To(Succeed())
	})

	// The budgets are written out BY HAND and deliberately not derived from the
	// constants under test. They are the timeouts the NATS request-reply calls
	// carried, and keeping them is a compatibility fact rather than a taste:
	// an operator whose staging of a 35 GB checkpoint fits inside ten minutes
	// today must not find it cut to thirty seconds by the carrier changing.
	DescribeTable("gives each verb the ceiling its NATS timeout carried",
		func(path string, want time.Duration) { Expect(fileRPCBudget(path)).To(Equal(want)) },
		Entry("ensure moves bytes", workerctl.PathFilesEnsure, 10*time.Minute),
		Entry("stage moves bytes", workerctl.PathFilesStage, 10*time.Minute),
		Entry("temp is metadata", workerctl.PathFilesTemp, 30*time.Second),
		Entry("listdir is metadata", workerctl.PathFilesListDir, 30*time.Second),
	)

	It("gives a verb it does not know the shorter ceiling", func() {
		// The safe direction: a verb wrongly given thirty seconds fails visibly
		// and is retried, while one wrongly given ten minutes parks a caller on
		// a verb that was never meant to be slow.
		Expect(fileRPCBudget(workerctl.Prefix + "invented")).To(Equal(30 * time.Second))
	})

	DescribeTable("addresses the verb that verb's path names",
		func(path string, reply any, call func(*S3FileStager) error) {
			workers.scriptReply(controlKey(nodeID, path), reply)
			Expect(call(stager)).To(Succeed())
			Expect(workers.callSubjects()).To(ContainElement(controlKey(nodeID, path)))
		},
		Entry("ensure", workerctl.PathFilesEnsure, fileEnsureReply{LocalPath: "/w/models/m.gguf"},
			func(s *S3FileStager) error {
				_, err := s.EnsureRemote(context.Background(), nodeID, local, storage.ModelKey("m.gguf"))
				return err
			}),
		Entry("temp", workerctl.PathFilesTemp, fileTempReply{LocalPath: "/w/tmp/x"},
			func(s *S3FileStager) error {
				_, err := s.AllocRemoteTemp(context.Background(), nodeID)
				return err
			}),
		Entry("listdir", workerctl.PathFilesListDir, fileListDirReply{Files: []string{"a", "b"}},
			func(s *S3FileStager) error {
				_, err := s.ListRemoteDir(context.Background(), nodeID, "models/m")
				return err
			}),
		Entry("stage", workerctl.PathFilesStage, fileStageReply{Key: "data/out"},
			func(s *S3FileStager) error {
				return s.StageRemoteToStore(context.Background(), nodeID, "/w/models/out", "data/out")
			}),
	)

	// THE rule of this change, and it is written out at five separate call
	// sites: the RPC's budget is DERIVED FROM the caller's context, never
	// started fresh from a background one. A site that started fresh would keep
	// commanding a worker after its caller had given up, and would report the
	// worker's late answer as a live one. Each site is pinned on its own,
	// because five sites behind one spec is four sites nothing holds.
	DescribeTable("never reaches the worker once the caller's context is spent",
		func(call func(context.Context, *S3FileStager) error) {
			// Every verb is scripted to answer, so the ONLY thing that can stop
			// the call is the caller's own spent context.
			for _, p := range []string{
				workerctl.PathFilesEnsure, workerctl.PathFilesStage,
				workerctl.PathFilesTemp, workerctl.PathFilesListDir,
			} {
				workers.scriptRawReply(controlKey(nodeID, p), []byte(`{}`))
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			err := call(ctx, stager)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, context.Canceled)).To(BeTrue(), "got %v", err)
			// An expiry is never evidence about a file, so it must arrive
			// wearing the umbrella that stops a caller acting on it.
			Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeTrue(), "got %v", err)
			Expect(cluster.IsWorkerAnswer(err)).To(BeFalse())
			Expect(workers.callSubjects()).To(BeEmpty())
		},
		Entry("ensure", func(ctx context.Context, s *S3FileStager) error {
			_, err := s.EnsureRemote(ctx, nodeID, local, storage.ModelKey("m.gguf"))
			return err
		}),
		Entry("temp", func(ctx context.Context, s *S3FileStager) error {
			_, err := s.AllocRemoteTemp(ctx, nodeID)
			return err
		}),
		Entry("listdir", func(ctx context.Context, s *S3FileStager) error {
			_, err := s.ListRemoteDir(ctx, nodeID, "models/m")
			return err
		}),
		Entry("stage to store", func(ctx context.Context, s *S3FileStager) error {
			return s.StageRemoteToStore(ctx, nodeID, "/w/models/out", "data/out")
		}),
		Entry("fetch", func(ctx context.Context, s *S3FileStager) error {
			return s.FetchRemote(ctx, nodeID, "/w/models/out", filepath.Join(GinkgoT().TempDir(), "dst"))
		}),
		Entry("fetch by key", func(ctx context.Context, s *S3FileStager) error {
			return s.FetchRemoteByKey(ctx, nodeID, "data/out", filepath.Join(GinkgoT().TempDir(), "dst"))
		}),
	)

	// The other half of the same distinction, also at every site: what the
	// WORKER said comes back as the worker's answer, so a caller may act on it,
	// and it must not be dressed up as a route failure.
	DescribeTable("reports the worker's own refusal as the worker's answer",
		func(path string, call func(*S3FileStager) error) {
			workers.scriptRawReply(controlKey(nodeID, path), []byte(`{"error":"no space left on device"}`))
			err := call(stager)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no space left on device"))
			Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeFalse(), "got %v", err)
		},
		Entry("ensure", workerctl.PathFilesEnsure, func(s *S3FileStager) error {
			_, err := s.EnsureRemote(context.Background(), nodeID, local, storage.ModelKey("m.gguf"))
			return err
		}),
		Entry("temp", workerctl.PathFilesTemp, func(s *S3FileStager) error {
			_, err := s.AllocRemoteTemp(context.Background(), nodeID)
			return err
		}),
		Entry("listdir", workerctl.PathFilesListDir, func(s *S3FileStager) error {
			_, err := s.ListRemoteDir(context.Background(), nodeID, "models/m")
			return err
		}),
		Entry("stage to store", workerctl.PathFilesStage, func(s *S3FileStager) error {
			return s.StageRemoteToStore(context.Background(), nodeID, "/w/models/out", "data/out")
		}),
		Entry("fetch", workerctl.PathFilesStage, func(s *S3FileStager) error {
			return s.FetchRemote(context.Background(), nodeID, "/w/models/out",
				filepath.Join(GinkgoT().TempDir(), "dst"))
		}),
	)

	// A worker too old to serve a file verb answers 404. That is a DEPLOYMENT
	// fact about the worker's build and says nothing about the file, so it must
	// not reach a caller as "that file is not there".
	It("reports a worker that serves no file verbs as unsupported, not as an absent file", func() {
		workers.scriptUnsupported(controlKey(nodeID, workerctl.PathFilesListDir))
		_, err := stager.ListRemoteDir(context.Background(), nodeID, "models/m")
		Expect(err).To(MatchError(ErrWorkerControlUnsupported))
		Expect(cluster.IsWorkerAnswer(err)).To(BeFalse())
	})

	It("reports a worker it has no route to as unroutable", func() {
		workers.scriptUnroutable(nodeID)
		_, err := stager.AllocRemoteTemp(context.Background(), nodeID)
		Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeTrue(), "got %v", err)
		Expect(cluster.IsWorkerAnswer(err)).To(BeFalse())
	})
})
