package model

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// storeEvicter drops a model from the loader store the way a real shutdown
// does, and can be told to fail like a stop that cannot get the model's
// lifecycle lock.
type storeEvicter struct {
	ml       *ModelLoader
	mu       sync.Mutex
	evicted  []string
	failures map[string]error
}

func (s *storeEvicter) setFailure(name string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failures == nil {
		s.failures = map[string]error{}
	}
	if err == nil {
		delete(s.failures, name)
		return
	}
	s.failures[name] = err
}

func (s *storeEvicter) ShutdownModel(name string) error {
	s.mu.Lock()
	s.evicted = append(s.evicted, name)
	failure := s.failures[name]
	s.mu.Unlock()
	if failure != nil {
		return failure
	}
	s.ml.mu.Lock()
	s.ml.store.Delete(name)
	s.ml.mu.Unlock()
	return nil
}

func (s *storeEvicter) ShutdownModelForce(name string) error { return s.ShutdownModel(name) }

func (s *storeEvicter) names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.evicted...)
}

// Load must evict a resident model of the same concurrency group BEFORE the
// requested model is started, and must not forget a resident model it failed
// to stop. This drives the real Load path.
var _ = Describe("Load with concurrency groups", func() {
	const (
		gemma    = "gemma-4-26b-a4b-it"
		nemotron = "NVIDIA-Nemotron-3.5-Lightning-30B-A3B-UD-Q4_K_XL"
		address  = "127.0.0.1:65001"
	)

	var (
		ml   *ModelLoader
		wd   *WatchDog
		evic *storeEvicter
	)

	load := func() error {
		_, err := ml.Load(
			WithContext(context.Background()),
			WithModelID(nemotron),
			WithModel("nemotron.gguf"),
			WithBackendString("fakebackend"),
			WithGRPCAttempts(1),
			WithGRPCAttemptsDelay(0),
		)
		return err
	}

	BeforeEach(func() {
		systemState, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).ToNot(HaveOccurred())
		ml = NewModelLoader(systemState)
		ml.SetLRUEvictionRetrySettings(3, 5*time.Millisecond)
		ml.SetExternalBackend("fakebackend", "127.0.0.1:1")

		evic = &storeEvicter{ml: ml}
		wd = NewWatchDog(WithProcessManager(evic), WithLRULimit(2))
		wd.ReplaceModelGroups(map[string][]string{
			gemma:    {"vram-heavy"},
			nemotron: {"vram-heavy"},
		})
		ml.SetWatchDog(wd)

		// gemma is resident and idle: in the loader store and in the watchdog.
		resident := NewModel(gemma, address, nil)
		resident.MarkHealthy()
		ml.mu.Lock()
		ml.store.Set(gemma, resident)
		ml.mu.Unlock()
		wd.AddAddressModelMap(address, gemma)
		wd.TrackRequest(address)()
	})

	It("evicts the resident model of the group before loading the requested one", func() {
		// The load itself cannot succeed (no real backend); what matters is that
		// the conflicting model was evicted first.
		_ = load()

		Expect(evic.names()).To(ConsistOf(gemma),
			"gemma shares the vram-heavy group and must be evicted before nemotron loads")
	})

	It("leaves a resident model alone when it is not in the group", func() {
		wd.ReplaceModelGroups(map[string][]string{nemotron: {"vram-heavy"}})

		_ = load()

		Expect(evic.names()).To(BeEmpty())
	})

	It("keeps tracking a resident model it failed to stop", func() {
		// The incident: stopping gemma timed out, gemma kept its VRAM, and it
		// had been forgotten, so the next load saw no conflict and ran out of
		// memory.
		evic.setFailure(gemma, errors.New("waiting to shut down model: context deadline exceeded"))

		_ = load()

		Expect(wd.GetState().AddressModelMap).To(HaveKeyWithValue(address, gemma),
			"gemma still runs, so the watchdog must still know it")
	})

	It("stops a resident model that first refused to stop on a later attempt", func() {
		evic.setFailure(gemma, errors.New("waiting to shut down model: context deadline exceeded"))
		ml.SetLRUEvictionRetrySettings(200, 10*time.Millisecond)

		go func() {
			// Release the lifecycle lock once a first attempt has failed, rather
			// than after a wall-clock delay that a slow CI could outrun.
			defer GinkgoRecover()
			Eventually(evic.names, "5s", "1ms").ShouldNot(BeEmpty())
			evic.setFailure(gemma, nil)
		}()

		_ = load()

		Expect(wd.GetState().AddressModelMap).ToNot(HaveKey(address),
			"gemma was finally stopped and untracked")
	})
})
