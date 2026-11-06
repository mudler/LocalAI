package localai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/voiceprofile"
	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpcLib "google.golang.org/grpc"
)

// asUser injects the account that the auth middleware would have set. A nil
// user is a server without accounts.
func asUser(u *auth.User) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if u != nil {
				c.Set("auth_user", u)
			}
			return next(c)
		}
	}
}

type cloneTTSBackend struct {
	grpc.Backend
	requests []*proto.TTSRequest
}

func (b *cloneTTSBackend) HealthCheck(context.Context) (bool, error) { return true, nil }
func (b *cloneTTSBackend) IsBusy() bool                              { return false }
func (b *cloneTTSBackend) TTS(_ context.Context, req *proto.TTSRequest, _ ...grpcLib.CallOption) (*proto.Result, error) {
	b.requests = append(b.requests, req)
	if err := os.WriteFile(req.Dst, voiceProfileWAV(1e9), 0o600); err != nil {
		return nil, err
	}
	return &proto.Result{Success: true}, nil
}

var _ = Describe("Voice profile ownership over HTTP", func() {
	var (
		store          *voiceprofile.Store
		userA, userB   *auth.User
		admin          *auth.User
		backend        *cloneTTSBackend
		ttsCfg         *config.ModelConfig
		ml             *model.ModelLoader
		appConfig      *config.ApplicationConfig
		serverFor      func(u *auth.User) *echo.Echo
		create, speak  func(e *echo.Echo, voice string) *httptest.ResponseRecorder
		profilesOf     func(e *echo.Echo) []voiceprofile.Profile
		call           func(e *echo.Echo, method, path string) *httptest.ResponseRecorder
		createdBy      func(u *auth.User) voiceprofile.Profile
		createdBySlice func(u *auth.User, n int)
	)

	BeforeEach(func() {
		store = voiceprofile.NewStore(GinkgoT().TempDir())
		DeferCleanup(func() { Expect(store.Close()).To(Succeed()) })
		userA = &auth.User{ID: "user-a", Role: auth.RoleUser}
		userB = &auth.User{ID: "user-b", Role: auth.RoleUser}
		admin = &auth.User{ID: "root", Role: auth.RoleAdmin}

		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		appConfig = config.NewApplicationConfig(config.WithSystemState(state))
		appConfig.GeneratedContentDir = GinkgoT().TempDir()
		ml = model.NewModelLoader(state)
		backend = &cloneTTSBackend{}
		ttsCfg = &config.ModelConfig{Name: "clone-tts", Backend: "qwen3-tts-cpp", TTSConfig: config.TTSConfig{VoiceCloning: new(bool)}}
		*ttsCfg.TTSConfig.VoiceCloning = true
		ttsCfg.Model = "weights"
		loaded := model.NewModelWithClient(ttsCfg.ModelID(), "in-process", backend)
		loaded.MarkHealthy()
		_, err = ml.LoadModel(ttsCfg.ModelID(), ttsCfg.Model, func(_, _, _ string) (*model.Model, error) { return loaded, nil })
		Expect(err).NotTo(HaveOccurred())

		serverFor = func(u *auth.User) *echo.Echo {
			e := echo.New()
			e.Use(asUser(u))
			e.GET("/api/voice-profiles", ListVoiceProfilesEndpoint(store))
			e.POST("/api/voice-profiles", CreateVoiceProfileEndpoint(store))
			e.GET("/api/voice-profiles/:id/audio", ServeVoiceProfileAudioEndpoint(store))
			e.DELETE("/api/voice-profiles/:id", DeleteVoiceProfileEndpoint(store))
			e.POST("/v1/audio/speech", TTSEndpoint(nil, ml, appConfig, store), func(next echo.HandlerFunc) echo.HandlerFunc {
				return func(c echo.Context) error {
					var req schema.TTSRequest
					if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
						return echo.ErrBadRequest
					}
					c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, &req)
					cfg := *ttsCfg
					c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &cfg)
					return next(c)
				}
			})
			return e
		}
		call = func(e *echo.Echo, method, path string) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
			return rec
		}
		create = func(e *echo.Echo, consent string) *httptest.ResponseRecorder {
			body, ct := voiceProfileMultipart(consent)
			req := httptest.NewRequest(http.MethodPost, "/api/voice-profiles", body)
			req.Header.Set(echo.HeaderContentType, ct)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			return rec
		}
		speak = func(e *echo.Echo, voice string) *httptest.ResponseRecorder {
			payload, _ := json.Marshal(map[string]any{"model": "clone-tts", "input": "hello", "voice": voice})
			req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(string(payload)))
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			return rec
		}
		profilesOf = func(e *echo.Echo) []voiceprofile.Profile {
			rec := call(e, http.MethodGet, "/api/voice-profiles")
			Expect(rec.Code).To(Equal(http.StatusOK))
			var out VoiceProfileListResponse
			Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
			return out.Data
		}
		createdBy = func(u *auth.User) voiceprofile.Profile {
			rec := create(serverFor(u), "true")
			Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
			var p voiceprofile.Profile
			Expect(json.Unmarshal(rec.Body.Bytes(), &p)).To(Succeed())
			return p
		}
		createdBySlice = func(u *auth.User, n int) {
			for i := 0; i < n; i++ {
				Expect(create(serverFor(u), "true").Code).To(Equal(http.StatusCreated))
			}
		}
	})

	It("stamps a user's profile with their id and an administrator's as instance-wide", func() {
		Expect(createdBy(userA).OwnerID).To(Equal("user-a"))
		Expect(createdBy(admin).OwnerID).To(BeEmpty())
	})

	It("lists own and instance-wide profiles but not another user's; an administrator sees all", func() {
		own := createdBy(userA)
		other := createdBy(userB)
		shared := createdBy(admin)

		ids := func(ps []voiceprofile.Profile) []string {
			var out []string
			for _, p := range ps {
				out = append(out, p.ID)
			}
			return out
		}
		Expect(ids(profilesOf(serverFor(userA)))).To(ConsistOf(own.ID, shared.ID))
		Expect(ids(profilesOf(serverFor(userB)))).To(ConsistOf(other.ID, shared.ID))
		Expect(ids(profilesOf(serverFor(admin)))).To(ConsistOf(own.ID, other.ID, shared.ID))
	})

	It("answers 404, not 403, for the audio of another user's profile", func() {
		mine := createdBy(userA)
		Expect(call(serverFor(userA), http.MethodGet, "/api/voice-profiles/"+mine.ID+"/audio").Code).To(Equal(http.StatusOK))
		Expect(call(serverFor(userB), http.MethodGet, "/api/voice-profiles/"+mine.ID+"/audio").Code).To(Equal(http.StatusNotFound))
		Expect(call(serverFor(admin), http.MethodGet, "/api/voice-profiles/"+mine.ID+"/audio").Code).To(Equal(http.StatusOK))
	})

	It("lets the owner and an administrator delete a profile, and hides it from other users", func() {
		mine := createdBy(userA)
		Expect(call(serverFor(userB), http.MethodDelete, "/api/voice-profiles/"+mine.ID).Code).To(Equal(http.StatusNotFound))
		Expect(profilesOf(serverFor(admin))).To(HaveLen(1))
		Expect(call(serverFor(userA), http.MethodDelete, "/api/voice-profiles/"+mine.ID).Code).To(Equal(http.StatusNoContent))

		again := createdBy(userA)
		Expect(call(serverFor(admin), http.MethodDelete, "/api/voice-profiles/"+again.ID).Code).To(Equal(http.StatusNoContent))
		Expect(profilesOf(serverFor(admin))).To(BeEmpty())
	})

	It("lets only an administrator delete an instance-wide profile", func() {
		shared := createdBy(admin)
		Expect(call(serverFor(userA), http.MethodDelete, "/api/voice-profiles/"+shared.ID).Code).To(Equal(http.StatusForbidden))
		Expect(profilesOf(serverFor(userA))).To(HaveLen(1))
		Expect(call(serverFor(admin), http.MethodDelete, "/api/voice-profiles/"+shared.ID).Code).To(Equal(http.StatusNoContent))
	})

	It("keeps a profile stored before ownership existed visible to everyone", func() {
		p, err := store.Create(context.Background(), voiceprofile.CreateInput{
			Name: "Legacy", Transcript: "words", ConsentConfirmed: true,
		}, strings.NewReader(string(voiceProfileWAV(2e9))))
		Expect(err).NotTo(HaveOccurred())
		Expect(p.OwnerID).To(BeEmpty())
		Expect(profilesOf(serverFor(userA))).To(HaveLen(1))
		Expect(profilesOf(serverFor(userB))).To(HaveLen(1))
	})

	It("behaves as before without accounts: everything is instance-wide", func() {
		p := createdBy(nil)
		Expect(p.OwnerID).To(BeEmpty())
		Expect(profilesOf(serverFor(nil))).To(HaveLen(1))
		Expect(call(serverFor(nil), http.MethodDelete, "/api/voice-profiles/"+p.ID).Code).To(Equal(http.StatusNoContent))
	})

	It("limits the profiles of a user who is not an administrator", func() {
		createdBySlice(userA, voiceprofile.MaxProfilesPerUser)
		rec := create(serverFor(userA), "true")
		Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
		// Another user, and an administrator, are not affected.
		Expect(create(serverFor(userB), "true").Code).To(Equal(http.StatusCreated))
		Expect(create(serverFor(admin), "true").Code).To(Equal(http.StatusCreated))
	})

	It("still needs the consent flag", func() {
		Expect(create(serverFor(userA), "false").Code).To(Equal(http.StatusBadRequest))
	})

	Describe("speech with a profile", func() {
		It("synthesizes for the owner and answers 404 for another user, end to end", func() {
			mine := createdBy(userA)
			rec := speak(serverFor(userA), mine.Voice)
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			Expect(backend.requests).To(HaveLen(1))
			Expect(backend.requests[0].Params).To(HaveKeyWithValue("ref_text", "The exact words spoken in this reference."))

			rec = speak(serverFor(userB), mine.Voice)
			Expect(rec.Code).To(Equal(http.StatusNotFound))
			Expect(backend.requests).To(HaveLen(1), "the backend must not run for a profile the user cannot see")

			Expect(speak(serverFor(admin), mine.Voice).Code).To(Equal(http.StatusOK))
		})

		It("lets every user speak with an instance-wide profile", func() {
			shared := createdBy(admin)
			Expect(speak(serverFor(userB), shared.Voice).Code).To(Equal(http.StatusOK))
		})
	})
})
