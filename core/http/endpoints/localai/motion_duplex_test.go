// SPDX-License-Identifier: MIT
package localai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	motion "github.com/mudler/LocalAI/pkg/motion/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
)

type gatedMotionStream struct {
	ctx           context.Context
	gate          chan struct{}
	started       chan uint64
	input         *motion.Input
	mu            sync.Mutex
	processed     []uint64
	wrongIdentity bool
}

func (f *gatedMotionStream) Send(r *pb.MotionRequest) error {
	f.input = &motion.Input{}
	return proto.Unmarshal(r.Input, f.input)
}
func (f *gatedMotionStream) Recv() (*pb.MotionResponse, error) {
	frame := f.input.GetFrame()
	select {
	case f.started <- frame.Sequence:
	default:
	}
	select {
	case <-f.gate:
	case <-f.ctx.Done():
		return nil, f.ctx.Err()
	}
	f.mu.Lock()
	f.processed = append(f.processed, frame.Sequence)
	f.mu.Unlock()
	out := &motion.Output{Payload: &motion.Output_Pose{Pose: &motion.Pose{Sequence: frame.Sequence, SourceTimeUs: frame.SourceTimeUs}}}
	if f.wrongIdentity {
		out.GetPose().Sequence++
	}
	data, err := proto.Marshal(out)
	return &pb.MotionResponse{Output: data}, err
}
func (f *gatedMotionStream) CloseSend() error         { return nil }
func (f *gatedMotionStream) Context() context.Context { return f.ctx }

func duplexTestFrame(sequence uint64) []byte {
	data, err := proto.Marshal(&motion.Input{Payload: &motion.Input_Frame{Frame: &motion.Frame{Sequence: sequence, SourceTimeUs: int64(sequence) * 10000, Rgb: make([]byte, 8*8*3), Width: 8, Height: 8}}})
	Expect(err).NotTo(HaveOccurred())
	return data
}

var _ = Describe("Motion duplex WebSocket", func() {
	var m *MotionEndpoints
	var s *motionSession
	var backend *gatedMotionStream
	var server *httptest.Server
	var address string
	BeforeEach(func() {
		m = NewMotionEndpoints(nil)
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		backend = &gatedMotionStream{
			ctx:     ctx,
			gate:    make(chan struct{}),
			started: make(chan uint64, 8),
		}
		s = &motionSession{
			id:      "duplex",
			owner:   "unauthenticated",
			model:   "gem-x",
			profile: "smpl24",
			ctx:     ctx,
			cancel:  cancel,
			stream:  backend,
			started: time.Now(),
		}
		s.definition = motionBytes(&motion.Output{Payload: &motion.Output_Definition{Definition: &motion.Definition{Schema: "test"}}})
		m.sessions[s.id] = s
		m.reserved[s.model] = true

		e := echo.New()
		e.GET("/api/motion/sessions/:id/poses", m.Poses)
		server = httptest.NewServer(e)
		DeferCleanup(server.Close)
		address = "ws" + strings.TrimPrefix(server.URL, "http") + "/api/motion/sessions/duplex/poses"
	})
	connect := func() *websocket.Conn {

		dialer := websocket.Dialer{Subprotocols: []string{"localai.motion.v2"}}
		ws, response, err := dialer.Dial(address, nil)
		if response != nil {
			DeferCleanup(response.Body.Close)
		}
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = ws.Close() })

		Expect(ws.SetReadDeadline(time.Now().Add(3 * time.Second))).To(Succeed())
		Expect(ws.Subprotocol()).To(Equal("localai.motion.v2"))

		kind, data, err := ws.ReadMessage()
		Expect(err).NotTo(HaveOccurred())
		Expect(kind).To(Equal(websocket.BinaryMessage))
		Expect(data).To(Equal(s.definition))

		kind, data, err = ws.ReadMessage()
		Expect(err).NotTo(HaveOccurred())
		Expect(kind).To(Equal(websocket.TextMessage))
		var flow motionFlow
		Expect(json.Unmarshal(data, &flow)).To(Succeed())
		Expect(flow.ID).To(Equal(uint64(1)))
		Expect(flow.WindowFrames).To(Equal(2))
		Expect(flow.RecommendedFPS).To(BeZero())
		return ws
	}
	DescribeTable("rejects non-duplex protocols before upgrade", func(protocols []string) {
		d := websocket.Dialer{Subprotocols: protocols}
		_, response, err := d.Dial(address, nil)
		Expect(err).To(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusBadRequest))
		Expect(response.Body.Close()).To(Succeed())
		Expect(s.ctx.Err()).NotTo(HaveOccurred())
		Expect(s.frameMu.TryLock()).To(BeTrue())
		s.frameMu.Unlock()
	}, Entry("missing", []string{}), Entry("read-only", []string{"localai.motion.v1"}))
	It("thins pending frames evenly, preserves active inference, and releases each ID once", func() {
		ws := connect()
		Expect(ws.WriteMessage(websocket.BinaryMessage, duplexTestFrame(1))).To(Succeed())
		Eventually(backend.started).Should(Receive(Equal(uint64(1))))

		// Deliberately overload the server rather than obeying its bootstrap credits.
		for sequence := uint64(2); sequence <= 7; sequence++ {
			Expect(ws.WriteMessage(websocket.BinaryMessage, duplexTestFrame(sequence))).To(Succeed())
		}

		released := map[string]bool{}
		sawPressure := false
		for !sawPressure {
			_, data, err := ws.ReadMessage()
			Expect(err).NotTo(HaveOccurred())
			var flow motionFlow
			Expect(json.Unmarshal(data, &flow)).To(Succeed())
			for _, id := range flow.Release {
				Expect(released[id]).To(BeFalse())
				released[id] = true
			}
			if flow.Pressure {
				Expect(flow.Release).To(Equal([]string{"3", "5", "6"}))
				Expect(flow.QueuedFrames).To(Equal(3))
				sawPressure = true
			}
		}

		close(backend.gate)
		for len(released) < 7 {
			kind, data, err := ws.ReadMessage()
			Expect(err).NotTo(HaveOccurred())
			if kind == websocket.BinaryMessage {
				continue
			}
			var flow motionFlow
			Expect(json.Unmarshal(data, &flow)).To(Succeed())
			for _, id := range flow.Release {
				Expect(released[id]).To(BeFalse())
				released[id] = true
			}
			Expect(flow.RecommendedFPS).To(BeNumerically(">", 0))
		}

		backend.mu.Lock()
		processed := append([]uint64(nil), backend.processed...)
		backend.mu.Unlock()
		Expect(processed).To(Equal([]uint64{1, 2, 4, 7}))
	})
	It("rejects competing ingress and cancels a blocked worker on disconnect", func() {
		ws := connect()
		Expect(ws.WriteMessage(websocket.BinaryMessage, duplexTestFrame(1))).To(Succeed())
		Eventually(backend.started).Should(Receive())

		dialer := websocket.Dialer{Subprotocols: []string{"localai.motion.v2"}}
		_, response, err := dialer.Dial(address, nil)
		Expect(err).To(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusConflict))
		Expect(response.Body.Close()).To(Succeed())

		Expect(ws.Close()).To(Succeed())
		Eventually(s.ctx.Done()).Should(BeClosed())
		Eventually(func() bool {
			if s.frameMu.TryLock() {
				s.frameMu.Unlock()
				return true
			}
			return false
		}).Should(BeTrue())

		m.mu.Lock()
		remaining := len(m.sessions)
		m.mu.Unlock()
		Expect(remaining).To(BeZero())
	})
	It("rejects reset without backend work", func() {
		ws := connect()
		Expect(ws.WriteMessage(websocket.BinaryMessage, []byte{16, 1})).To(Succeed())
		Eventually(s.ctx.Done()).Should(BeClosed())
		Consistently(backend.started).ShouldNot(Receive())
	})
	It("rejects oversized socket frames before protobuf decoding", func() {
		ws := connect()
		_ = ws.WriteMessage(websocket.BinaryMessage, make([]byte, (2<<20)+1))
		Eventually(s.ctx.Done()).Should(BeClosed())
		Consistently(backend.started).ShouldNot(Receive())
	})
	It("rejects sequence replay even when the original was dropped", func() {
		u := newMotionUpload()
		for sequence := uint64(1); sequence <= 6; sequence++ {
			Expect(u.admit(s.ctx, duplexTestFrame(sequence))).To(Succeed())
		}
		Expect(u.admit(s.ctx, duplexTestFrame(2))).To(MatchError("sequence and source time must increase"))
	})
	It("rejects mismatched backend poses before publication or counters", func() {
		backend.wrongIdentity = true
		ws := connect()

		Expect(ws.WriteMessage(websocket.BinaryMessage, duplexTestFrame(1))).To(Succeed())
		Eventually(backend.started).Should(Receive())
		close(backend.gate)

		Eventually(s.ctx.Done()).Should(BeClosed())
		Expect(s.poses.Load()).To(BeZero())
		Expect(s.frames.Load()).To(BeZero())
	})

})
