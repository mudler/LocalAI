package downloader

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("IsRetryable classification", func() {
	// net/http surfaces a ResponseHeaderTimeout as an error satisfying
	// errors.Is(err, context.DeadlineExceeded). That is our own transport guard
	// aborting a wedged peer, not the caller giving up, so an explicit
	// transient marking has to outrank the deadline sentinel.
	wedged := func() error {
		return fmt.Errorf("net/http: timeout awaiting response headers: %w", context.DeadlineExceeded)
	}

	It("retries a transient failure that reports itself as a deadline", func() {
		Expect(IsRetryable(context.Background(), asTransient(wedged()))).To(BeTrue())
	})

	It("still refuses to retry once the caller's context is done", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(IsRetryable(ctx, asTransient(wedged()))).To(BeFalse())
	})

	It("still refuses to retry a deliberate user abort", func() {
		Expect(IsRetryable(context.Background(), asTransient(ErrUserCancelled))).To(BeFalse())
	})

	It("does not retry an unmarked failure", func() {
		Expect(IsRetryable(context.Background(), errors.New("checksum mismatch"))).To(BeFalse())
	})

	It("leaves a nil error unretryable", func() {
		Expect(IsRetryable(context.Background(), nil)).To(BeFalse())
	})
})
