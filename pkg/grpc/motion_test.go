// SPDX-License-Identifier: MIT
package grpc

import (
	"context"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"io"
	"time"
)

type motionEcho struct{ base.Base }

func (*motionEcho) Load(*pb.ModelOptions) error { return nil }
func (*motionEcho) MotionStream(_ context.Context, recv func() (*pb.MotionRequest, error), send func(*pb.MotionResponse) error) error {
	for {
		r, err := recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := send(&pb.MotionResponse{Output: r.Input}); err != nil {
			return err
		}
	}
}

var _ = Describe("MotionStream RPC", func() {
	It("round-trips binary payloads, drains half-close and cancels idle receivers", func() {
		Provide("test://motion-echo", &motionEcho{})
		c := NewClient("test://motion-echo", true, nil, false)
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		stream, err := c.MotionStream(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(stream.Send(&pb.MotionRequest{Input: []byte{8, 128, 1}})).To(Succeed())
		reply, err := stream.Recv()
		Expect(err).NotTo(HaveOccurred())
		Expect(reply.Output).To(Equal([]byte{8, 128, 1}))
		Expect(stream.CloseSend()).To(Succeed())
		_, err = stream.Recv()
		Expect(err).To(Equal(io.EOF))
		other, err := c.MotionStream(ctx)
		Expect(err).NotTo(HaveOccurred())
		cancel()
		done := make(chan error, 1)
		go func() { _, err := other.Recv(); done <- err }()
		Eventually(done, time.Second).Should(Receive(HaveOccurred()))
	})
	It("rejects stale model identity before invoking native code", func() {
		s := &server{llm: &motionEcho{}, loadedIdentity: "actual"}
		e := &embedBackend{s: s}
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		stream, err := e.MotionStream(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(stream.Send(&pb.MotionRequest{ModelIdentity: "stale"})).To(Succeed())
		_, err = stream.Recv()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("actual"))
	})
})
