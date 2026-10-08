package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// scriptedWorker is a worker whose answers to the file verbs a spec scripts. A
// rig makes it reachable by one carrier.
type scriptedWorker struct {
	mu       sync.Mutex
	replies  map[string]any // verb -> reply
	requests map[string][]json.RawMessage
}

func newScriptedWorker() *scriptedWorker {
	return &scriptedWorker{replies: map[string]any{}, requests: map[string][]json.RawMessage{}}
}

func (w *scriptedWorker) on(verb string, reply any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.replies[verb] = reply
}

// answer records a request and returns the scripted reply as JSON.
func (w *scriptedWorker) answer(verb string, body []byte) []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.requests[verb] = append(w.requests[verb], append(json.RawMessage(nil), body...))
	reply, ok := w.replies[verb]
	if !ok {
		reply = struct{}{}
	}
	raw, _ := json.Marshal(reply)
	return raw
}

func (w *scriptedWorker) seen(verb string) []json.RawMessage {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]json.RawMessage(nil), w.requests[verb]...)
}

// natsRig reaches the scripted worker over NATS request and reply.
type natsRig struct {
	releaseTestMessaging
	worker  *scriptedWorker
	noRoute bool
}

func (r *natsRig) Request(subject string, data []byte, _ time.Duration) ([]byte, error) {
	if r.noRoute {
		return nil, nats.ErrNoResponders
	}
	for _, verb := range []string{workerctl.VerbFilesEnsure, workerctl.VerbFilesStage, workerctl.VerbFilesTemp, workerctl.VerbFilesListDir, workerctl.VerbFilesRelease} {
		if subject == natsSubject("n1", verb) {
			return r.worker.answer(verb, data), nil
		}
	}
	return nil, fmt.Errorf("the rig has no verb for %s", subject)
}

var _ = Describe("The S3 file stager over each carrier of the file verbs", func() {
	type rig struct {
		name    string
		build   func(fm *storage.FileManager, w *scriptedWorker) (FileStager, func(noRoute bool))
		release func(FileStager) RequestFileReleaser
	}

	rigs := []rig{
		{
			name: "NATS",
			build: func(fm *storage.FileManager, w *scriptedWorker) (FileStager, func(bool)) {
				bus := &natsRig{worker: w}
				return NewS3NATSFileStager(fm, bus), func(noRoute bool) { bus.noRoute = noRoute }
			},
		},
		{
			name: "the HTTP control plane of the tunnel",
			build: func(fm *storage.FileManager, w *scriptedWorker) (FileStager, func(bool)) {
				worker := newControlWorker()
				for _, verb := range []string{workerctl.VerbFilesEnsure, workerctl.VerbFilesStage, workerctl.VerbFilesTemp, workerctl.VerbFilesListDir, workerctl.VerbFilesRelease} {
					worker.on(verb, func(rw http.ResponseWriter, r *http.Request) {
						body := make([]byte, 0, 256)
						buf := make([]byte, 256)
						for {
							n, err := r.Body.Read(buf)
							body = append(body, buf[:n]...)
							if err != nil {
								break
							}
						}
						rw.Header().Set("Content-Type", "application/json")
						_, _ = rw.Write(w.answer(strings.ReplaceAll(strings.TrimPrefix(r.URL.Path, workerctl.Prefix), "/", "."), body))
					})
				}
				stager := NewS3TunnelFileStager(fm, NewControlClient(worker.dialerFor(), "token"))
				return stager, func(noRoute bool) {
					if noRoute {
						worker.failDial(func(context.Context) error { return ErrNoRoute })
					} else {
						worker.failDial(nil)
					}
				}
			},
		},
	}

	for _, r := range rigs {
		Describe(r.name, func() {
			var (
				ctx     context.Context
				store   storage.ObjectStore
				fm      *storage.FileManager
				worker  *scriptedWorker
				stager  FileStager
				setDown func(bool)
			)

			BeforeEach(func() {
				ctx = context.Background()
				var err error
				store, err = storage.NewFilesystemStore(GinkgoT().TempDir())
				Expect(err).ToNot(HaveOccurred())
				fm, err = storage.NewFileManager(store, GinkgoT().TempDir())
				Expect(err).ToNot(HaveOccurred())
				worker = newScriptedWorker()
				stager, setDown = r.build(fm, worker)
			})

			It("uploads a file and asks the worker to ensure it, and returns the path the worker gives", func() {
				local := GinkgoT().TempDir() + "/in.bin"
				Expect(writeFile(local, "payload")).To(Succeed())
				worker.on(workerctl.VerbFilesEnsure, workerctl.FileEnsureReply{LocalPath: "/cache/in.bin"})

				path, err := stager.EnsureRemote(ctx, "n1", local, "ephemeral/r1/in.bin")
				Expect(err).ToNot(HaveOccurred())
				Expect(path).To(Equal("/cache/in.bin"))

				exists, err := store.Exists(ctx, "ephemeral/r1/in.bin")
				Expect(err).ToNot(HaveOccurred())
				Expect(exists).To(BeTrue())
				Expect(worker.seen(workerctl.VerbFilesEnsure)).To(HaveLen(1))
				Expect(string(worker.seen(workerctl.VerbFilesEnsure)[0])).To(MatchJSON(`{"key":"ephemeral/r1/in.bin"}`))
			})

			It("reports a refusal of the worker as an error that is not a missing route", func() {
				local := GinkgoT().TempDir() + "/in.bin"
				Expect(writeFile(local, "payload")).To(Succeed())
				worker.on(workerctl.VerbFilesEnsure, workerctl.FileEnsureReply{Error: "disk full"})

				_, err := stager.EnsureRemote(ctx, "n1", local, "k1")
				Expect(err).To(MatchError(ContainSubstring("disk full")))
				Expect(errors.Is(err, ErrNoRoute)).To(BeFalse())
			})

			It("reports a node that nothing can reach as ErrNoRoute", func() {
				local := GinkgoT().TempDir() + "/in.bin"
				Expect(writeFile(local, "payload")).To(Succeed())
				setDown(true)
				_, err := stager.EnsureRemote(ctx, "n1", local, "k1")
				Expect(errors.Is(err, ErrNoRoute)).To(BeTrue(), "got %v", err)
				_, err = stager.AllocRemoteTemp(ctx, "n1")
				Expect(errors.Is(err, ErrNoRoute)).To(BeTrue(), "got %v", err)
			})

			It("allocates a temp file, lists a directory and stages a file to the store through the worker", func() {
				worker.on(workerctl.VerbFilesTemp, workerctl.FileTempReply{LocalPath: "/tmp/x"})
				worker.on(workerctl.VerbFilesListDir, workerctl.FileListDirReply{Files: []string{"a", "b"}})
				path, err := stager.AllocRemoteTemp(ctx, "n1")
				Expect(err).ToNot(HaveOccurred())
				Expect(path).To(Equal("/tmp/x"))
				files, err := stager.ListRemoteDir(ctx, "n1", "data/")
				Expect(err).ToNot(HaveOccurred())
				Expect(files).To(Equal([]string{"a", "b"}))

				Expect(stager.StageRemoteToStore(ctx, "n1", "/data/out.bin", "out/key")).To(Succeed())
				Expect(string(worker.seen(workerctl.VerbFilesStage)[0])).To(MatchJSON(`{"local_path":"/data/out.bin","key":"out/key"}`))
			})

			It("fetches a file by having the worker stage it to the store, then reading it from there", func() {
				Expect(store.Put(ctx, "out/key", strings.NewReader("result"))).To(Succeed())
				dst := GinkgoT().TempDir() + "/dst.bin"
				Expect(stager.FetchRemoteByKey(ctx, "n1", "out/key", dst)).To(Succeed())
				got, err := readFile(dst)
				Expect(err).ToNot(HaveOccurred())
				Expect(got).To(Equal("result"))
				Expect(worker.seen(workerctl.VerbFilesStage)).To(HaveLen(1))
			})

			It("evicts the worker's copy before it deletes the shared object, in one round trip for a request", func() {
				keys := []string{"ephemeral/audio/r1/in.wav", "ephemeral/images/r1/f.jpg"}
				for _, k := range keys {
					Expect(store.Put(ctx, k, strings.NewReader("x"))).To(Succeed())
				}
				releaser, ok := stager.(RequestFileReleaser)
				Expect(ok).To(BeTrue())
				Expect(releaser.ReleaseRemoteRequest(ctx, "n1", "r1", keys)).To(Succeed())

				Expect(worker.seen(workerctl.VerbFilesRelease)).To(HaveLen(1))
				Expect(string(worker.seen(workerctl.VerbFilesRelease)[0])).To(MatchJSON(`{"request_id":"r1"}`))
				for _, k := range keys {
					exists, _ := store.Exists(ctx, k)
					Expect(exists).To(BeFalse(), k)
				}
			})
		})
	}
})

var _ messaging.MessagingClient = (*natsRig)(nil)

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}
