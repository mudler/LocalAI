package grpc

import (
	"context"
	"errors"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	"io"
	"net"
	"time"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type liveTTSFixture struct{ base.SingleThread }

func (*liveTTSFixture) TTSLive(ctx context.Context, recv func() (*pb.TTSRequest, error), send func([]byte) error) error {
	for {
		r, e := recv()
		if e == io.EOF {
			return send([]byte("tail"))
		}
		if e != nil {
			return e
		}
		if r.Text == "fail" {
			return errors.New("synthesis failed")
		}
		if r.Text != "" {
			if e := send([]byte(r.Text)); e != nil {
				return e
			}
		}
	}
}

var _ = Describe("TTSLiveEarlyAudioAndTail", func() {
	It("preserves the live synthesis contract", func() {
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			Fail(fmt.Sprint(e))
		}
		s := ggrpc.NewServer(serverOpts()...)
		pb.RegisterBackendServer(s, &server{llm: &liveTTSFixture{}})
		go func() { _ = s.Serve(listener) }()
		defer s.Stop()
		c := NewClient(listener.Addr().String(), true, nil, false).(TTSLiveBackend)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		text := make(chan string)
		audio := make(chan string)
		done := make(chan error, 1)
		go func() {
			done <- c.TTSLive(ctx, &pb.TTSRequest{}, text, func(b []byte) error {
				select {
				case audio <- string(b):
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		}()
		text <- "early"
		if got := <-audio; got != "early" {
			Fail(fmt.Sprint(got))
		}
		// Input deliberately remains open until audio has reached the receiver.
		text <- "late"
		if got := <-audio; got != "late" {
			Fail(fmt.Sprint(got))
		}
		close(text)
		if got := <-audio; got != "tail" {
			Fail(fmt.Sprint(got))
		}
		if e := <-done; e != nil {
			Fail(fmt.Sprint(e))
		}
	})
})
var _ = Describe("TTSLiveCancellationAndSinkError", func() {
	It("preserves the live synthesis contract", func() {
		for _, sinkError := range []bool{false, true} {
			By(map[bool]string{true: "sink error", false: "cancel waiting input"}[sinkError])
			func() {
				listener, e := net.Listen("tcp", "127.0.0.1:0")
				if e != nil {
					Fail(fmt.Sprint(e))
				}
				s := ggrpc.NewServer(serverOpts()...)
				pb.RegisterBackendServer(s, &server{llm: &liveTTSFixture{}})
				go func() { _ = s.Serve(listener) }()
				defer s.Stop()
				c := NewClient(listener.Addr().String(), false, nil, false).(TTSLiveBackend)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				text := make(chan string)
				done := make(chan error, 1)
				sinkErr := errors.New("sink failed")
				go func() {
					done <- c.TTSLive(ctx, &pb.TTSRequest{Text: "start"}, text, func([]byte) error {
						if sinkError {
							return sinkErr
						}
						cancel()
						return nil
					})
				}()
				if e := <-done; e == nil {
					Fail(fmt.Sprint("expected error"))
				} else if sinkError && !errors.Is(e, sinkErr) {
					Fail(fmt.Sprint(e))
				}
				if c.(*Client).IsBusy() {
					Fail(fmt.Sprint("backend remains busy"))
				}
			}()
		}
	})
})

var _ = Describe("TTSLiveRejectsInvalidUnicode", func() {
	It("preserves the live synthesis contract", func() {
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			Fail(fmt.Sprint(e))
		}
		s := ggrpc.NewServer(serverOpts()...)
		pb.RegisterBackendServer(s, &server{llm: &liveTTSFixture{}})
		go func() { _ = s.Serve(listener) }()
		defer s.Stop()
		c := NewClient(listener.Addr().String(), true, nil, false).(TTSLiveBackend)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		text := make(chan string, 1)
		text <- string([]byte{0xe4, 0xb8})
		close(text)
		e = c.TTSLive(ctx, &pb.TTSRequest{}, text, func([]byte) error { return nil })
		if status.Code(e) != codes.InvalidArgument {
			Fail(fmt.Sprintf("invalid text: %v", e))
		}
		if ctx.Err() != nil {
			Fail(fmt.Sprint("invalid input left receiver blocked"))
		}
	})
})
