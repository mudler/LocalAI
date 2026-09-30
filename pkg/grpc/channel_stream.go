// SPDX-License-Identifier: MIT
package grpc

import (
	"context"
	"io"
	"sync"

	"google.golang.org/grpc/metadata"
)

// channelStream carries in-process bidirectional RPCs. Publishing the terminal
// error before closing responses lets callers drain the response tail reliably.
type channelStream[Request, Response any] struct {
	ctx       context.Context
	requests  chan Request
	responses chan Response
	done      chan struct{}
	err       error
}

type channelStreamServer[Request, Response any] struct {
	*channelStream[Request, Response]
}

type channelStreamClient[Request, Response any] struct {
	*channelStream[Request, Response]
	sendMu  sync.Mutex
	closed  bool
	cleanup *streamCleanup
}

func newChannelStream[Request, Response any](ctx context.Context, capacity int, serve func(*channelStreamServer[Request, Response]) error) *channelStreamClient[Request, Response] {
	stream := &channelStream[Request, Response]{ctx: ctx, requests: make(chan Request, capacity), responses: make(chan Response, capacity), done: make(chan struct{})}
	client := &channelStreamClient[Request, Response]{channelStream: stream, cleanup: newStreamCleanup(ctx, nil)}
	go func() {
		stream.err = serve(&channelStreamServer[Request, Response]{stream})
		close(stream.done)
		close(stream.responses)
	}()
	return client
}

func (s *channelStream[Request, Response]) Context() context.Context { return s.ctx }
func (s *channelStreamServer[Request, Response]) Send(response Response) error {
	select {
	case s.responses <- response:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}
func (s *channelStreamServer[Request, Response]) Recv() (Request, error) {
	select {
	case request, ok := <-s.requests:
		if ok {
			return request, nil
		}
	case <-s.ctx.Done():
		var zero Request
		return zero, s.ctx.Err()
	}
	var zero Request
	return zero, io.EOF
}
func (s *channelStreamServer[Request, Response]) SetHeader(metadata.MD) error  { return nil }
func (s *channelStreamServer[Request, Response]) SendHeader(metadata.MD) error { return nil }
func (s *channelStreamServer[Request, Response]) SetTrailer(metadata.MD)       {}
func (s *channelStreamServer[Request, Response]) SendMsg(message any) error {
	if response, ok := message.(Response); ok {
		return s.Send(response)
	}
	return nil
}

// Generated bidirectional handlers use typed Recv directly.
func (s *channelStreamServer[Request, Response]) RecvMsg(any) error { return nil }

func (c *channelStreamClient[Request, Response]) AddCleanup(fn func()) { c.cleanup.add(fn) }
func (c *channelStreamClient[Request, Response]) Send(request Request) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.closed {
		return io.EOF
	}
	select {
	case <-c.done:
		return io.EOF
	case <-c.ctx.Done():
		return c.ctx.Err()
	default:
	}
	select {
	case c.requests <- request:
		return nil
	case <-c.done:
		return io.EOF
	case <-c.ctx.Done():
		return c.ctx.Err()
	}
}
func (c *channelStreamClient[Request, Response]) Recv() (Response, error) {
	var zero Response
	select {
	case response, ok := <-c.responses:
		if ok {
			return response, nil
		}
		c.cleanup.finish()
		if c.err != nil {
			return zero, c.err
		}
		return zero, io.EOF
	case <-c.ctx.Done():
		c.cleanup.finish()
		return zero, c.ctx.Err()
	}
}

// Half-close only the request side; the server may still produce responses.
func (c *channelStreamClient[Request, Response]) CloseSend() error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.requests)
	}
	return nil
}
