// SPDX-License-Identifier: MIT
package grpc

import (
	"context"
	"sync"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type MotionStreamClient interface {
	Send(*pb.MotionRequest) error
	Recv() (*pb.MotionResponse, error)
	CloseSend() error
	Context() context.Context
}

type motionStreamClient struct {
	pb.Backend_MotionStreamClient
	closeOnce sync.Once
	closer    func()
}

// Keep the connection alive after CloseSend until final output is drained.
func (s *motionStreamClient) Recv() (*pb.MotionResponse, error) {
	resp, err := s.Backend_MotionStreamClient.Recv()
	if err != nil {
		s.release()
	}
	return resp, err
}

func (s *motionStreamClient) release() {
	s.closeOnce.Do(func() {
		if s.closer != nil {
			s.closer()
		}
	})
}

// MotionStream holds the watchdog busy marker for the session lifetime.
func (c *Client) MotionStream(ctx context.Context, opts ...grpc.CallOption) (MotionStreamClient, error) {
	if !c.parallel {
		if !c.opMutex.TryLock() {
			return nil, status.Error(codes.ResourceExhausted, "backend is busy")
		}
	}
	c.setBusy(true)
	completeRequest := c.wdMark()

	cleanup := func() {
		completeRequest()
		c.setBusy(false)
		if !c.parallel {
			c.opMutex.Unlock()
		}
	}

	conn, err := c.dial()
	if err != nil {
		cleanup()
		return nil, err
	}
	client := pb.NewBackendClient(conn)
	stream, err := client.MotionStream(ctx, opts...)
	if err != nil {
		_ = conn.Close()
		cleanup()
		return nil, err
	}
	result := &motionStreamClient{
		Backend_MotionStreamClient: stream,
		closer: func() {
			_ = conn.Close()
			cleanup()
		},
	}
	go func() { <-stream.Context().Done(); result.release() }()
	return result, nil
}

func (s *server) MotionStream(stream pb.Backend_MotionStreamServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if err := s.checkModelIdentity(first); err != nil {
		return err
	}
	pending := true
	return s.llm.MotionStream(stream.Context(), func() (*pb.MotionRequest, error) {
		if pending {
			pending = false
			return first, nil
		}
		return stream.Recv()
	}, stream.Send)
}
