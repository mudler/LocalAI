package cloudproxy

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// scriptedForwardClient is a fake ForwardClient that returns a fixed
// sequence of replies. Each Recv pops the next reply or returns the
// terminal error. Used to drive forwardReader through scripted gRPC
// responses without standing up a real backend.
type scriptedForwardClient struct {
	replies []*pb.ForwardReply
	final   error
	idx     int
}

func (s *scriptedForwardClient) Send(*pb.ForwardRequest) error { return nil }
func (s *scriptedForwardClient) CloseSend() error              { return nil }
func (s *scriptedForwardClient) Context() context.Context      { return context.Background() }
func (s *scriptedForwardClient) Recv() (*pb.ForwardReply, error) {
	if s.idx >= len(s.replies) {
		if s.final != nil {
			return nil, s.final
		}
		return nil, io.EOF
	}
	r := s.replies[s.idx]
	s.idx++
	return r, nil
}

func TestForwardReader_ConcatsChunks(t *testing.T) {
	g := NewWithT(t)
	r := &forwardReader{stream: &scriptedForwardClient{
		replies: []*pb.ForwardReply{
			{BodyChunk: []byte("hello ")},
			{BodyChunk: []byte("world")},
			{BodyChunk: []byte("!")},
		},
	}}
	got, err := io.ReadAll(r)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(string(got)).To(Equal("hello world!"))
}

func TestForwardReader_PartialReads(t *testing.T) {
	g := NewWithT(t)
	r := &forwardReader{stream: &scriptedForwardClient{
		replies: []*pb.ForwardReply{
			{BodyChunk: []byte("abcdefghij")},
		},
	}}
	// Read 3 bytes at a time — exercises pos advancement within a chunk.
	var out []byte
	buf := make([]byte, 3)
	for {
		n, err := r.Read(buf)
		out = append(out, buf[:n]...)
		if errors.Is(err, io.EOF) {
			break
		}
		g.Expect(err).NotTo(HaveOccurred())
	}
	g.Expect(string(out)).To(Equal("abcdefghij"))
}

func TestForwardReader_SkipsEmptyChunks(t *testing.T) {
	g := NewWithT(t)
	// Empty chunks must not be treated as EOF — backends may legitimately
	// emit them (e.g. SSE keepalives, transport quirks).
	r := &forwardReader{stream: &scriptedForwardClient{
		replies: []*pb.ForwardReply{
			{BodyChunk: nil},
			{BodyChunk: []byte("data")},
			{BodyChunk: []byte{}},
			{BodyChunk: []byte("more")},
		},
	}}
	got, err := io.ReadAll(r)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(string(got)).To(Equal("datamore"))
}

// infiniteForwardClient simulates a misbehaving backend that never
// stops emitting replies. Used to verify Close() doesn't spin forever.
type infiniteForwardClient struct {
	ctx   context.Context
	calls int
}

func (s *infiniteForwardClient) Send(*pb.ForwardRequest) error { return nil }
func (s *infiniteForwardClient) CloseSend() error              { return nil }
func (s *infiniteForwardClient) Context() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}
func (s *infiniteForwardClient) Recv() (*pb.ForwardReply, error) {
	s.calls++
	return &pb.ForwardReply{BodyChunk: []byte("never-ending")}, nil
}

func TestForwardReader_CloseBoundedByIterationCap(t *testing.T) {
	// Misbehaving backend that never returns EOF. Without the cap,
	// Close() would loop forever. The cap is currently 1024.
	g := NewWithT(t)
	upstream := &infiniteForwardClient{}
	r := &forwardReader{stream: upstream}

	done := make(chan struct{})
	go func() {
		_ = r.Close()
		close(done)
	}()
	g.Eventually(done, 2*time.Second).Should(BeClosed(), "Close did not return within 2s")
	g.Expect(upstream.calls).To(BeNumerically("<=", 2048),
		"Close drained %d replies; expected bounded near 1024", upstream.calls)
}

func TestForwardReader_CloseExitsOnContextCancel(t *testing.T) {
	// Even before the iteration cap, a cancelled context should let
	// Close() return — that's the request-scoped exit path.
	g := NewWithT(t)
	ctx, cancel := context.WithCancel(context.Background())
	upstream := &infiniteForwardClient{ctx: ctx}
	r := &forwardReader{stream: upstream}

	cancel()
	done := make(chan struct{})
	go func() {
		_ = r.Close()
		close(done)
	}()
	g.Eventually(done, 2*time.Second).Should(BeClosed(), "Close did not return after context cancel")
}

func TestForwardReader_PropagatesError(t *testing.T) {
	g := NewWithT(t)
	wantErr := errors.New("upstream blew up")
	r := &forwardReader{stream: &scriptedForwardClient{
		replies: []*pb.ForwardReply{{BodyChunk: []byte("partial")}},
		final:   wantErr,
	}}
	buf := make([]byte, 16)
	n, err := r.Read(buf)
	g.Expect(n).To(Equal(len("partial")))
	g.Expect(string(buf[:n])).To(Equal("partial"))
	g.Expect(err).NotTo(HaveOccurred())
	_, err = r.Read(buf)
	g.Expect(errors.Is(err, wantErr)).To(BeTrue())
}
