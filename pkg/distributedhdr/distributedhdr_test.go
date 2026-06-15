package distributedhdr_test

import (
	"context"
	"sync"
	"testing"

	"github.com/mudler/LocalAI/pkg/distributedhdr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDistributedHdr(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "distributedhdr suite")
}

var _ = Describe("distributedhdr", func() {
	It("returns empty string when no holder is attached", func() {
		Expect(distributedhdr.Holder(context.Background())).To(BeNil())
		Expect(distributedhdr.Load(distributedhdr.Holder(context.Background()))).To(BeEmpty())
	})

	It("is a no-op when stamping into a context without a holder", func() {
		Expect(func() {
			distributedhdr.Stamp(context.Background(), "worker-x")
		}).ToNot(Panic())
	})

	It("is a no-op when stamping a nil context", func() {
		Expect(func() {
			distributedhdr.Stamp(nil, "worker-x")
		}).ToNot(Panic())
	})

	It("stores and retrieves a node ID through Stamp/Load", func() {
		h := distributedhdr.NewHolder()
		ctx := distributedhdr.WithHolder(context.Background(), h)

		distributedhdr.Stamp(ctx, "worker-a")
		Expect(distributedhdr.Load(h)).To(Equal("worker-a"))
	})

	It("retains the latest stamp when called twice", func() {
		h := distributedhdr.NewHolder()
		ctx := distributedhdr.WithHolder(context.Background(), h)

		distributedhdr.Stamp(ctx, "worker-a")
		distributedhdr.Stamp(ctx, "worker-b")
		Expect(distributedhdr.Load(h)).To(Equal("worker-b"))
	})

	It("does not overwrite when stamping with an empty string", func() {
		h := distributedhdr.NewHolder()
		ctx := distributedhdr.WithHolder(context.Background(), h)

		distributedhdr.Stamp(ctx, "worker-a")
		distributedhdr.Stamp(ctx, "")
		Expect(distributedhdr.Load(h)).To(Equal("worker-a"))
	})

	It("propagates the holder via Inherit into a derived context", func() {
		h := distributedhdr.NewHolder()
		src := distributedhdr.WithHolder(context.Background(), h)
		dst := distributedhdr.Inherit(context.Background(), src)

		distributedhdr.Stamp(dst, "worker-c")
		Expect(distributedhdr.Load(h)).To(Equal("worker-c"))
	})

	It("Inherit is a no-op when src has no holder", func() {
		dst := distributedhdr.Inherit(context.Background(), context.Background())
		Expect(distributedhdr.Holder(dst)).To(BeNil())
	})

	It("publishes stamps across goroutines race-free", func() {
		h := distributedhdr.NewHolder()
		ctx := distributedhdr.WithHolder(context.Background(), h)

		const N = 64
		var wg sync.WaitGroup
		wg.Add(N)
		for i := 0; i < N; i++ {
			i := i
			go func() {
				defer wg.Done()
				if i%2 == 0 {
					distributedhdr.Stamp(ctx, "worker-even")
				} else {
					distributedhdr.Stamp(ctx, "worker-odd")
				}
			}()
		}
		wg.Wait()

		got := distributedhdr.Load(h)
		Expect(got).To(SatisfyAny(Equal("worker-even"), Equal("worker-odd")))
	})
})
