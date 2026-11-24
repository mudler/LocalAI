package blob_test

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
)

var _ = Describe("Grace", func() {
	DescribeTable("turns the BlobGrace option into the wait of RemoveLater",
		func(in, want time.Duration) {
			Expect(blob.Grace(in)).To(Equal(want))
		},
		Entry("unset uses the default", time.Duration(0), blob.DefaultGrace),
		Entry("negative removes at once", -time.Second, time.Duration(0)),
		Entry("positive is kept", 5*time.Second, 5*time.Second),
	)
})

// removerStore is a Store that can remove, counting what it removed.
type removerStore struct {
	blob.Store
	mu      sync.Mutex
	removed []string
}

func (r *removerStore) Remove(_ context.Context, sha string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removed = append(r.removed, sha)
	return nil
}

func (r *removerStore) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.removed...)
}

var _ = Describe("Sweep", func() {
	var rs *removerStore
	BeforeEach(func() { rs = &removerStore{} })

	It("removes only what InUse reports as free and then releases", func() {
		released := false
		sw := blob.Sweep{Store: rs, InUse: func(context.Context, []string) (map[string]bool, func(), error) {
			return map[string]bool{"a": true}, func() { released = true }, nil
		}}
		Expect(sw.RemoveLater(context.Background(), []string{"a", "b"})).To(Equal(1))
		Expect(rs.got()).To(Equal([]string{"b"}))
		Expect(released).To(BeTrue())
	})

	It("keeps the blobs when the check fails", func() {
		sw := blob.Sweep{Store: rs, InUse: func(context.Context, []string) (map[string]bool, func(), error) {
			return nil, nil, errors.New("down")
		}}
		Expect(sw.RemoveLater(context.Background(), []string{"a"})).To(BeZero())
		Expect(rs.got()).To(BeEmpty())
	})

	It("removes after the grace, even when the caller is gone", func() {
		caller, cancel := context.WithCancel(context.Background())
		cancel()
		sw := blob.Sweep{Store: rs, Grace: 10 * time.Millisecond, InUse: func(context.Context, []string) (map[string]bool, func(), error) {
			return nil, nil, nil
		}}
		sw.RemoveLater(caller, []string{"a"})
		Eventually(rs.got).Should(Equal([]string{"a"}))
	})

	It("drops a removal that still waits when Life ends", func() {
		life, end := context.WithCancel(context.Background())
		sw := blob.Sweep{Store: rs, Grace: 100 * time.Millisecond, Life: life, InUse: func(context.Context, []string) (map[string]bool, func(), error) {
			return nil, nil, nil
		}}
		sw.RemoveLater(context.Background(), []string{"a"})
		end()
		Consistently(rs.got, 300*time.Millisecond).Should(BeEmpty())
	})
})
