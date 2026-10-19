package grpc

import (
	"context"
	"errors"
	"io"
	"unicode/utf8"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TTSLiveModel optionally accepts text during a single persistent synthesis.
// recv must unblock when ctx is cancelled. io.EOF commits the final text.
// send applies backpressure and must return its first error to the caller.
type TTSLiveModel interface {
	TTSLive(context.Context, func() (*pb.TTSRequest, error), func([]byte) error) error
}

// TTSLiveBackend is optional: older backends retain their existing TTS APIs.
// Closing text signals end of input; audio may continue until this call returns.
type TTSLiveBackend interface {
	TTSLive(context.Context, *pb.TTSRequest, <-chan string, func([]byte) error) error
}

func (s *server) TTSLive(stream pb.Backend_TTSLiveServer) error {
	model, ok := s.llm.(TTSLiveModel)
	if !ok {
		return status.Error(codes.Unimplemented, "backend does not support incremental text TTS")
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if err := s.checkModelIdentity(first); err != nil {
		return err
	}
	if s.llm.Locking() {
		s.llm.Lock()
		defer s.llm.Unlock()
	}
	pending := true
	return model.TTSLive(stream.Context(), func() (*pb.TTSRequest, error) {
		if pending {
			pending = false
			return first, nil
		}
		req, err := stream.Recv()
		if err != nil {
			return nil, err
		}
		if req.Voice != "" || req.Model != "" || req.Language != nil || req.Instructions != nil || len(req.Params) > 0 || req.Dst != "" || req.ModelIdentity != "" {
			return nil, status.Error(codes.InvalidArgument, "only text is permitted after the first TTSLive frame")
		}
		return req, nil
	}, func(pcm []byte) error { return stream.Send(&pb.Reply{Audio: pcm}) })
}

func (c *Client) TTSLive(ctx context.Context, config *pb.TTSRequest, text <-chan string, send func([]byte) error) (returnErr error) {
	parentCtx := ctx
	if !c.parallel {
		if !c.opMutex.TryLock() {
			return status.Error(codes.ResourceExhausted, "backend is busy")
		}
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	complete := c.wdMark()
	defer complete()
	conn, err := c.dial()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := pb.NewBackendClient(conn).TTSLive(ctx)
	if err != nil {
		return err
	}
	if err := stream.Send(config); err != nil {
		return err
	}
	sender := make(chan error, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				sender <- ctx.Err()
				return
			case chunk, ok := <-text:
				if !ok {
					err := stream.CloseSend()
					if err != nil {
						cancel()
					}
					sender <- err
					return
				}
				if !utf8.ValidString(chunk) {
					sender <- status.Error(codes.InvalidArgument, "TTSLive text fragments must be valid UTF-8")
					cancel()
					return
				}
				if err := stream.Send(&pb.TTSRequest{Text: chunk}); err != nil {
					sender <- err
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		cancel()
		sendErr := <-sender
		if sendErr != nil && !errors.Is(sendErr, context.Canceled) && (returnErr == nil || (status.Code(returnErr) == codes.Canceled && parentCtx.Err() == nil)) {
			returnErr = sendErr
		}
	}()
	for {
		reply, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := send(reply.Audio); err != nil {
			return err
		}
	}
}

func (e *embedBackend) TTSLive(ctx context.Context, config *pb.TTSRequest, text <-chan string, send func([]byte) error) error {
	model, ok := e.s.llm.(TTSLiveModel)
	if !ok {
		return status.Error(codes.Unimplemented, "backend does not support incremental text TTS")
	}
	if err := e.s.checkModelIdentity(config); err != nil {
		return err
	}
	if e.s.llm.Locking() {
		e.s.llm.Lock()
		defer e.s.llm.Unlock()
	}
	pending := true
	return model.TTSLive(ctx, func() (*pb.TTSRequest, error) {
		if pending {
			pending = false
			return config, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case chunk, ok := <-text:
			if !utf8.ValidString(chunk) {
				return nil, status.Error(codes.InvalidArgument, "TTSLive text fragments must be valid UTF-8")
			}
			if !ok {
				return nil, io.EOF
			}
			return &pb.TTSRequest{Text: chunk}, nil
		}
	}, send)
}
