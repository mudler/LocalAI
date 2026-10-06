package worker

import (
	"context"
	"fmt"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/workerctl"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"io"
	"time"
)

// Loop back the real adapter wire requests into the registered worker verbs.
type recoveryLoopback struct{ *recordingBus }

func (b recoveryLoopback) Request(subject string, data []byte, timeout time.Duration) ([]byte, error) {
	b.mu.Lock()
	h := b.handlers[subject]
	b.mu.Unlock()
	if h == nil {
		return nil, fmt.Errorf("no handler: %s", subject)
	}
	replies := make(chan []byte, 1)
	h(data, func(reply []byte) { replies <- reply })
	select {
	case reply := <-replies:
		return reply, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout")
	}
}

var _ = Describe("Linked revision cleanup", func() {
	It("installs through the adapter then stops the actual worker process at the same revision", func() {
		proc := startModelStopProcess()
		terminated := false
		DeferCleanup(func() {
			// The successful stop below already removes the temporary PID file.
			if !terminated {
				Expect(proc.Stop()).To(Succeed())
			}
		})
		s := &backendSupervisor{cfg: &Config{}, nodeID: "worker", processes: map[string]*backendProcess{"model#0": {proc: proc, addr: "127.0.0.1:1", instance: "instance"}}}
		// Use real installBackend's reuse path, including its backend directory check.
		bp := s.processes["model#0"]
		bp.backendName = "backend"
		bp.backendDir = GinkgoT().TempDir()
		bus := newRecordingBus()
		Expect(registerLifecycleForTest(s, bus)).To(Succeed())
		adapter := nodes.NewRemoteUnloaderAdapter(nil, recoveryLoopback{bus}, time.Second, time.Second)
		reply, err := adapter.InstallBackend("worker", "backend", "model", "[]", "", "", "", 0, "", nil, "revision")
		Expect(err).NotTo(HaveOccurred())
		Expect(reply.Success).To(BeTrue(), reply.Error)
		Expect(bp.revision).To(Equal("revision"))
		replica := nodes.NodeModel{ModelName: "model", Address: bp.addr, ConfigRevision: "wrong"}
		stopped, err := adapter.StopModelReplica(context.Background(), "worker", replica, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(stopped.Error).NotTo(BeEmpty())
		Expect(stopped.Terminated).To(BeFalse())
		Expect(proc.IsAlive()).To(BeTrue())
		replica.ConfigRevision = "revision"
		stopped, err = adapter.StopModelReplica(context.Background(), "worker", replica, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(stopped.Terminated).To(BeTrue())
		Expect(proc.IsAlive()).To(BeFalse())
		terminated = true
		Expect(stopped.OperationAcknowledged).To(BeFalse())
	})
})

type observedStagingStore struct {
	stagingObjectStore
	observe func()
}

func (s *observedStagingStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	s.observe()
	return s.stagingObjectStore.Get(ctx, key)
}
func (s *observedStagingStore) Put(context.Context, string, io.Reader) error { s.observe(); return nil }

var _ = Describe("Linked legacy staging inventory", func() {
	It("reports both legacy file handlers while object storage work is executing", func() {
		s := &backendSupervisor{processes: map[string]*backendProcess{}}
		observed := 0
		store := &observedStagingStore{observe: func() {
			inv := s.modelsRunning(context.Background(), workerctl.ModelsRunningRequest{})
			Expect(inv.ReportsOperations).To(BeTrue())
			Expect(inv.Operations).To(HaveLen(1))
			Expect(inv.Operations[0].Phase).To(Equal("legacy-stage"))
			Expect(inv.Operations[0].Active).To(BeTrue())
			Expect(inv.Operations[0].Identity.Generation).To(BeEmpty())
			observed++
		}}
		dir := GinkgoT().TempDir()
		fm, err := storage.NewFileManager(store, dir)
		Expect(err).NotTo(HaveOccurred())
		v := &fileStagingVerbs{cfg: &Config{}, supervisor: s, fm: fm, cacheDir: dir}
		ensured := v.ensure(context.Background(), workerctl.FileEnsureRequest{Key: "object"})
		Expect(ensured.Error).To(BeEmpty())
		staged := v.stage(context.Background(), workerctl.FileStageRequest{Key: "upload", LocalPath: ensured.LocalPath})
		Expect(staged.Error).To(BeEmpty())
		Expect(observed).To(Equal(2))
		Expect(s.modelsRunning(context.Background(), workerctl.ModelsRunningRequest{}).Operations).To(BeEmpty())
	})
})

var _ = Describe("Linked absent-target recovery", func() {
	It("preserves another model through adapter unload", func() {
		backend := &modelStopBackend{}
		addr, _, stop := startModelStopBackend(backend)
		defer stop()
		s := &backendSupervisor{cfg: &Config{}, nodeID: "worker", processes: map[string]*backendProcess{"other#0": {addr: addr}}}
		bus := newRecordingBus()
		Expect(registerLifecycleForTest(s, bus)).To(Succeed())
		adapter := nodes.NewRemoteUnloaderAdapter(nil, recoveryLoopback{bus}, time.Second, time.Second)
		Expect(adapter.UnloadModelOnNode("worker", "missing")).To(Succeed())
		Expect(backend.freeCalls.Load()).To(BeZero())
		Expect(s.processes).To(HaveKey("other#0"))
	})
})
