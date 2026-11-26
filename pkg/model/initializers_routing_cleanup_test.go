// SPDX-License-Identifier: MIT
package model

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type stubRemoteBackend struct{ grpc.Backend }

func (*stubRemoteBackend) HealthCheck(context.Context) (bool, error) { return true, nil }
func (*stubRemoteBackend) IsBusy() bool                              { return false }
func (*stubRemoteBackend) Free(context.Context) error                { return nil }

// clusterStubStore mirrors DistributedModelStore: Get checks only the local
// cache, while Range also reports replicas known to the cluster registry.
type clusterStubStore struct {
	local   *InMemoryModelStore
	cluster map[string]*Model
}

func (s *clusterStubStore) Get(id string) (*Model, bool) {
	return s.local.Get(id)
}

func (s *clusterStubStore) Set(id string, m *Model) {
	s.local.Set(id, m)
}

func (s *clusterStubStore) Delete(id string) {
	s.local.Delete(id)
}

func (s *clusterStubStore) Range(fn func(string, *Model) bool) {
	seen := make(map[string]bool)
	stopped := false
	s.local.Range(func(id string, m *Model) bool {
		seen[id] = true
		if !fn(id, m) {
			stopped = true
			return false
		}
		return true
	})
	if stopped {
		return
	}
	for id, m := range s.cluster {
		if seen[id] {
			continue
		}
		if !fn(id, m) {
			return
		}
	}
}

var _ = Describe("backendLoader cleanup after a failed load or route", func() {
	Context("in distributed mode", func() {
		It("does not stop a resident model across nodes when routing fails", func() {
			ml := NewModelLoader(&system.SystemState{})
			unloader := &recordingRemoteUnloader{}
			ml.SetRemoteUnloader(unloader)

			var calls atomic.Int32
			ml.SetModelRouter(func(_ context.Context, _, id, _, _, _ string, _ *pb.ModelOptions, _ bool) (*Model, error) {
				if calls.Add(1) == 1 {
					return NewModelWithClient(id, "slice-a:1", &stubRemoteBackend{}), nil
				}
				return nil, fmt.Errorf("failed to increment in-flight counter: %w", context.Canceled)
			})

			_, err := ml.Load(WithModelID("silero-vad-ggml"), WithBackendString("whisper"), WithModel("silero-vad.gguf"))
			Expect(err).ToNot(HaveOccurred())
			Expect(ml.isResident("silero-vad-ggml")).To(BeTrue())

			_, err = ml.Load(WithModelID("silero-vad-ggml"), WithBackendString("whisper"), WithModel("silero-vad.gguf"))
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, context.Canceled)).To(BeTrue())

			Expect(unloader.calls()).To(BeEmpty(),
				"a failed routing attempt for a resident model must not trigger a cluster-wide remote unload")
			Expect(ml.isResident("silero-vad-ggml")).To(BeTrue(),
				"the resident local stub must remain intact after a transient routing failure")
		})

		It("does not stop cluster-resident replicas when the local cache has no stub yet", func() {
			ml := NewModelLoader(&system.SystemState{})
			unloader := &recordingRemoteUnloader{}
			ml.SetRemoteUnloader(unloader)
			ml.SetModelStore(&clusterStubStore{
				local: NewInMemoryModelStore(),
				cluster: map[string]*Model{
					"silero-vad-ggml": NewModelWithClient("silero-vad-ggml", "slice-b:1", &stubRemoteBackend{}),
				},
			})
			ml.SetModelRouter(func(_ context.Context, _, _, _, _, _ string, _ *pb.ModelOptions, _ bool) (*Model, error) {
				return nil, context.Canceled
			})

			Expect(ml.isResident("silero-vad-ggml")).To(BeFalse())

			_, err := ml.Load(WithModelID("silero-vad-ggml"), WithBackendString("whisper"), WithModel("silero-vad.gguf"))
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, context.Canceled)).To(BeTrue())

			Expect(unloader.calls()).To(BeEmpty(),
				"a failed distributed route on a cold frontend must not stop models already running on workers")
		})
	})

	Context("in single-node mode", func() {
		It("still runs defensive StopGRPC cleanup after a failed cold load", func() {
			systemState, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
			Expect(err).ToNot(HaveOccurred())
			ml := NewModelLoader(systemState)

			// Simulate a leftover entry in the local store produced during a failed cold load.
			ml.SetLoadObserver(func(ev BackendLoadEvent) {
				if ev.Err != nil {
					ml.mu.Lock()
					ml.store.Set(ev.ModelID, NewModelWithClient(ev.ModelID, "127.0.0.1:65535", &stubRemoteBackend{}))
					ml.mu.Unlock()
				}
			})

			var unloaded []string
			ml.OnModelUnload(func(modelName string) {
				unloaded = append(unloaded, modelName)
			})

			_, err = ml.Load(
				WithModelID("cold-local"),
				WithBackendString("definitely-not-an-installed-backend"),
				WithModel("cold-local.gguf"),
			)
			Expect(err).To(HaveOccurred())
			Expect(ml.isResident("cold-local")).To(BeFalse(),
				"defensive cleanup after a failed local cold load must still remove leftover entries")
			Expect(unloaded).To(ConsistOf("cold-local"))
		})

		It("does not stop an already-resident local model when a load is refused", func() {
			systemState, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
			Expect(err).ToNot(HaveOccurred())
			ml := NewModelLoader(systemState)

			resident := NewModelWithClient("local-resident", "127.0.0.1:65534", &stubRemoteBackend{})
			resident.MarkHealthy()
			ml.mu.Lock()
			ml.store.Set("local-resident", resident)
			ml.mu.Unlock()

			ml.BlockLoads("local-resident")
			DeferCleanup(func() { ml.UnblockLoads("local-resident") })

			var unloaded []string
			ml.OnModelUnload(func(modelName string) {
				unloaded = append(unloaded, modelName)
			})

			_, err = ml.backendLoader(
				WithModelID("local-resident"),
				WithBackendString("llama-cpp"),
				WithModel("local-resident.gguf"),
			)
			Expect(err).To(HaveOccurred())
			var blockedErr *ModelLoadsBlockedError
			Expect(errors.As(err, &blockedErr)).To(BeTrue())
			Expect(ml.isResident("local-resident")).To(BeTrue(),
				"a refused load must not tear down an already-resident local model")
			Expect(unloaded).To(BeEmpty())
		})
	})
})
