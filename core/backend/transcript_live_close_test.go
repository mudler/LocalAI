package backend

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// scriptedLiveStream is a client-side live stream whose responses are
// scripted. After CloseSend it delivers the "tail" responses, then EOF. With
// hang set it never answers after CloseSend, like a backend still working
// through a backlog of fed audio; cancelling the stream context unblocks it.
type scriptedLiveStream struct {
	ctx      context.Context
	mu       sync.Mutex
	queue    []*proto.TranscriptLiveResponse
	tail     []*proto.TranscriptLiveResponse
	hang     bool
	closed   chan struct{}
	closeOne sync.Once
}

func newScriptedLiveStream(ctx context.Context, live, tail []*proto.TranscriptLiveResponse, hang bool) *scriptedLiveStream {
	return &scriptedLiveStream{ctx: ctx, queue: live, tail: tail, hang: hang, closed: make(chan struct{})}
}

func (s *scriptedLiveStream) Send(*proto.TranscriptLiveRequest) error { return nil }
func (s *scriptedLiveStream) Context() context.Context              { return s.ctx }
func (s *scriptedLiveStream) CloseSend() error {
	s.closeOne.Do(func() { close(s.closed) })
	return nil
}

func (s *scriptedLiveStream) Recv() (*proto.TranscriptLiveResponse, error) {
	s.mu.Lock()
	if len(s.queue) > 0 {
		r := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
		return r, nil
	}
	s.mu.Unlock()
	select {
	case <-s.closed:
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
	if s.hang {
		<-s.ctx.Done()
		return nil, s.ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.tail) > 0 {
		r := s.tail[0]
		s.tail = s.tail[1:]
		return r, nil
	}
	return nil, io.EOF
}

var _ = Describe("liveTranscriptionSession.Close", func() {
	var saved time.Duration
	BeforeEach(func() {
		saved = liveCloseDrainTimeout
		liveCloseDrainTimeout = 200 * time.Millisecond
	})
	AfterEach(func() { liveCloseDrainTimeout = saved })

	open := func(live, tail []*proto.TranscriptLiveResponse, hang bool) (*liveTranscriptionSession, *[]LiveTranscriptionEvent) {
		ctx, cancel := context.WithCancel(context.Background())
		s := &liveTranscriptionSession{
			stream:   newScriptedLiveStream(ctx, live, tail, hang),
			cancel:   cancel,
			recvDone: make(chan struct{}),
			release:  func() {},
		}
		var mu sync.Mutex
		events := &[]LiveTranscriptionEvent{}
		go s.recvLoop(ctx, func(ev LiveTranscriptionEvent) {
			mu.Lock()
			*events = append(*events, ev)
			mu.Unlock()
		})
		return s, events
	}

	It("returns nil once the backend delivered the final transcript", func() {
		s, events := open(
			[]*proto.TranscriptLiveResponse{{Delta: "hello"}},
			[]*proto.TranscriptLiveResponse{{Delta: " world"}, {FinalResult: &proto.TranscriptResult{Text: "hello world"}}},
			false)
		Expect(s.Close()).To(Succeed())
		Expect(*events).To(HaveLen(3))
		Expect((*events)[2].Final.Text).To(Equal("hello world"))
	})

	It("reports an incomplete transcript when the backend does not finalize in time", func() {
		// The decode is behind real time: the words of the fed audio after
		// "hello" are still in the backend's backlog when Close gives up.
		s, events := open([]*proto.TranscriptLiveResponse{{Delta: "hello"}}, nil, true)
		err := s.Close()
		Expect(errors.Is(err, ErrLiveTranscriptIncomplete)).To(BeTrue(), "got %v", err)
		Expect(*events).To(HaveLen(1))
	})

	It("reports an incomplete transcript when the stream ends without a final", func() {
		s, _ := open([]*proto.TranscriptLiveResponse{{Delta: "hello"}}, nil, false)
		Expect(errors.Is(s.Close(), ErrLiveTranscriptIncomplete)).To(BeTrue())
	})
})
