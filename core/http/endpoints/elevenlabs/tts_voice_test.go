package elevenlabs_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/endpoints/elevenlabs"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the ElevenLabs TTS voice-id", func() {
	// speak calls the handler as the given user (nil: no accounts) and returns
	// its error and whether the model was asked to load.
	speak := func(user *auth.User, voiceID string) (error, bool) {
		app := config.NewApplicationConfig(config.WithSystemState(&system.SystemState{}))
		app.GeneratedContentDir = GinkgoT().TempDir()
		cfg := config.ModelConfig{Name: "tts", Backend: "stub"}
		cfg.SetDefaults()
		loaded := false
		ml := model.NewModelLoader(&system.SystemState{})
		ml.SetModelRouter(func(_ context.Context, _, _, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
			loaded = true
			return nil, errors.New("no backend in this test")
		})
		req := httptest.NewRequest(http.MethodPost, "/v1/text-to-speech/x", strings.NewReader("{}"))
		c := echo.New().NewContext(req, httptest.NewRecorder())
		c.SetParamNames("voice-id")
		c.SetParamValues(voiceID)
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, &schema.ElevenLabsTTSRequest{ModelID: "tts", Text: "hi"})
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &cfg)
		if user != nil {
			c.Set("auth_user", user)
		}
		return elevenlabs.TTSEndpoint(config.NewModelConfigLoader(""), ml, app)(c), loaded
	}
	isPathRefusal := func(err error) bool {
		var he *echo.HTTPError
		return errors.As(err, &he) && he.Code == http.StatusBadRequest && strings.Contains(he.Message.(string), "not a path")
	}

	It("refuses a path as the voice of a user who is not an administrator", func() {
		for _, v := range []string{"/etc/passwd", "../x.wav", `a\b.wav`} {
			err, loaded := speak(&auth.User{ID: "u", Role: auth.RoleUser}, v)
			Expect(isPathRefusal(err)).To(BeTrue(), v)
			Expect(loaded).To(BeFalse(), v)
		}
	})

	It("does not refuse an administrator, a server without accounts, or a plain voice", func() {
		for _, c := range []struct {
			u *auth.User
			v string
		}{
			{&auth.User{ID: "a", Role: auth.RoleAdmin}, "/voices/a.wav"},
			{nil, "/voices/a.wav"},
			{&auth.User{ID: "u", Role: auth.RoleUser}, "speaker-7"},
		} {
			err, _ := speak(c.u, c.v)
			Expect(isPathRefusal(err)).To(BeFalse(), c.v)
		}
	})
})
