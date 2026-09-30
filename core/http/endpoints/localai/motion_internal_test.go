// SPDX-License-Identifier: MIT
package localai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	echomiddleware "github.com/labstack/echo/v4/middleware"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/routing/billing"
	"github.com/mudler/LocalAI/pkg/grpc/metadata"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	motion "github.com/mudler/LocalAI/pkg/motion/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/proto"
)

type fakeMotionStream struct {
	ctx      context.Context
	input    *pb.MotionRequest
	response *pb.MotionResponse
	err      error
}

func (f *fakeMotionStream) Send(r *pb.MotionRequest) error    { f.input = r; return nil }
func (f *fakeMotionStream) Recv() (*pb.MotionResponse, error) { return f.response, f.err }
func (f *fakeMotionStream) CloseSend() error                  { return nil }
func (f *fakeMotionStream) Context() context.Context          { return f.ctx }
func motionBytes(o *motion.Output) []byte {
	b, err := proto.Marshal(o)
	Expect(err).NotTo(HaveOccurred())
	return b
}

type motionUsageBackend struct {
	billing.StatsBackend
	records []*auth.UsageRecord
}

func (b *motionUsageBackend) Record(ctx context.Context, r *auth.UsageRecord) error {
	b.records = append(b.records, r)
	return b.StatsBackend.Record(ctx, r)
}

var configForMotionAuth = config.ApplicationConfig{ApiKeys: []string{"test-key"}}

var _ = Describe("Motion streaming endpoints", func() {
	var m *MotionEndpoints
	var s *motionSession
	var reader *sdkmetric.ManualReader
	BeforeEach(func() {
		reader = sdkmetric.NewManualReader()
		provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
		previous := otel.GetMeterProvider()
		otel.SetMeterProvider(provider)
		DeferCleanup(func() { otel.SetMeterProvider(previous); Expect(provider.Shutdown(context.Background())).To(Succeed()) })
		m = NewMotionEndpoints(nil)
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		s = &motionSession{id: "session", owner: "unauthenticated", model: "gem-x", engine: "gemxcpp", profile: "smpl24", ctx: ctx, cancel: cancel, started: time.Now()}
		s.definition = motionBytes(&motion.Output{Payload: &motion.Output_Definition{Definition: &motion.Definition{Schema: "test", Profile: "smpl24"}}})
		m.sessions[s.id] = s
		m.reserved[s.model] = true
	})
	It("does not expose another user's session", func() {
		c := echo.New().NewContext(httptest.NewRequest("GET", "/", nil), httptest.NewRecorder())
		c.SetParamNames("id")
		c.SetParamValues(s.id)
		c.Set("auth_user", &auth.User{ID: "different"})
		_, err := m.lookup(c)
		Expect(err).To(MatchError(echo.NewHTTPError(404, "motion session not found")))
	})
	It("rejects unauthenticated access through standard middleware", func() {
		e := echo.New()
		e.Use(auth.Middleware(nil, &configForMotionAuth))
		e.GET("/api/motion/sessions/:id", m.Get)
		recorder := httptest.NewRecorder()
		e.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/motion/sessions/session", nil))
		Expect(recorder.Code).To(Equal(http.StatusUnauthorized))
	})
	It("preserves source timestamps through ingestion and records stage metrics", func() {
		frame := &motion.Frame{Width: 8, Height: 8, Rgb: make([]byte, 192), Sequence: 7, SourceTimeUs: 123456}
		input, err := proto.Marshal(&motion.Input{Payload: &motion.Input_Frame{Frame: frame}})
		Expect(err).NotTo(HaveOccurred())
		output := motionBytes(&motion.Output{Payload: &motion.Output_Pose{Pose: &motion.Pose{Sequence: 7, SourceTimeUs: 123456, Flags: 1}}})
		stream := &fakeMotionStream{ctx: s.ctx, response: &pb.MotionResponse{Output: output, StageMs: map[string]float64{"gem": 12}}}
		s.stream = stream
		decoded := &motion.Input{}
		Expect(proto.Unmarshal(input, decoded)).To(Succeed())
		result, err := m.processMotionInput(s, decoded, input)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.data).To(Equal(output))
		Expect(stream.input.Input).To(Equal(input))
		Expect(s.frames.Load()).To(Equal(uint64(1)))
		Expect(s.poses.Load()).To(Equal(uint64(1)))
		var metrics metricdata.ResourceMetrics
		Expect(reader.Collect(context.Background(), &metrics)).To(Succeed())
		names := []string{}
		for _, scope := range metrics.ScopeMetrics {
			for _, metric := range scope.Metrics {
				names = append(names, metric.Name)
			}
		}
		Expect(names).To(ContainElements("localai_motion_frames", "localai_motion_resets", "localai_motion_stage_duration_seconds"))
	})

	DescribeTable("accounts once per processed socket frame", func(outcome string, wantOutput int, enabled bool) {
		stats := &motionUsageBackend{StatsBackend: billing.NewMemoryBackend(10)}
		DeferCleanup(func() { Expect(stats.Close()).To(Succeed()) })
		var recorder *billing.Recorder
		if enabled {
			recorder = billing.NewRecorder(stats)
		}
		c := echo.New().NewContext(httptest.NewRequest("GET", "/api/motion/sessions/session/poses", nil), httptest.NewRecorder())
		record := recordMotionUsage(c, s, recorder, &auth.User{ID: "local"})
		output := &motion.Output{Payload: &motion.Output_Event{Event: &motion.Event{Type: outcome}}}
		if outcome == "pose" {
			output.Payload = &motion.Output_Pose{Pose: &motion.Pose{}}
		}
		record(&motionFrameResult{output: output, elapsed: time.Millisecond})
		if !enabled {
			Expect(stats.records).To(BeEmpty())
			return
		}
		Expect(stats.records).To(HaveLen(1))
		entry := stats.records[0]
		Expect(entry.UserID).To(Equal("local"))
		Expect(entry.Model).To(Equal("gem-x"))
		Expect(entry.PromptTokens).To(Equal(int64(1)))
		Expect(entry.CompletionTokens).To(Equal(int64(wantOutput)))
		usage, err := metadata.ParseUsage([]byte(entry.Metadata))
		Expect(err).NotTo(HaveOccurred())
		Expect(usage.AccountingRule).To(Equal("motion_frames_v1"))
	}, Entry("pose", "pose", 1, true), Entry("warmup", "warmup", 0, true), Entry("lost", "lost", 0, true), Entry("ambiguous", "ambiguous", 0, true), Entry("disabled", "pose", 1, false))

	DescribeTable("rejects failed or malformed inference without counting a frame", func(failure string) {
		stream := &fakeMotionStream{ctx: s.ctx}
		if failure == "backend" {
			stream.err = errors.New("backend failed")
		} else {
			stream.response = &pb.MotionResponse{Output: motionBytes(&motion.Output{Payload: &motion.Output_Definition{Definition: &motion.Definition{}}})}
		}
		s.stream = stream
		input := &motion.Input{}
		data := duplexTestFrame(1)
		Expect(proto.Unmarshal(data, input)).To(Succeed())
		_, err := m.processMotionInput(s, input, data)
		Expect(err).To(HaveOccurred())
		Expect(s.frames.Load()).To(BeZero())
		Expect(s.poses.Load()).To(BeZero())
		Expect(s.ctx.Done()).To(BeClosed())
	}, Entry("backend error", "backend"), Entry("invalid output", "malformed"))

	It("rejects cross-origin browser connections", func() {
		e := echo.New()
		e.GET("/api/motion/sessions/:id/poses", m.Poses)
		server := httptest.NewServer(e)
		DeferCleanup(server.Close)
		_, response, err := (&websocket.Dialer{Subprotocols: []string{"localai.motion.v2"}}).Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/motion/sessions/session/poses", http.Header{"Origin": []string{"https://untrusted.example"}})
		Expect(err).To(HaveOccurred())
		Expect(response.StatusCode).To(Equal(403))
		Expect(response.Body.Close()).To(Succeed())
	})
	DescribeTable("honors explicitly enabled CORS for browser connections", func(enabled bool, allowed []string, origin string, accept bool) {
		m.corsEnabled = enabled
		e := echo.New()
		e.Use(echomiddleware.CORSWithConfig(echomiddleware.CORSConfig{AllowOrigins: allowed}))
		e.GET("/api/motion/sessions/:id/poses", m.Poses)
		server := httptest.NewServer(e)
		DeferCleanup(server.Close)
		ws, response, err := (&websocket.Dialer{Subprotocols: []string{"localai.motion.v2"}}).Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/motion/sessions/session/poses", http.Header{"Origin": []string{origin}})
		if !accept {
			Expect(err).To(HaveOccurred())
			Expect(response.StatusCode).To(Equal(403))
			Expect(response.Body.Close()).To(Succeed())
			return
		}
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(ws.Close()).To(Succeed()) })
		Expect(response.StatusCode).To(Equal(101))
		kind, data, err := ws.ReadMessage()
		Expect(err).NotTo(HaveOccurred())
		Expect(kind).To(Equal(websocket.BinaryMessage))
		out := &motion.Output{}
		Expect(proto.Unmarshal(data, out)).To(Succeed())
		Expect(out.GetDefinition().Profile).To(Equal("smpl24"))
	},
		Entry("explicit allowed origin", true, []string{"https://consumer.example"}, "https://consumer.example", true),
		Entry("explicit wildcard policy", true, []string{"*"}, "http://localhost:3000", true),
		Entry("subdomain policy", true, []string{"https://*.example.com"}, "https://consumer.example.com", true),
		Entry("disallowed origin", true, []string{"https://consumer.example"}, "https://untrusted.example", false),
		Entry("different port", true, []string{"http://localhost:3000"}, "http://localhost:3001", false),
		Entry("default HTTP wildcard does not enable cross-origin sockets", false, []string{"*"}, "https://untrusted.example", false),
	)

	DescribeTable("bypasses origins only for validated header credentials", func(header, value string, status int) {
		m.app = &application.Application{}
		s.owner = "legacy-api-key"
		e := echo.New()
		e.Use(auth.Middleware(nil, &configForMotionAuth))
		e.GET("/api/motion/sessions/:id/poses", m.Poses)
		server := httptest.NewServer(e)
		DeferCleanup(server.Close)
		headers := http.Header{"Origin": []string{"https://external.example"}}
		headers.Set(header, value)
		ws, response, err := (&websocket.Dialer{Subprotocols: []string{"localai.motion.v2"}}).Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/motion/sessions/session/poses", headers)
		Expect(response.StatusCode).To(Equal(status))
		if status != 101 {
			Expect(err).To(HaveOccurred())
			Expect(response.Body.Close()).To(Succeed())
			return
		}
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(ws.Close()).To(Succeed()) })
		_, data, err := ws.ReadMessage()
		Expect(err).NotTo(HaveOccurred())
		output := &motion.Output{}
		Expect(proto.Unmarshal(data, output)).To(Succeed())
		Expect(output.GetDefinition()).NotTo(BeNil())
	},
		Entry("Bearer", "Authorization", "Bearer test-key", 101),
		Entry("API key", "x-api-key", "test-key", 101),
		Entry("alternative API key", "xi-api-key", "test-key", 101),
		Entry("invalid key", "Authorization", "Bearer invalid", 401),
		Entry("cookie key retains origin policy", "Cookie", "token=test-key", 403),
	)

	It("issues browser tickets, preserves identity and rejects replay and revoked credentials", func() {
		m.app = &application.Application{}
		s.owner = "legacy-api-key"
		cfg := config.ApplicationConfig{ApiKeys: []string{"test-key"}}
		e := echo.New()
		e.Use(auth.WithWebSocketTickets(m.app.WebSocketTickets(), auth.Middleware(nil, &cfg)))
		e.POST("/api/motion/sessions/:id/tickets", m.Ticket)
		e.GET("/api/motion/sessions/:id/poses", m.Poses)
		server := httptest.NewServer(e)
		DeferCleanup(server.Close)
		issue := func() auth.WebSocketTicketResponse {
			req := httptest.NewRequest("POST", "/api/motion/sessions/session/tickets", strings.NewReader(`{"origin":"https://consumer.example"}`))
			req.Header.Set("Authorization", "Bearer test-key")
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", "https://consumer.example")
			res := httptest.NewRecorder()
			e.ServeHTTP(res, req)
			Expect(res.Code).To(Equal(201), res.Body.String())
			Expect(res.Header().Get("Cache-Control")).To(Equal("no-store"))
			var ticket auth.WebSocketTicketResponse
			Expect(json.Unmarshal(res.Body.Bytes(), &ticket)).To(Succeed())
			return ticket
		}
		connect := func(ticket, origin, id string) (*websocket.Conn, *http.Response, error) {
			d := websocket.Dialer{Subprotocols: []string{"localai.motion.v2", auth.WebSocketTicketProtocolPrefix + ticket}}
			return d.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/motion/sessions/"+id+"/poses", http.Header{"Origin": []string{origin}})
		}
		ticket := issue()
		revoked := issue()
		for _, attempt := range []struct{ origin, id string }{{"https://untrusted.example", "session"}, {"https://consumer.example", "other"}} {
			_, res, err := connect(ticket.Ticket, attempt.origin, attempt.id)
			Expect(err).To(HaveOccurred())
			Expect(res.StatusCode).To(Equal(401))
			Expect(res.Body.Close()).To(Succeed())
		}
		ws, res, err := connect(ticket.Ticket, "https://consumer.example", "session")
		Expect(err).NotTo(HaveOccurred())
		Expect(ws.Subprotocol()).To(Equal("localai.motion.v2"))
		Expect(res.Header.Get("Sec-WebSocket-Protocol")).NotTo(ContainSubstring(ticket.Ticket))
		_, data, err := ws.ReadMessage()
		Expect(err).NotTo(HaveOccurred())
		out := &motion.Output{}
		Expect(proto.Unmarshal(data, out)).To(Succeed())
		Expect(out.GetDefinition()).NotTo(BeNil())
		Expect(ws.Close()).To(Succeed())
		_, res, err = connect(ticket.Ticket, "https://consumer.example", "session")
		Expect(err).To(HaveOccurred())
		Expect(res.StatusCode).To(Equal(401))
		Expect(res.Body.Close()).To(Succeed())
		cfg.ApiKeys = []string{"replacement"}
		_, res, err = connect(revoked.Ticket, "https://consumer.example", "session")
		Expect(err).To(HaveOccurred())
		Expect(res.StatusCode).To(Equal(401))
		Expect(res.Body.Close()).To(Succeed())
	})
	It("does not issue tickets without session ownership or valid authentication", func() {
		m.app = &application.Application{}
		s.owner = "someone-else"
		e := echo.New()
		e.Use(auth.Middleware(nil, &configForMotionAuth))
		e.POST("/api/motion/sessions/:id/tickets", m.Ticket)
		for _, key := range []string{"", "invalid", "test-key"} {
			req := httptest.NewRequest("POST", "/api/motion/sessions/session/tickets", strings.NewReader(`{"origin":"https://consumer.example"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+key)
			res := httptest.NewRecorder()
			e.ServeHTTP(res, req)
			if key == "test-key" {
				Expect(res.Code).To(Equal(404))
			} else {
				Expect(res.Code).To(Equal(401))
			}
		}
	})

	It("cancels once and releases the model reservation on deletion", func() {
		m.close(s, "deleted")
		m.close(s, "deleted")
		Expect(s.ctx.Done()).To(BeClosed())
		Expect(m.sessions).To(BeEmpty())
		Expect(m.reserved).To(BeEmpty())
	})
})
