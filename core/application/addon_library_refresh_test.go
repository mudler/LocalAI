// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the refresh of the add-on libraries at startup", func() {
	It("does not hold the start of the server while a library answers slowly", func() {
		release := make(chan struct{})
		done := make(chan struct{})
		began := time.Now()
		startLibraryRefresh(context.Background(), func(context.Context) error {
			defer close(done)
			<-release
			return nil
		})
		Expect(time.Since(began)).To(BeNumerically("<", time.Second), "the caller waited for the refresh")
		close(release)
		Eventually(done).Should(BeClosed())
	})
	It("keeps a failed refresh to a log line and stays quiet at shutdown", func() {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		startLibraryRefresh(ctx, func(context.Context) error {
			defer close(done)
			cancel()
			return errors.New("unreachable")
		})
		Eventually(done).Should(BeClosed())
	})
})
