// SPDX-License-Identifier: MIT
package grpc

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("In-process bidirectional channel streams", func() {
	It("drains responses after half-close, then preserves the terminal error", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		terminal := errors.New("backend failed after tail")
		serverDone := make(chan struct{})
		client := newChannelStream(ctx, 2, func(server *channelStreamServer[int, string]) error {
			defer close(serverDone)
			request, err := server.Recv()
			if err != nil {
				return err
			}
			if request != 7 {
				return errors.New("wrong request")
			}
			_, err = server.Recv()
			if err != io.EOF {
				return errors.New("expected half-close")
			}
			if err = server.Send("tail"); err != nil {
				return err
			}
			return terminal
		})
		var cleaned atomic.Int32
		client.AddCleanup(func() { cleaned.Add(1) })
		Expect(client.Send(7)).To(Succeed())
		Expect(client.CloseSend()).To(Succeed())
		Expect(client.CloseSend()).To(Succeed())
		Eventually(serverDone).Should(BeClosed())
		Expect(cleaned.Load()).To(BeZero())
		tail, err := client.Recv()
		Expect(err).NotTo(HaveOccurred())
		Expect(tail).To(Equal("tail"))
		for range 2 {
			_, err = client.Recv()
			Expect(err).To(MatchError(terminal))
		}
		Expect(cleaned.Load()).To(Equal(int32(1)))
		Expect(client.Send(8)).To(MatchError(io.EOF))
	})
	It("unblocks pending sends when the server finishes without reading", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		stop := make(chan struct{})
		client := newChannelStream(ctx, 0, func(*channelStreamServer[int, int]) error { <-stop; return errors.New("stopped") })
		sent := make(chan error, 1)
		go func() { sent <- client.Send(1) }()
		close(stop)
		Eventually(sent).Should(Receive(MatchError(io.EOF)))
		_, err := client.Recv()
		Expect(err).To(MatchError("stopped"))
	})
	It("cancels blocked readers and writers and releases cleanup exactly once", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		serverDone := make(chan struct{})
		client := newChannelStream(ctx, 0, func(server *channelStreamServer[int, int]) error {
			defer close(serverDone)
			<-ctx.Done()
			return ctx.Err()
		})
		var cleaned atomic.Int32
		client.AddCleanup(func() { cleaned.Add(1) })
		sent := make(chan error, 1)
		received := make(chan error, 1)
		go func() { sent <- client.Send(1) }()
		go func() { _, err := client.Recv(); received <- err }()
		cancel()
		Eventually(sent).Should(Receive(HaveOccurred()))
		Eventually(received).Should(Receive(MatchError(context.Canceled)))
		Eventually(serverDone).Should(BeClosed())
		Eventually(cleaned.Load).Should(Equal(int32(1)))
	})
	It("allows concurrent half-closes without closing responses", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		client := newChannelStream(ctx, 1, func(server *channelStreamServer[int, int]) error {
			_, err := server.Recv()
			if err != io.EOF {
				return err
			}
			return server.Send(42)
		})
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() { _ = client.CloseSend() })
		}
		wg.Wait()
		result, err := client.Recv()
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(42))
		_, err = client.Recv()
		Expect(err).To(MatchError(io.EOF))
	})
})
