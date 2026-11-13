package openai

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/services/voiceprofile"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("a realtime session of a paired client", func() {
	newSession := func(client *RealtimeClient) *Session {
		return &Session{
			Model:                   "voice",
			client:                  client,
			ModelConfig:             &config.ModelConfig{Pipeline: config.Pipeline{Transcription: "stt-a", LLM: "llm-a", TTS: "tts-a"}},
			InputAudioTranscription: &types.AudioTranscription{Model: "stt-a"},
		}
	}
	realtime := func(model, transcription string) *types.SessionUnion {
		rt := &types.RealtimeSession{Model: model}
		if transcription != "" {
			rt.Audio = &types.RealtimeSessionAudio{Input: &types.SessionAudioInput{Transcription: &types.AudioTranscription{Model: transcription}}}
		}
		return &types.SessionUnion{Realtime: rt}
	}
	transcription := func(model string) *types.SessionUnion {
		return &types.SessionUnion{Transcription: &types.TranscriptionSession{
			Audio: &types.TranscriptionSessionAudio{Input: &types.SessionAudioInput{Transcription: &types.AudioTranscription{Model: model}}},
		}}
	}
	client := &RealtimeClient{KeyID: "k", OwnerID: "o"}

	It("may repeat the model it connected with, and change anything that is not a model", func() {
		s := newSession(client)
		Expect(clientUpdateRefusal(s, realtime("", ""))).To(Succeed())
		Expect(clientUpdateRefusal(s, realtime("voice", ""))).To(Succeed())
		Expect(clientUpdateRefusal(s, realtime("voice", "stt-a"))).To(Succeed())
		Expect(clientUpdateRefusal(s, &types.SessionUnion{Realtime: &types.RealtimeSession{Instructions: "be brief"}})).To(Succeed())
		Expect(clientUpdateRefusal(s, transcription("stt-a"))).To(Succeed())
		Expect(clientUpdateRefusal(s, transcription(""))).To(Succeed())
	})

	It("cannot switch to another pipeline or to another transcription model", func() {
		s := newSession(client)
		Expect(clientUpdateRefusal(s, realtime("other-pipeline", ""))).To(MatchError(errClientModelFixed))
		Expect(clientUpdateRefusal(s, realtime("", "stt-b"))).To(MatchError(errClientModelFixed))
		Expect(clientUpdateRefusal(s, realtime("voice", "stt-b"))).To(MatchError(errClientModelFixed))
		Expect(clientUpdateRefusal(s, transcription("other-pipeline"))).To(MatchError(errClientModelFixed))
	})

	It("leaves the session of any other caller alone", func() {
		s := newSession(nil)
		Expect(clientUpdateRefusal(s, realtime("other-pipeline", "stt-b"))).To(Succeed())
		Expect(clientUpdateRefusal(s, transcription("other-pipeline"))).To(Succeed())
	})
})

var _ = Describe("the voice profiles of a realtime session of a paired client", func() {
	handshake := func(user *auth.User, key *auth.UserAPIKey) echo.Context {
		c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/v1/realtime?model=voice", nil), httptest.NewRecorder())
		if user != nil {
			c.Set("auth_user", user)
		}
		if key != nil {
			c.Set("auth_apikey", key)
		}
		return c
	}
	owner := &auth.User{ID: "owner-1", Role: auth.RoleAdmin}
	clientKey := &auth.UserAPIKey{ID: "k1", UserID: "owner-1", Kind: auth.KeyKindClient}

	It("has no viewer of the owner: a client sees no saved profile, not even a shared one", func() {
		store := voiceprofile.NewStore(GinkgoT().TempDir())
		DeferCleanup(func() { Expect(store.Close()).To(Succeed()) })
		mk := func(v voiceprofile.Viewer, name string) voiceprofile.Profile {
			p, err := store.CreateFor(context.Background(), v, voiceprofile.CreateInput{Name: name, Transcript: "Words.", ConsentConfirmed: true},
				[]voiceprofile.ReferenceInput{{Transcript: "Words.", Audio: bytes.NewReader(realtimeProfileWAV(time.Second))}})
			Expect(err).NotTo(HaveOccurred())
			return p
		}
		owned := mk(voiceprofile.Viewer{UserID: "owner-1"}, "owned")
		shared := mk(voiceprofile.AdminViewer(), "shared")
		cfg := &config.ModelConfig{Name: "clone-base", Backend: "qwen3-tts-cpp", TTSConfig: config.TTSConfig{VoiceCloning: ptrTo(true)}}

		// The same owner on a user key, or with a session, still resolves both.
		viewer := realtimeVoiceViewer(handshake(owner, nil))
		for _, p := range []voiceprofile.Profile{owned, shared} {
			_, _, release, err := resolveRealtimeVoice(context.Background(), p.Voice, cfg, store, viewer)
			Expect(err).NotTo(HaveOccurred(), p.Name)
			release()
		}

		// The key of a paired client acts under the account of the owner, with
		// the role of a plain user, and sees neither.
		asUser := *owner
		asUser.Role = auth.RoleUser
		viewer = realtimeVoiceViewer(handshake(&asUser, clientKey))
		Expect(viewer).To(Equal(voiceprofile.NoProfiles()))
		for _, p := range []voiceprofile.Profile{owned, shared} {
			for _, v := range []string{p.Voice, "/etc/passwd"} {
				_, _, release, err := resolveClientRealtimeVoice(context.Background(), v, cfg, store, viewer)
				Expect(err).To(HaveOccurred(), v)
				Expect(release).To(BeNil())
			}
			_, _, release, err := resolveRealtimeVoice(context.Background(), p.Voice, cfg, store, viewer)
			Expect(errors.Is(err, voiceprofile.ErrNotFound)).To(BeTrue(), p.Name)
			Expect(release).To(BeNil())
		}
	})

	It("keeps the viewer of every other caller", func() {
		Expect(realtimeVoiceViewer(handshake(nil, nil))).To(Equal(voiceprofile.AdminViewer()))
		Expect(realtimeVoiceViewer(handshake(&auth.User{ID: "u", Role: auth.RoleUser}, nil))).To(Equal(voiceprofile.Viewer{UserID: "u"}))
		userKey := &auth.UserAPIKey{ID: "k2", UserID: "u"}
		Expect(realtimeVoiceViewer(handshake(&auth.User{ID: "u", Role: auth.RoleUser}, userKey))).To(Equal(voiceprofile.Viewer{UserID: "u"}))
	})

	It("identifies the client of the handshake", func() {
		Expect(realtimeClient(handshake(owner, clientKey))).To(Equal(&RealtimeClient{KeyID: "k1", OwnerID: "owner-1"}))
		Expect(realtimeClient(handshake(owner, nil))).To(BeNil())
	})
})
