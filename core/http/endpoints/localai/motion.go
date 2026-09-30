// SPDX-License-Identifier: MIT
package localai

import (
	"context"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/trace"
	grpcpkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	motion "github.com/mudler/LocalAI/pkg/motion/proto"
	"github.com/mudler/xlog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/protobuf/proto"
)

const motionIdleTimeout = 2 * time.Minute

type MotionSessionRequest struct {
	Model      string `json:"model"`
	Profile    string `json:"profile"`
	TimeOrigin string `json:"time_origin"`
}
type MotionSessionInfo struct {
	UploadAccepted uint64 `json:"upload_accepted"`
	UploadDropped  uint64 `json:"upload_dropped"`
	ID             string `json:"id"`
	Model          string `json:"model"`
	Profile        string `json:"profile"`
	TimeOrigin     string `json:"time_origin"`
	Frames         uint64 `json:"frames"`
	Poses          uint64 `json:"poses"`
}

type motionSession struct {
	uploadAccepted, uploadDropped             atomic.Uint64
	id, owner, model, engine, profile, origin string
	ctx                                       context.Context
	cancel                                    context.CancelFunc
	stream                                    grpcpkg.MotionStreamClient
	definition                                []byte
	frameMu                                   sync.Mutex
	frames, poses                             atomic.Uint64
	last                                      atomic.Int64
	once                                      sync.Once
	started                                   time.Time
	traceID                                   string
}
type MotionEndpoints struct {
	ingress          metric.Int64Counter
	app              *application.Application
	corsEnabled      bool
	mu               sync.Mutex
	sessions         map[string]*motionSession
	reserved         map[string]bool
	active           metric.Int64UpDownCounter
	outcomes, resets metric.Int64Counter
	latency, stages  metric.Float64Histogram
}

func NewMotionEndpoints(app *application.Application) *MotionEndpoints {
	meter := otel.Meter("github.com/mudler/LocalAI/motion")
	m := &MotionEndpoints{app: app, sessions: map[string]*motionSession{}, reserved: map[string]bool{}}
	if app != nil {
		m.corsEnabled = app.ApplicationConfig().CORS && app.ApplicationConfig().CORSAllowOrigins != ""
	}
	m.active, _ = meter.Int64UpDownCounter("localai_motion_sessions", metric.WithDescription("Active motion sessions"))
	m.ingress, _ = meter.Int64Counter("localai_motion_uploads", metric.WithDescription("Duplex frames accepted or dropped before inference"))
	m.outcomes, _ = meter.Int64Counter("localai_motion_frames", metric.WithDescription("Motion outcomes and rejected input"))
	m.resets, _ = meter.Int64Counter("localai_motion_resets")
	m.latency, _ = meter.Float64Histogram("localai_motion_frame_duration_seconds", metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120))
	m.stages, _ = meter.Float64Histogram("localai_motion_stage_duration_seconds", metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120))
	return m
}
func motionOwner(c echo.Context) string {
	if u := auth.GetUser(c); u != nil {
		return u.ID
	}
	return "unauthenticated"
}
func (m *MotionEndpoints) allowed(c echo.Context, name string) bool {
	u := auth.GetUser(c)
	return u == nil || auth.IsModelAllowed(m.app.AuthDB(), u, name)
}
func (m *MotionEndpoints) attrs(s *motionSession) metric.MeasurementOption {
	return metric.WithAttributes(attribute.String("model", s.model), attribute.String("backend", s.engine))
}
func (s *motionSession) info() MotionSessionInfo {
	return MotionSessionInfo{UploadAccepted: s.uploadAccepted.Load(), UploadDropped: s.uploadDropped.Load(), ID: s.id, Model: s.model, Profile: s.profile, TimeOrigin: s.origin, Frames: s.frames.Load(), Poses: s.poses.Load()}
}
func (m *MotionEndpoints) lookup(c echo.Context) (*motionSession, error) {
	m.mu.Lock()
	s := m.sessions[c.Param("id")]
	m.mu.Unlock()
	if s == nil || s.owner != motionOwner(c) {
		return nil, echo.NewHTTPError(404, "motion session not found")
	}
	if !m.allowed(c, s.model) {
		return nil, echo.NewHTTPError(403, "model not allowed")
	}
	return s, nil
}
func (m *MotionEndpoints) close(s *motionSession, reason string) {
	s.once.Do(func() {
		s.cancel()
		if m.app != nil {
			m.app.WebSocketTickets().RevokePath("/api/motion/sessions/" + s.id + "/poses")
		}
		m.mu.Lock()
		delete(m.sessions, s.id)
		delete(m.reserved, s.model)
		m.mu.Unlock()
		m.active.Add(context.Background(), -1, m.attrs(s))
		failure := ""
		switch reason {
		case "backend error", "inference timeout", "invalid backend output":
			failure = reason
		}
		if s.traceID != "" {
			trace.RecordBackendTrace(trace.BackendTrace{ID: s.traceID, Timestamp: s.started, Duration: time.Since(s.started), Type: trace.BackendTraceMotion, ModelName: s.model, Backend: s.engine, Summary: "motion: " + reason, Error: failure, Data: map[string]any{"upload_accepted": s.uploadAccepted.Load(), "upload_dropped": s.uploadDropped.Load(), "frames": s.frames.Load(), "poses": s.poses.Load(), "profile": s.profile}})
		}
		xlog.Info("motion session closed", "model", s.model, "reason", reason, "frames", s.frames.Load(), "poses", s.poses.Load(), "upload_accepted", s.uploadAccepted.Load(), "upload_dropped", s.uploadDropped.Load())
	})
}

// Create starts a resident motion session.
// @Summary Create a motion capture session
// @Tags motion
// @Accept json
// @Produce json
// @Param request body MotionSessionRequest true "Model, output profile and source clock origin"
// @Success 201 {object} MotionSessionInfo
// @Router /api/motion/sessions [post]
func (m *MotionEndpoints) Create(c echo.Context) error {
	var req MotionSessionRequest
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, 4096)
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(400, "invalid motion session request")
	}
	if req.Profile == "" {
		req.Profile = "soma77"
	}
	if req.Model == "" || (req.Profile != "soma77" && req.Profile != "smpl24") || len(req.TimeOrigin) == 0 || len(req.TimeOrigin) > 128 {
		return echo.NewHTTPError(400, "model, profile soma77/smpl24 and time_origin (1..128 characters) required")
	}
	if !m.allowed(c, req.Model) {
		return echo.NewHTTPError(403, "model not allowed")
	}
	cfg, err := m.app.ModelConfigLoader().LoadModelConfigFileByNameDefaultOptions(req.Model, m.app.ApplicationConfig())
	if err != nil || cfg == nil {
		return echo.NewHTTPError(404, "model not found")
	}
	if !cfg.HasUsecases(config.FLAG_MOTION) {
		return echo.NewHTTPError(400, "model does not support motion")
	}
	m.mu.Lock()
	if len(m.reserved) >= 16 || m.reserved[req.Model] {
		m.mu.Unlock()
		return echo.NewHTTPError(429, "motion capacity exhausted; one session per loaded model")
	}
	m.reserved[req.Model] = true
	m.mu.Unlock()
	success := false
	defer func() {
		if !success {
			m.mu.Lock()
			delete(m.reserved, req.Model)
			m.mu.Unlock()
		}
	}()
	ctx, cancel := context.WithCancel(m.app.ApplicationConfig().Context)
	defer func() {
		if !success {
			cancel()
		}
	}()
	stop := context.AfterFunc(c.Request().Context(), cancel)
	defer stop()
	loadTimer := time.AfterFunc(5*time.Minute, cancel)
	defer loadTimer.Stop()
	stream, err := backend.ModelMotionStream(ctx, m.app.ModelLoader(), m.app.ApplicationConfig(), *cfg)
	if err != nil {
		return echo.NewHTTPError(503, "motion backend unavailable")
	}
	if err := stream.Send(&pb.MotionRequest{ModelIdentity: cfg.Model, Profile: req.Profile}); err != nil {
		return echo.NewHTTPError(502, "motion configuration failed")
	}
	ready, err := stream.Recv()
	if err != nil {
		return echo.NewHTTPError(502, "motion backend initialization failed")
	}
	out := &motion.Output{}
	if err := proto.Unmarshal(ready.Output, out); err != nil || out.GetDefinition() == nil {
		return echo.NewHTTPError(502, "invalid motion definition")
	}
	if out.GetDefinition().Conventions == nil {
		out.GetDefinition().Conventions = map[string]string{}
	}
	out.GetDefinition().Conventions["time_origin"] = req.TimeOrigin
	definition, err := proto.Marshal(out)
	if err != nil {
		return err
	}
	s := &motionSession{id: uuid.NewString(), owner: motionOwner(c), model: req.Model, engine: cfg.Backend, profile: req.Profile, origin: req.TimeOrigin, ctx: ctx, cancel: cancel, stream: stream, definition: definition, started: time.Now()}
	s.last.Store(time.Now().UnixNano())
	if conf := m.app.ApplicationConfig(); conf.EnableTracing {
		trace.InitBackendTracingIfEnabled(conf.TracingMaxItems, conf.TracingMaxBodyBytes)
		s.traceID = trace.BeginBackendTrace(trace.BackendTrace{Timestamp: s.started, Type: trace.BackendTraceMotion, ModelName: s.model, Backend: s.engine, Summary: "motion: " + s.profile})
	}
	m.mu.Lock()
	m.sessions[s.id] = s
	m.mu.Unlock()
	success = true
	m.active.Add(ctx, 1, m.attrs(s))
	xlog.Info("motion session started", "model", s.model, "profile", s.profile)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				m.close(s, "closed")
				return
			case <-ticker.C:
				if time.Since(time.Unix(0, s.last.Load())) > motionIdleTimeout {
					m.close(s, "idle timeout")
					return
				}
			}
		}
	}()
	return c.JSON(http.StatusCreated, s.info())
}

// Get returns session counters.
// @Summary Get motion session status
// @Tags motion
// @Param id path string true "Session ID"
// @Success 200 {object} MotionSessionInfo
// @Router /api/motion/sessions/{id} [get]
func (m *MotionEndpoints) Get(c echo.Context) error {
	s, err := m.lookup(c)
	if err != nil {
		return err
	}
	return c.JSON(200, s.info())
}

// Delete releases the session.
// @Summary Close a motion session
// @Tags motion
// @Param id path string true "Session ID"
// @Success 204
// @Router /api/motion/sessions/{id} [delete]
func (m *MotionEndpoints) Delete(c echo.Context) error {
	s, err := m.lookup(c)
	if err != nil {
		return err
	}
	m.close(s, "deleted")
	return c.NoContent(204)
}

type motionFrameResult struct {
	data    []byte
	output  *motion.Output
	elapsed time.Duration
}

func (m *MotionEndpoints) processMotionInput(s *motionSession, input *motion.Input, data []byte) (*motionFrameResult, error) {
	s.last.Store(time.Now().UnixNano())
	start := time.Now()
	timeout := time.AfterFunc(motionIdleTimeout, func() { m.close(s, "inference timeout") })
	defer timeout.Stop()
	if err := s.stream.Send(&pb.MotionRequest{Input: data}); err != nil {
		m.close(s, "backend error")
		return nil, echo.NewHTTPError(502, "motion backend send failed")
	}
	res, err := s.stream.Recv()
	elapsed := time.Since(start)
	if err != nil {
		m.close(s, "backend error")
		return nil, echo.NewHTTPError(502, "motion inference failed")
	}
	s.last.Store(time.Now().UnixNano())
	out := &motion.Output{}
	if err := proto.Unmarshal(res.Output, out); err != nil {
		m.close(s, "invalid backend output")
		return nil, echo.NewHTTPError(502, "invalid motion output")
	}
	outcome := "pose"
	flags := uint32(0)
	if p := out.GetPose(); p != nil {
		if frame := input.GetFrame(); frame != nil && (p.Sequence != frame.Sequence || p.SourceTimeUs != frame.SourceTimeUs) {
			m.close(s, "backend pose identity mismatch")
			return nil, echo.NewHTTPError(502, "backend pose identity mismatch")
		}
		s.poses.Add(1)
		flags = p.Flags
	} else if e := out.GetEvent(); e != nil {
		switch e.Type {
		case "warmup", "lost", "ambiguous", "reset":
			outcome = e.Type
		default:
			m.close(s, "invalid backend output")
			return nil, echo.NewHTTPError(502, "unexpected motion event")
		}
		flags = e.Flags
	} else {
		m.close(s, "invalid backend output")
		return nil, echo.NewHTTPError(502, "unexpected motion output")
	}
	if f := input.GetFrame(); f != nil {
		s.frames.Add(1)
	}
	m.outcomes.Add(s.ctx, 1, metric.WithAttributes(attribute.String("model", s.model), attribute.String("outcome", outcome)))
	if flags&1 != 0 || outcome == "reset" {
		m.resets.Add(s.ctx, 1, m.attrs(s))
	}
	m.latency.Record(s.ctx, time.Since(start).Seconds(), m.attrs(s))
	for _, stage := range []string{"detector", "vitpose", "gem", "world", "camera"} {
		if ms, ok := res.StageMs[stage]; ok && ms >= 0 && !math.IsNaN(ms) && !math.IsInf(ms, 0) {
			m.stages.Record(s.ctx, ms/1000, metric.WithAttributes(attribute.String("model", s.model), attribute.String("stage", stage)))
		}
	}
	return &motionFrameResult{data: res.Output, output: out, elapsed: elapsed}, nil
}

type MotionTicketRequest struct {
	Origin string `json:"origin"`
}

// Ticket issues a single-use browser WebSocket credential for this session.
// @Summary Issue a motion WebSocket ticket (30 seconds, single use)
// @Tags motion
// @Accept json
// @Produce json
// @Param id path string true "Session ID"
// @Param request body MotionTicketRequest true "Browser origin"
// @Success 201 {object} auth.WebSocketTicketResponse
// @Router /api/motion/sessions/{id}/tickets [post]
func (m *MotionEndpoints) Ticket(c echo.Context) error {
	s, err := m.lookup(c)
	if err != nil {
		return err
	}
	if auth.GetUser(c) == nil && (m.app.AuthDB() != nil || len(m.app.ApplicationConfig().ApiKeys) > 0) {
		return echo.NewHTTPError(401, "authentication required")
	}
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, 4096)
	var req MotionTicketRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(400, "invalid ticket request")
	}
	ticket, err := m.app.WebSocketTickets().Issue(c, "/api/motion/sessions/"+s.id+"/poses", req.Origin)
	if err != nil {
		return err
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(201, ticket)
}

// Poses exchanges binary Protobuf frames and outputs; definition is always first.
// @Summary Stream motion frames and poses over a duplex WebSocket
// @Tags motion
// @Param id path string true "Session ID"
// @Success 101
// @Router /api/motion/sessions/{id}/poses [get]
func (m *MotionEndpoints) Poses(c echo.Context) error {
	s, err := m.lookup(c)
	if err != nil {
		return err
	}
	// Validated header credentials or an explicit CORS policy extend the origin default.
	// Reuse middleware approval to keep origin matching consistent with HTTP.
	duplex := false
	for _, protocol := range websocket.Subprotocols(c.Request()) {
		if protocol == "localai.motion.v2" {
			duplex = true
		}
	}
	if !duplex {
		return echo.NewHTTPError(400, "localai.motion.v2 subprotocol required")
	}
	if !s.frameMu.TryLock() {
		return echo.NewHTTPError(409, "motion session already has an input owner")
	}
	defer s.frameMu.Unlock()
	upgrader := websocket.Upgrader{Subprotocols: []string{"localai.motion.v2"}}
	origin := c.Request().Header.Get(echo.HeaderOrigin)
	allowedOrigin := c.Response().Header().Get(echo.HeaderAccessControlAllowOrigin)
	if auth.WebSocketTicketAuthenticated(c) || auth.HeaderAuthenticated(c) || (m.corsEnabled && origin != "" && (allowedOrigin == "*" || allowedOrigin == origin)) {
		upgrader.CheckOrigin = func(*http.Request) bool { return true }
	}
	ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = ws.Close() }()
	return m.duplexMotion(c, s, ws)
}
