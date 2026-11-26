package model

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// With model swap off, a load that needs room must never unload another
// model. It waits for the retry window and then refuses with a retryable
// error instead of loading over the limit.
var _ = Describe("Load with model swap off", func() {
	const (
		resident  = "resident-model"
		requested = "requested-model"
		address   = "127.0.0.1:65011"
	)

	var (
		ml   *ModelLoader
		wd   *WatchDog
		evic *storeEvicter
	)

	load := func() error {
		_, err := ml.Load(
			WithContext(context.Background()),
			WithModelID(requested),
			WithModel("requested.gguf"),
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
		wd = NewWatchDog(WithProcessManager(evic), WithLRULimit(1))
		ml.SetWatchDog(wd)

		m := NewModel(resident, address, nil)
		m.MarkHealthy()
		ml.mu.Lock()
		ml.store.Set(resident, m)
		ml.mu.Unlock()
		wd.AddAddressModelMap(address, resident)
	})

	It("is on by default and evicts the idle model to make room", func() {
		Expect(ml.ModelSwapEnabled()).To(BeTrue())

		_ = load()

		Expect(evic.names()).To(ConsistOf(resident))
	})

	It("refuses the load with a retryable error and keeps the resident model", func() {
		ml.SetModelSwap(false)

		err := load()

		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrModelSwapDisabled)).To(BeTrue(), "got %v", err)
		var swapErr *ModelSwapDisabledError
		Expect(errors.As(err, &swapErr)).To(BeTrue())
		Expect(swapErr.RetryAfter).To(BeNumerically(">", 0))
		var retry interface{ RetryLater() bool }
		Expect(errors.As(err, &retry)).To(BeTrue())
		Expect(retry.RetryLater()).To(BeTrue())
		Expect(evic.names()).To(BeEmpty(), "swap off must not unload any model")
	})

	It("loads when room frees up during the wait", func() {
		ml.SetModelSwap(false)
		ml.SetLRUEvictionRetrySettings(50, 5*time.Millisecond)

		go func() {
			defer GinkgoRecover()
			time.Sleep(20 * time.Millisecond)
			wd.Untrack(address)
		}()

		err := load()

		// The load itself fails (no real backend), but not because of swap.
		Expect(errors.Is(err, ErrModelSwapDisabled)).To(BeFalse(), "got %v", err)
		Expect(evic.names()).To(BeEmpty())
	})

	It("refuses a load whose concurrency group is held by another model", func() {
		ml.SetModelSwap(false)
		wd.SetLRULimit(0)
		wd.ReplaceModelGroups(map[string][]string{
			resident:  {"gpu"},
			requested: {"gpu"},
		})

		err := load()

		Expect(errors.Is(err, ErrModelSwapDisabled)).To(BeTrue(), "got %v", err)
		Expect(evic.names()).To(BeEmpty())
	})
})
