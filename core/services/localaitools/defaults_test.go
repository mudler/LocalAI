package localaitools_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/localaitools/httpapi"
)

// The tools that make or read media pick their model through one function.
// These specs run the REST client against a stand-in server, so they cover
// the rule once for each tool and the same code that the in-process client
// uses.
var _ = Describe("the default model of a client tool", func() {
	var (
		srv   *localAIStandIn
		store *memStore
		cl    *httpapi.Client
		ctx   context.Context
	)

	BeforeEach(func() {
		srv = newStandIn()
		DeferCleanup(srv.Close)
		store = newMemStore()
		cl = httpapi.New(srv.URL, "secret")
		cl.Files = store
		ctx = context.Background()
		store.files["/files/a.png"] = localaitools.File{Name: "a.png", Data: pngBytes(2, 2)}
		store.files["/files/a.wav"] = localaitools.File{Name: "a.wav", Data: []byte("RIFFxxxxWAVE")}
		for name, caps := range map[string][]string{
			"first-img": {"FLAG_IMAGE"}, "flux": {"FLAG_IMAGE"}, "kokoro": {"FLAG_TTS"}, "first-tts": {"FLAG_TTS"},
			"whisper": {"FLAG_TRANSCRIPT"}, "bge-m3": {"FLAG_EMBEDDINGS"}, "bge-rerank": {"FLAG_RERANK"},
			"vid": {"FLAG_VIDEO"}, "qwen-vl": {"FLAG_VISION"}, "aaa-vl": {"FLAG_VISION"},
		} {
			srv.installed[name] = caps
		}
	})

	defaults := func(m map[string]any) { srv.wellknown["defaults"] = m }

	// each tool calls the API with the model it was given.
	type run struct {
		route string
		call  func(model string) (string, string, error) // the model and the note of the result
	}
	runs := map[string]run{
		"image": {"/v1/images/generations", func(m string) (string, string, error) {
			r, err := cl.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "a fox", Model: m})
			if err != nil {
				return "", "", err
			}
			return r.Model, r.ModelNote, nil
		}},
		"tts": {"/v1/audio/speech", func(m string) (string, string, error) {
			r, err := cl.Speak(ctx, localaitools.SpeakRequest{Text: "hi", Model: m})
			if err != nil {
				return "", "", err
			}
			return r.Model, r.ModelNote, nil
		}},
		"stt": {"/v1/audio/transcriptions", func(m string) (string, string, error) {
			r, err := cl.TranscribeAudio(ctx, localaitools.TranscribeAudioRequest{Audio: "/files/a.wav", Model: m})
			if err != nil {
				return "", "", err
			}
			return r.Model, r.ModelNote, nil
		}},
		"embeddings": {"/v1/embeddings", func(m string) (string, string, error) {
			r, err := cl.Embed(ctx, localaitools.EmbedRequest{Input: []string{"a"}, Model: m})
			if err != nil {
				return "", "", err
			}
			return r.Model, r.ModelNote, nil
		}},
		"rerank": {"/v1/rerank", func(m string) (string, string, error) {
			r, err := cl.Rerank(ctx, localaitools.RerankRequest{Query: "q", Documents: []string{"a"}, Model: m})
			if err != nil {
				return "", "", err
			}
			return r.Model, r.ModelNote, nil
		}},
		"video": {"/video", func(m string) (string, string, error) {
			r, err := cl.GenerateVideo(ctx, localaitools.GenerateVideoRequest{Prompt: "waves", Model: m})
			if err != nil {
				return "", "", err
			}
			return r.Model, r.ModelNote, nil
		}},
		"vision": {"/v1/chat/completions", func(m string) (string, string, error) {
			r, err := cl.DescribeImage(ctx, localaitools.DescribeImageRequest{Image: "/files/a.png", Model: m})
			if err != nil {
				return "", "", err
			}
			return r.Model, r.ModelNote, nil
		}},
	}
	// The models each key holds in these specs, and one other installed model of that kind.
	installed := map[string]string{
		"image": "flux", "tts": "kokoro", "stt": "whisper", "embeddings": "bge-m3",
		"rerank": "bge-rerank", "video": "vid", "vision": "qwen-vl",
	}

	for key, r := range runs {
		key, r := key, r
		Describe(key, func() {
			It("uses the default model, sends it to the API and says which default it used", func() {
				defaults(map[string]any{key: installed[key]})
				model, note, err := r.call("")
				Expect(err).ToNot(HaveOccurred())
				Expect(model).To(Equal(installed[key]))
				Expect(note).To(And(ContainSubstring(installed[key]), ContainSubstring("default")))
				Expect(string(srv.seen[r.route])).To(ContainSubstring(installed[key]))
			})

			It("stops and says to ask the user when the default is ask, and calls nothing", func() {
				defaults(map[string]any{key: "ask"})
				_, _, err := r.call("")
				var ask *localaitools.AskModelError
				Expect(err).To(BeAssignableToTypeOf(ask))
				Expect(err).To(MatchError(And(ContainSubstring("asked which"), ContainSubstring("model argument"))))
				Expect(srv.seen).ToNot(HaveKey(r.route))
			})

			It("picks an installed model itself when the key is null or missing, as before", func() {
				defaults(map[string]any{key: nil})
				model, note, err := r.call("")
				Expect(err).ToNot(HaveOccurred())
				Expect(model).ToNot(BeEmpty())
				Expect(note).To(BeEmpty())
				defaults(map[string]any{})
				_, _, err = r.call("")
				Expect(err).ToNot(HaveOccurred())
			})

			It("refuses a default that is not installed any more, without using another model", func() {
				defaults(map[string]any{key: "gone-model"})
				_, _, err := r.call("")
				var gone *localaitools.DefaultGoneError
				Expect(err).To(BeAssignableToTypeOf(gone))
				Expect(err).To(MatchError(And(ContainSubstring(`"gone-model"`), ContainSubstring("no other model was used"), ContainSubstring("Account > Defaults"))))
				Expect(srv.seen).ToNot(HaveKey(r.route))
			})

			It("lets one request name another model and leaves the default alone", func() {
				defaults(map[string]any{key: installed[key]})
				model, note, err := r.call("other-model")
				Expect(err).ToNot(HaveOccurred())
				Expect(model).To(Equal("other-model"))
				Expect(note).To(BeEmpty())
				Expect(srv.wellknown["defaults"]).To(HaveKeyWithValue(key, installed[key]))
			})

			It("lets one request name a model when the default is ask", func() {
				defaults(map[string]any{key: "ask"})
				model, _, err := r.call("chosen")
				Expect(err).ToNot(HaveOccurred())
				Expect(model).To(Equal("chosen"))
			})
		})
	}

	It("does not read the default of another kind of work", func() {
		defaults(map[string]any{"tts": "kokoro", "image": "ask"})
		model, _, err := runs["video"].call("")
		Expect(err).ToNot(HaveOccurred())
		Expect(model).To(Equal("vid"))
	})

	It("leaves the tools without a default alone", func() {
		defaults(map[string]any{"image": "ask", "stt": "ask"})
		srv.installed["det"] = []string{"FLAG_DETECTION"}
		_, err := cl.Detect(ctx, localaitools.DetectRequest{Image: "/files/a.png"})
		Expect(err).ToNot(HaveOccurred())
	})

	It("uses the vision default for describe_image and keeps the old choice without one", func() {
		_, _, err := runs["vision"].call("")
		Expect(err).ToNot(HaveOccurred())
		Expect(srv.body("/v1/chat/completions")).To(HaveKeyWithValue("model", "aaa-vl"))
		defaults(map[string]any{"vision": "qwen-vl"})
		_, _, err = runs["vision"].call("")
		Expect(err).ToNot(HaveOccurred())
		Expect(srv.body("/v1/chat/completions")).To(HaveKeyWithValue("model", "qwen-vl"))
	})

	It("uses a default of a disabled model as not installed", func() {
		srv.mu.Lock()
		srv.disabled = map[string]bool{"flux": true}
		srv.mu.Unlock()
		defaults(map[string]any{"image": "flux"})
		_, _, err := runs["image"].call("")
		Expect(err).To(BeAssignableToTypeOf(&localaitools.DefaultGoneError{}))
	})
})
