// SPDX-License-Identifier: MIT
package localai

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/routing/billing"
	"github.com/mudler/LocalAI/pkg/grpc/metadata"
	motionutil "github.com/mudler/LocalAI/pkg/motion"
	motion "github.com/mudler/LocalAI/pkg/motion/proto"
	"github.com/mudler/xlog"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/protobuf/proto"
)

// Each release retires an outstanding capture exactly once. Binary output precedes
// its completion release; thinning/expiry releases have no binary output.
type motionFlow struct {
	Type              string   `json:"type"`
	Version           int      `json:"version"`
	ID                uint64   `json:"id"`
	Release           []string `json:"release"`
	CompletedSequence string   `json:"completed_sequence,omitempty"`
	CompletedKind     string   `json:"completed_kind,omitempty"`
	WindowFrames      int      `json:"window_frames"`
	MaxPendingBytes   int      `json:"max_pending_bytes"`
	MaxFrameBytes     int      `json:"max_frame_bytes"`
	ProcessingEWMA    float64  `json:"processing_ewma_ms"`
	RecommendedFPS    float64  `json:"recommended_fps"`
	QueuedFrames      int      `json:"queued_frames"`
	QueuedBytes       int      `json:"queued_bytes"`
	OldestAgeMS       float64  `json:"oldest_age_ms"`
	Pressure          bool     `json:"pressure"`
	Dropped           uint64   `json:"dropped_frames"`
}

type motionSocketMessage struct {
	output []byte
	flow   motionFlow
}

// The reader admits monotonically ordered inputs; one worker owns inference.
// A single writer consumes this bounded channel, preserving output/release order.
type motionUpload struct {
	recordIngress func(string, int)
	mu            sync.Mutex
	pending       motionutil.FrameQueue
	lastSequence  uint64
	lastTime      int64
	haveFrame     bool
	processing    time.Duration
	pressure      bool
	wake          chan struct{}
	messages      chan motionSocketMessage
}

func newMotionUpload() *motionUpload {
	return &motionUpload{
		recordIngress: func(string, int) {},
		wake:          make(chan struct{}, 1),
		messages:      make(chan motionSocketMessage, 8),
	}
}

func (u *motionUpload) feedback(released []uint64) motionFlow {
	releases := make([]string, len(released))
	for i, sequence := range released {
		releases[i] = strconv.FormatUint(sequence, 10)
	}
	flow := motionFlow{
		Type:            "flow",
		Version:         2,
		Release:         releases,
		WindowFrames:    motionutil.UploadWindow,
		MaxPendingBytes: motionutil.UploadMaxBytes,
		MaxFrameBytes:   motionutil.UploadFrameMaxBytes,
		ProcessingEWMA:  u.processing.Seconds() * 1000,
		QueuedFrames:    u.pending.Len(),
		QueuedBytes:     u.pending.Bytes(),
		OldestAgeMS:     u.pending.OldestAge(time.Now()).Seconds() * 1000,
		Pressure:        u.pressure,
		Dropped:         u.pending.Dropped,
	}
	if u.processing == 0 {
		flow.WindowFrames = 2
	} else {
		factor := 1.05
		if u.pressure {
			factor = .9
		}
		flow.RecommendedFPS = factor / u.processing.Seconds()
	}
	return flow
}

func (u *motionUpload) send(ctx context.Context, msg motionSocketMessage) bool {
	select {
	case u.messages <- msg:
		return true
	case <-ctx.Done():
		return false
	}
}

func (u *motionUpload) admit(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	input := &motion.Input{}
	if err := proto.Unmarshal(data, input); err != nil {
		return errors.New("invalid motion protobuf")
	}
	frame := input.GetFrame()
	if input.GetResetState() || frame == nil {
		return errors.New("v2 accepts frames only; create a new session to reset")
	}
	if err := motionutil.ValidateFrame(frame); err != nil {
		return err
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	if u.haveFrame && (frame.Sequence <= u.lastSequence || frame.SourceTimeUs <= u.lastTime) {
		return errors.New("sequence and source time must increase")
	}
	u.lastSequence, u.lastTime, u.haveFrame = frame.Sequence, frame.SourceTimeUs, true
	arrived := time.Now()
	expired := u.pending.Expire(arrived)
	u.recordIngress("expired", len(expired))
	released, pressure := u.pending.Offer(motionutil.QueuedFrame{Sequence: frame.Sequence, SourceTime: frame.SourceTimeUs, Data: data, Arrived: arrived})
	u.recordIngress("accepted", 1)
	u.recordIngress("thinned", len(released))
	released = append(expired, released...)
	u.pressure = u.pressure || pressure
	if !u.send(ctx, motionSocketMessage{flow: u.feedback(released)}) {
		return ctx.Err()
	}
	return nil
}

func (u *motionUpload) read(s *motionSession, ws *websocket.Conn) error {
	ws.SetReadLimit(motionutil.UploadFrameMaxBytes)
	for {
		kind, data, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		if kind != websocket.BinaryMessage {
			return errors.New("binary motion Input required")
		}
		err = u.admit(s.ctx, data)
		if err != nil {
			return err
		}
		s.last.Store(time.Now().UnixNano())
		select {
		case u.wake <- struct{}{}:
		default:
		}
	}
}

// Exactly one worker holds the session's frameMu through the v2 owner lifetime.
func (m *MotionEndpoints) runMotionUpload(s *motionSession, u *motionUpload, record func(*motionFrameResult)) error {
	for {
		u.mu.Lock()
		expired := u.pending.Expire(time.Now())
		u.recordIngress("expired", len(expired))
		frame, ok := u.pending.Take()
		if u.pending.Len() <= 2 {
			u.pressure = false
		}
		flow := u.feedback(expired)
		if len(expired) > 0 && !u.send(s.ctx, motionSocketMessage{flow: flow}) {
			u.mu.Unlock()
			return s.ctx.Err()
		}
		u.mu.Unlock()
		if !ok {
			select {
			case <-u.wake:
				continue
			case <-s.ctx.Done():
				return s.ctx.Err()
			}
		}
		if s.ctx.Err() != nil {
			return s.ctx.Err()
		}
		input := &motion.Input{}
		if err := proto.Unmarshal(frame.Data, input); err != nil {
			return err
		}
		result, err := m.processMotionInput(s, input, frame.Data)
		if err != nil {
			return err
		}
		record(result)

		kind := "event"
		if pose := result.output.GetPose(); pose != nil {
			kind = "pose"
		}
		u.mu.Lock()
		elapsed := max(time.Millisecond, result.elapsed)
		if u.processing == 0 {
			u.processing = elapsed
		} else {
			u.processing = (u.processing*4 + elapsed) / 5
		}
		flow = u.feedback([]uint64{frame.Sequence})
		flow.CompletedSequence = strconv.FormatUint(frame.Sequence, 10)
		flow.CompletedKind = kind
		sent := u.send(s.ctx, motionSocketMessage{output: result.data, flow: flow})
		u.mu.Unlock()
		if !sent {
			return s.ctx.Err()
		}
	}
}

func writeMotionSocket(ws *websocket.Conn, kind int, data []byte) error {
	_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return ws.WriteMessage(kind, data)
}

func (m *MotionEndpoints) duplexMotion(c echo.Context, s *motionSession, ws *websocket.Conn) error {
	u := newMotionUpload()
	u.recordIngress = func(outcome string, count int) {
		if count == 0 {
			return
		}
		if outcome == "accepted" {
			s.uploadAccepted.Add(uint64(count))
		} else {
			s.uploadDropped.Add(uint64(count))
		}
		m.ingress.Add(s.ctx, int64(count), metric.WithAttributes(attribute.String("model", s.model), attribute.String("outcome", outcome)))
	}
	record := m.motionSocketAccounting(c, s)
	defer m.close(s, "duplex disconnected")
	if err := writeMotionSocket(ws, websocket.BinaryMessage, s.definition); err != nil {
		return nil
	}
	flowID := uint64(0)
	writeFlow := func(flow motionFlow) error {
		flowID++
		flow.ID = flowID
		data, err := json.Marshal(flow)
		if err != nil {
			return err
		}
		return writeMotionSocket(ws, websocket.TextMessage, data)
	}
	if err := writeFlow(u.feedback(nil)); err != nil {
		return nil
	}

	_ = ws.SetReadDeadline(time.Now().Add(time.Minute))
	ws.SetPongHandler(func(string) error { return ws.SetReadDeadline(time.Now().Add(time.Minute)) })
	finished := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() { defer workers.Done(); finished <- u.read(s, ws) }()
	go func() { defer workers.Done(); finished <- m.runMotionUpload(s, u, record) }()
	// Cancellation closes gRPC transport; it does not claim to preempt native GPU work.
	defer func() { m.close(s, "duplex disconnected"); _ = ws.Close(); workers.Wait() }()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case err := <-finished:
			if err != nil && s.ctx.Err() == nil {
				xlog.Debug("motion duplex closed", "model", s.model, "error", err)
			}
			_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "motion stream ended; create a new session"), time.Now().Add(time.Second))
			return nil
		case <-s.ctx.Done():
			return nil
		case msg := <-u.messages:
			if msg.output != nil {
				if err := writeMotionSocket(ws, websocket.BinaryMessage, msg.output); err != nil {
					return nil
				}
			}
			if err := writeFlow(msg.flow); err != nil {
				return nil
			}
		case <-ping.C:
			if err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return nil
			}
		}
	}
}

// WebSocket upgrades bypass HTTP usage middleware. Capture identity before worker
// startup and record processed inputs directly through the same billing recorder.
func (m *MotionEndpoints) motionSocketAccounting(c echo.Context, s *motionSession) func(*motionFrameResult) {
	if m.app == nil {
		return func(*motionFrameResult) {}
	}
	return recordMotionUsage(c, s, m.app.StatsRecorder(), m.app.FallbackUser())
}

func recordMotionUsage(c echo.Context, s *motionSession, recorder *billing.Recorder, fallback *auth.User) func(*motionFrameResult) {
	if recorder == nil {
		return func(*motionFrameResult) {}
	}
	user := auth.GetUser(c)
	if user == nil {
		user = fallback
	}
	endpoint := c.Request().URL.Path
	if user == nil {
		return func(*motionFrameResult) { billing.CountUnrecorded(context.Background(), endpoint, "no_user") }
	}
	base := auth.UsageRecord{UserID: user.ID, UserName: user.Name, Source: auth.GetSource(c), Model: s.model, Endpoint: endpoint, RequestedModel: s.model, ServedModel: s.model}
	if base.Source == "" {
		base.Source = auth.UsageSourceWeb
	}
	if key := auth.GetAPIKey(c); key != nil {
		id := key.ID
		base.APIKeyID = &id
	}
	return func(result *motionFrameResult) {
		outputs := 0
		if result.output.GetPose() != nil {
			outputs = 1
		}
		usage, err := metadata.EncodeUsage(metadata.Usage{InputUnits: 1, OutputUnits: outputs, AccountingRule: "motion_frames_v1"})
		if err != nil {
			xlog.Error("invalid motion usage metadata", "error", err)
			billing.CountUnrecorded(context.Background(), endpoint, "invalid_usage")
			return
		}
		record := base
		record.PromptTokens, record.CompletionTokens, record.TotalTokens = 1, int64(outputs), int64(1+outputs)
		record.PreFilterPromptTokens, record.PostFilterPromptTokens = 1, 1
		record.Duration = result.elapsed.Milliseconds()
		record.CreatedAt = time.Now()
		record.Metadata = string(usage)
		if err := recorder.Record(context.Background(), &record); err != nil {
			xlog.Error("motion usage recording failed", "error", err)
			billing.CountUnrecorded(context.Background(), endpoint, "record_error")
		}
	}
}
