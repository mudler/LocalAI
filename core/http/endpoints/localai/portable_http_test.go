// SPDX-License-Identifier: MIT
//
//nolint:errcheck,forbidigo // These focused HTTP harnesses use testing.T and assert response status inline.
package localai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/trace/tracepersist"
	"mime/multipart"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/endpoints/openai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/voicerecognition"
	grpcpkg "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type profileHTTPBackend struct {
	grpcpkg.Backend
	profiles    schema.SpeakerProfiles
	last        *pb.DiarizeRequest
	embeds      int
	unsupported bool
	values      [][]byte
	// soundsSupported makes Diarize honour include_sounds with sounds; left
	// false it behaves like a backend that ignores the field.
	soundsSupported bool
	sounds          []*pb.DiarizeSound
}

func (b *profileHTTPBackend) Status(context.Context) (*pb.StatusResponse, error) {
	if b.unsupported {
		return nil, status.Error(codes.Unimplemented, "no profiles")
	}
	return &pb.StatusResponse{SpeakerEncoder: &pb.SpeakerEncoder{Identity: b.profiles.Encoder.Identity, Dimension: int32(b.profiles.Encoder.Dimension)}}, nil
}
func (b *profileHTTPBackend) Diarize(_ context.Context, r *pb.DiarizeRequest, _ ...ggrpc.CallOption) (*pb.DiarizeResponse, error) {
	b.last = r
	raw, _ := json.Marshal(b.profiles)
	resp := &pb.DiarizeResponse{Segments: []*pb.DiarizeSegment{{Speaker: "7", Start: 0, End: 3, Text: "Hello"}}, SpeakerProfilesJson: string(raw)}
	if r.IncludeSounds && b.soundsSupported {
		resp.SoundsIncluded = true
		resp.Sounds = b.sounds
	}
	return resp, nil
}
func (b *profileHTTPBackend) VoiceEmbed(context.Context, *pb.VoiceEmbedRequest, ...ggrpc.CallOption) (*pb.VoiceEmbedResponse, error) {
	b.embeds++
	return &pb.VoiceEmbedResponse{Embedding: []float32{1, 0}, Model: "legacy.gguf"}, nil
}
func (b *profileHTTPBackend) StoresSet(_ context.Context, in *pb.StoresSetOptions, _ ...ggrpc.CallOption) (*pb.Result, error) {
	for _, v := range in.Values {
		b.values = append(b.values, append([]byte(nil), v.Bytes...))
	}
	return &pb.Result{Success: true}, nil
}
func profileFixture() schema.SpeakerProfiles {
	return schema.SpeakerProfiles{Version: 1, Encoder: schema.SpeakerEncoder{Identity: "sha256:" + strings.Repeat("a", 64), Dimension: 2}, Speakers: []schema.SpeakerProfile{{Speaker: 7, CleanDuration: 3, Intervals: []schema.SpeakerProfileInterval{{Start: 0, End: 3}}, Embedding: []float32{1, 0}}, {Speaker: 2, CleanDuration: 3, Intervals: []schema.SpeakerProfileInterval{{Start: 3, End: 6}}, Embedding: []float32{0, 1}}}}
}
func profileServer(b *profileHTTPBackend, denied bool) (*echo.Echo, voicerecognition.Registry) {
	ml := model.NewModelLoader(&system.SystemState{})
	ml.SetModelRouter(func(_ context.Context, id string, _, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
		return model.NewModelWithClient(id, "test://profiles", b), nil
	})
	cfg := &config.ModelConfig{Name: "test", Backend: "stub"}
	cfg.SetDefaults()
	cfg.Options = []string{"speaker_model:speaker.gguf"}
	reg := voicerecognition.NewStoreRegistry(func(context.Context, string) (grpcpkg.Backend, error) { return b, nil }, "test", 0)
	e := echo.New()
	var db *gorm.DB
	if denied {
		db = &gorm.DB{}
	}
	setup := func(voice bool) echo.MiddlewareFunc {
		return func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if denied {
					c.Set("auth_user", &auth.User{ID: "user", Role: "user"})
					c.Set("auth_permissions", &auth.UserPermission{Permissions: auth.PermissionMap{auth.FeatureVoiceRecognition: false}})
				}
				if voice {
					var r schema.VoiceRegisterRequest
					if err := c.Bind(&r); err != nil {
						return err
					}
					c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, &r)
				} else {
					var r schema.OpenAIRequest
					if strings.HasPrefix(c.Request().Header.Get("Content-Type"), "application/json") {
						if err := c.Bind(&r); err != nil {
							return err
						}
					}
					r.Model = "test"
					c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, &r)
				}
				c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)
				return next(c)
			}
		}
	}
	for _, route := range []string{"/v1/audio/diarization", "/audio/diarization"} {
		e.POST(route, openai.DiarizationEndpoint(nil, ml, &config.ApplicationConfig{SystemState: &system.SystemState{}}, reg, db), setup(false))
	}
	e.POST("/v1/voice/identify", localai.VoiceIdentifyEndpoint(nil, ml, &config.ApplicationConfig{SystemState: &system.SystemState{}}, reg), func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			var r schema.VoiceIdentifyRequest
			if err := c.Bind(&r); err != nil {
				return err
			}
			c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, &r)
			c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)
			return next(c)
		}
	})
	e.POST("/v1/voice/register", localai.VoiceRegisterEndpoint(nil, ml, &config.ApplicationConfig{SystemState: &system.SystemState{}}, reg), setup(true), auth.RequireFeature(db, auth.FeatureVoiceRecognition))
	return e, reg
}
func profileJSON(e *echo.Echo, route string, payload any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(payload)
	r := httptest.NewRequest("POST", route, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	return w
}
func TestPortableProfileHTTP(t *testing.T) {
	b := &profileHTTPBackend{profiles: profileFixture()}
	e, reg := profileServer(b, false)
	for _, on := range []bool{false, true} {
		w := profileJSON(e, "/v1/audio/diarization", map[string]any{"model": "test", "file": "YXVkaW8=", "include_speaker_profiles": on, "include_text": on})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if bytes.Contains(w.Body.Bytes(), []byte("speaker_profiles")) != on || b.last.IncludeSpeakerProfiles != on {
			t.Fatal(w.Body.String())
		}
		if on && !bytes.Contains(w.Body.Bytes(), []byte("Hello")) {
			t.Fatal("missing combined text")
		}
	}
	for _, slot := range []int{7, 2} {
		w := profileJSON(e, "/v1/voice/register", map[string]any{"model": "test", "name": "Ada", "speaker_slot": slot, "speaker_profiles": b.profiles})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	entries, err := reg.List(context.Background())
	if err != nil || len(entries) != 2 || entries[0].Metadata.ID == entries[1].Metadata.ID || entries[0].Embedding[0] == entries[1].Embedding[0] || entries[0].Metadata.Model != b.profiles.Encoder.Identity {
		t.Fatal(entries, err)
	}
	// Replay uses registration IDs and exact loaded identity, not names.
	wReplay := profileJSON(e, "/v1/audio/diarization", map[string]any{"model": "test", "file": "YQ=="})
	if wReplay.Code != 200 || len(b.last.KnownVoices) != 2 || b.last.KnownVoices[0].Id == b.last.KnownVoices[1].Id {
		t.Fatal(wReplay.Code, b.last)
	}
	wIdentify := profileJSON(e, "/v1/voice/identify", map[string]any{"model": "test", "audio": "YQ=="})
	var identified schema.VoiceIdentifyResponse
	if wIdentify.Code != 200 || json.Unmarshal(wIdentify.Body.Bytes(), &identified) != nil || len(identified.Matches) != 2 {
		t.Fatal(wIdentify.Code, wIdentify.Body.String())
	}
	b.profiles.Encoder.Identity = "sha256:" + strings.Repeat("b", 64)
	profileJSON(e, "/v1/audio/diarization", map[string]any{"model": "test", "file": "YQ=="})
	if len(b.last.KnownVoices) != 0 {
		t.Fatal("cross-model replay")
	}
	wIdentify = profileJSON(e, "/v1/voice/identify", map[string]any{"model": "test", "audio": "YQ=="})
	json.Unmarshal(wIdentify.Body.Bytes(), &identified)
	if len(identified.Matches) != 0 {
		t.Fatal("cross-model identify", wIdentify.Body.String())
	}
	b.profiles = profileFixture()
	if b.embeds != 2 {
		t.Fatal("profile enrollment invoked audio encoder")
	}
	w := profileJSON(e, "/v1/voice/register", map[string]any{"model": "test", "name": "Legacy", "audio": "YXVkaW8="})
	if w.Code != 200 || b.embeds != 3 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, kind := range []string{"wrongmodel", "zero", "wrongdim", "unavailable", "missing", "audio", "version"} {
		p := profileFixture()
		slot := 7
		payload := map[string]any{"model": "test", "name": "Ada", "speaker_slot": slot}
		switch kind {
		case "wrongmodel":
			p.Encoder.Identity = "sha256:" + strings.Repeat("b", 64)
		case "zero":
			p.Speakers[0].Embedding = []float32{0, 0}
		case "wrongdim":
			p.Speakers[0].Embedding = []float32{1}
		case "unavailable":
			reason := "overlap"
			p.Speakers[0].UnavailableReason = &reason
			p.Speakers[0].Embedding = nil
		case "missing":
			payload["speaker_slot"] = 0
		case "audio":
			payload["audio"] = "YQ=="
		case "version":
			p.Version = 2
		}
		payload["speaker_profiles"] = p
		w = profileJSON(e, "/v1/voice/register", payload)
		if w.Code != 400 {
			t.Fatalf("%s: %d %s", kind, w.Code, w.Body.String())
		}
	}
	deniedServer, _ := profileServer(b, true)
	deniedResponse := profileJSON(deniedServer, "/v1/voice/register", map[string]any{"model": "test", "name": "Ada", "speaker_slot": 7, "speaker_profiles": b.profiles})
	if deniedResponse.Code != 403 {
		t.Fatal(deniedResponse.Code, deniedResponse.Body.String())
	}
	// Non-JSON numeric values (NaN) must fail parsing before enrollment.
	raw := `{"model":"test","name":"Ada","speaker_slot":7,"speaker_profiles":{"version":1,"speakers":[{"embedding":[NaN]}]}}`
	request := httptest.NewRequest("POST", "/v1/voice/register", strings.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, request)
	if recorder.Code != 400 {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
	b.unsupported = true
	w = profileJSON(e, "/v1/voice/register", map[string]any{"model": "test", "name": "Ada", "speaker_slot": 7, "speaker_profiles": b.profiles})
	if w.Code != 501 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestPortableProfileMultipartPermissions(t *testing.T) {
	for _, denied := range []bool{false, true} {
		for _, route := range []string{"/v1/audio/diarization", "/audio/diarization"} {
			b := &profileHTTPBackend{profiles: profileFixture()}
			e, _ := profileServer(b, denied)
			body := &bytes.Buffer{}
			mw := multipart.NewWriter(body)
			mw.WriteField("model", "test")
			mw.WriteField("include_speaker_profiles", "true")
			mw.WriteField("include_text", "true")
			f, _ := mw.CreateFormFile("file", "sample.wav")
			f.Write([]byte("audio"))
			mw.Close()
			r := httptest.NewRequest("POST", route, body)
			r.Header.Set("Content-Type", mw.FormDataContentType())
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			want := 200
			if denied {
				want = 403
			}
			if w.Code != want {
				t.Fatal(route, w.Code, w.Body.String())
			}
			if denied && b.last != nil {
				t.Fatal("denied request reached backend")
			}
			if denied {
				w = profileJSON(e, route, map[string]any{"model": "test", "file": "YQ==", "include_speaker_profiles": true})
				if w.Code != 403 {
					t.Fatal(w.Code, w.Body.String())
				}
			}
		}
	}
	b := &profileHTTPBackend{profiles: profileFixture()}
	e, _ := profileServer(b, false)
	for _, format := range []string{"rttm", "invalid"} {
		w := profileJSON(e, "/v1/audio/diarization", map[string]any{"model": "test", "file": "YQ==", "response_format": format, "include_speaker_profiles": true})
		if w.Code != 400 || b.last != nil {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
func (b *profileHTTPBackend) HealthCheck(context.Context) (bool, error) { return true, nil }

func (b *profileHTTPBackend) StoresFind(_ context.Context, in *pb.StoresFindOptions, _ ...ggrpc.CallOption) (*pb.StoresFindResult, error) {
	r := &pb.StoresFindResult{}
	for _, v := range b.values {
		r.Keys = append(r.Keys, &pb.StoresKey{Floats: in.Key.Floats})
		r.Values = append(r.Values, &pb.StoresValue{Bytes: v})
		r.Similarities = append(r.Similarities, 1)
	}
	return r, nil
}
func TestPortableProfileModelAccess(t *testing.T) {
	b := &profileHTTPBackend{profiles: profileFixture()}
	e, _ := profileServer(b, false)
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set("auth_user", &auth.User{ID: "limited", Role: "user"})
			c.Set("auth_permissions", &auth.UserPermission{AllowedModels: auth.ModelAllowlist{Enabled: true, Models: []string{"different-model"}}})
			return next(c)
		}
	}, auth.RequireModelAccess(&gorm.DB{}))
	for _, route := range []string{"/v1/audio/diarization", "/audio/diarization", "/v1/voice/register"} {
		w := profileJSON(e, route, map[string]any{"model": "test", "file": "YQ==", "include_speaker_profiles": true, "name": "Ada", "speaker_profiles": b.profiles, "speaker_slot": 7})
		if w.Code != 403 || b.last != nil {
			t.Fatal(route, w.Code, w.Body.String())
		}
	}
}

func TestPortableProfilesNeverPersistInAPITraces(t *testing.T) {
	root := t.TempDir()
	app, err := application.New(config.EnableTracing, config.WithDataPath(root), config.WithDisableLocalAIAssistant(true), config.WithDisableStats(true), config.WithSystemState(&system.SystemState{Model: system.Model{ModelsPath: root}, Backend: system.Backend{BackendsPath: root}}))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Shutdown()
	b := &profileHTTPBackend{profiles: profileFixture()}
	// Slot zero must be distinguishable from an omitted slot.
	b.profiles.Speakers[0].Speaker = 0
	e, _ := profileServer(b, false)
	e.Use(middleware.TraceMiddleware(app))
	e.POST("/ordinary", func(c echo.Context) error { return c.JSON(200, map[string]string{"result": "benign"}) })
	for _, route := range []string{"/v1/audio/diarization", "/audio/diarization"} {
		for _, on := range []bool{true, false} {
			w := profileJSON(e, route, map[string]any{"model": "test", "file": "YXVkaW8=", "include_speaker_profiles": on})
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			if bytes.Contains(w.Body.Bytes(), []byte(`"embedding":[1,0]`)) != on {
				t.Fatal(w.Body.String())
			}
		}
	}
	w := profileJSON(e, "/v1/voice/register", map[string]any{"model": "test", "name": "Ada", "speaker_slot": 0, "speaker_profiles": b.profiles})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Queue a nonsensitive trace last: its persistence is a barrier for earlier requests.
	profileJSON(e, "/ordinary", map[string]string{"message": "benign"})
	store, err := tracepersist.New[middleware.APIExchange](filepath.Join(root, "traces", "api"), 100)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		records, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, r := range records {
			if r.Request.Path == "/ordinary" {
				found = true
			}
		}
		if found {
			if len(records) != 1 {
				t.Fatalf("persisted biometric exchanges: %d records", len(records))
			}
			if string(*records[0].Response.Body) != "{\"result\":\"benign\"}\n" {
				t.Fatal("ordinary trace changed")
			}
			if len(middleware.GetTraces()) != 1 {
				t.Fatal("biometric exchange captured in memory")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ordinary trace not persisted")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDiarizationSoundsHTTP(t *testing.T) {
	b := &profileHTTPBackend{profiles: profileFixture(), soundsSupported: true, sounds: []*pb.DiarizeSound{{Start: 1.5, End: 2.25, Label: "Dog", Confidence: 0.75}}}
	e, _ := profileServer(b, false)

	for _, format := range []string{"json", "verbose_json"} {
		w := profileJSON(e, "/v1/audio/diarization", map[string]any{"model": "test", "file": "YXVkaW8=", "include_sounds": true, "response_format": format})
		if w.Code != 200 || !b.last.IncludeSounds {
			t.Fatal(format, w.Code, w.Body.String())
		}
		var got struct {
			Sounds []schema.DiarizationSound `json:"sounds"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		want := schema.DiarizationSound{Start: 1.5, End: 2.25, Label: "Dog", Confidence: 0.75}
		if len(got.Sounds) != 1 || got.Sounds[0] != want {
			t.Fatal(format, w.Body.String())
		}
	}

	// Multipart form field.
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	mw.WriteField("model", "test")
	mw.WriteField("include_sounds", "true")
	f, _ := mw.CreateFormFile("file", "sample.wav")
	f.Write([]byte("audio"))
	mw.Close()
	r := httptest.NewRequest("POST", "/v1/audio/diarization", body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"label":"Dog"`)) {
		t.Fatal(w.Code, w.Body.String())
	}

	// Not requested: the field is absent and the backend is told so.
	w = profileJSON(e, "/v1/audio/diarization", map[string]any{"model": "test", "file": "YXVkaW8="})
	if w.Code != 200 || b.last.IncludeSounds || bytes.Contains(w.Body.Bytes(), []byte(`"sounds"`)) {
		t.Fatal(w.Code, w.Body.String())
	}

	// Requested and heard nothing: an empty list, not an absent field.
	b.sounds = nil
	w = profileJSON(e, "/v1/audio/diarization", map[string]any{"model": "test", "file": "YXVkaW8=", "include_sounds": true})
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"sounds":[]`)) {
		t.Fatal(w.Code, w.Body.String())
	}

	// RTTM cannot carry sounds: rejected before the backend runs.
	b.last = nil
	w = profileJSON(e, "/v1/audio/diarization", map[string]any{"model": "test", "file": "YXVkaW8=", "include_sounds": true, "response_format": "rttm"})
	if w.Code != 400 || b.last != nil {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestDiarizationSoundsUnsupported(t *testing.T) {
	// A backend that ignores include_sounds ends in a 501 carrying the stable
	// code, never a 200 with an empty list.
	e, _ := profileServer(&profileHTTPBackend{profiles: profileFixture()}, false)
	w := profileJSON(e, "/v1/audio/diarization", map[string]any{"model": "test", "file": "YXVkaW8=", "include_sounds": true})
	if w.Code != 501 || !bytes.Contains(w.Body.Bytes(), []byte(grpcerrors.SoundEventsUnsupportedCode)) {
		t.Fatal(w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte(`"sounds"`)) {
		t.Fatal("unsupported response carried a sounds field", w.Body.String())
	}
}
