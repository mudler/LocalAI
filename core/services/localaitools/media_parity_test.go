package localaitools_test

import (
	"context"
	"encoding/base64"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/localaitools/httpapi"
)

// These specs check that the media tools send the field names that the API
// handlers read, and that a normal user may use every new argument.
var _ = Describe("media tool arguments match the API", func() {
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
		// A user who is not an administrator: the tools must work for them.
		ctx = localaitools.WithUser(context.Background(), &auth.User{ID: "u", Role: auth.RoleUser, Status: auth.StatusActive})
		store.files["/files/a.png"] = localaitools.File{Name: "a.png", Data: pngBytes(4, 4)}
		store.files["/files/b.png"] = localaitools.File{Name: "b.png", Data: pngBytes(2, 2)}
		store.files["/files/a.wav"] = localaitools.File{Name: "a.wav", Data: []byte("RIFFxxxxWAVE")}
	})

	b64 := func(ref string) string { return base64.StdEncoding.EncodeToString(store.files[ref].Data) }

	Describe("generate_image", func() {
		It("sends seed, an init image as file and reference images as ref_images, in plain base64", func() {
			seed := 42
			_, err := cl.GenerateImage(ctx, localaitools.GenerateImageRequest{
				Prompt: "a fox", Model: "m", Seed: &seed,
				Image: "/files/a.png", RefImages: []string{"/files/a.png", "/files/b.png"},
			})
			Expect(err).ToNot(HaveOccurred())
			body := srv.body("/v1/images/generations")
			Expect(body).To(HaveKeyWithValue("seed", BeNumerically("==", 42)))
			Expect(body).To(HaveKeyWithValue("file", b64("/files/a.png")))
			Expect(body["ref_images"]).To(Equal([]any{b64("/files/a.png"), b64("/files/b.png")}))
		})

		It("keeps seed 0 when the caller sets it, and leaves the fields out when not", func() {
			zero := 0
			_, err := cl.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "a fox", Model: "m", Seed: &zero})
			Expect(err).ToNot(HaveOccurred())
			Expect(srv.body("/v1/images/generations")).To(HaveKeyWithValue("seed", BeNumerically("==", 0)))
			_, err = cl.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "a fox", Model: "m"})
			Expect(err).ToNot(HaveOccurred())
			body := srv.body("/v1/images/generations")
			Expect(body).ToNot(HaveKey("seed"))
			Expect(body).ToNot(HaveKey("file"))
			Expect(body).ToNot(HaveKey("ref_images"))
		})

		It("refuses a reference that is not an image or is missing, and too many references", func() {
			_, err := cl.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "p", Model: "m", RefImages: []string{"/files/a.wav"}})
			Expect(err).To(MatchError(ContainSubstring("ref_images[0]")))
			_, err = cl.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "p", Model: "m", Image: "/files/none.png"})
			Expect(err).To(MatchError(ContainSubstring("image")))
			_, err = cl.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "p", Model: "m", RefImages: make([]string, 17)})
			Expect(err).To(MatchError(ContainSubstring("ref_images: at most 16")))
			Expect(srv.seen).To(BeEmpty())
		})

		It("routes by the tool arguments: image alone is img2img, image with mask is inpainting", func() {
			srv.installed["sd"] = []string{"FLAG_IMAGE"}
			call := func(in localaitools.GenerateImageToolInput) error {
				_, err := localaitools.GenerateImageForTest(ctx, cl, in)
				return err
			}
			Expect(call(localaitools.GenerateImageToolInput{Prompt: "p", Image: "/files/a.png"})).To(Succeed())
			Expect(srv.body("/v1/images/generations")).To(HaveKey("file"))
			Expect(srv.seen).ToNot(HaveKey("/v1/images/inpainting"))

			Expect(call(localaitools.GenerateImageToolInput{Prompt: "p", Image: "/files/a.png", Mask: "/files/b.png"})).To(Succeed())
			Expect(srv.seen).To(HaveKey("/v1/images/inpainting"))

			seed := 1
			Expect(call(localaitools.GenerateImageToolInput{Prompt: "p", Image: "/files/a.png", Mask: "/files/b.png", Seed: &seed})).To(MatchError(ContainSubstring("do not work with mask")))
			Expect(call(localaitools.GenerateImageToolInput{Prompt: "p", Mask: "/files/b.png"})).To(MatchError(ContainSubstring("mask needs image")))
		})
	})

	Describe("generate_video", func() {
		It("sends the fields the handler reads: width, height, num_frames, fps, seed, cfg_scale, step", func() {
			seed, cfg := 5, float32(3.5)
			_, err := cl.GenerateVideo(ctx, localaitools.GenerateVideoRequest{
				Prompt: "waves", Model: "vid", Size: "640x360", Seconds: "2", FPS: 12,
				Seed: &seed, CFGScale: &cfg, Steps: 20,
			})
			Expect(err).ToNot(HaveOccurred())
			body := srv.body("/video")
			Expect(body).To(HaveKeyWithValue("width", BeNumerically("==", 640)))
			Expect(body).To(HaveKeyWithValue("height", BeNumerically("==", 360)))
			Expect(body).To(HaveKeyWithValue("num_frames", BeNumerically("==", 24)))
			Expect(body).To(HaveKeyWithValue("fps", BeNumerically("==", 12)))
			Expect(body).To(HaveKeyWithValue("seed", BeNumerically("==", 5)))
			Expect(body).To(HaveKeyWithValue("cfg_scale", BeNumerically("==", 3.5)))
			Expect(body).To(HaveKeyWithValue("step", BeNumerically("==", 20)))
			// The handler ignores these two, so they must not be the only carrier.
			Expect(body).ToNot(HaveKey("size"))
			Expect(body).ToNot(HaveKey("seconds"))
		})

		It("uses 24 fps when seconds come without a rate, and sends nothing for what is not set", func() {
			_, err := cl.GenerateVideo(ctx, localaitools.GenerateVideoRequest{Prompt: "p", Model: "v", Seconds: "1.5"})
			Expect(err).ToNot(HaveOccurred())
			body := srv.body("/video")
			Expect(body).To(HaveKeyWithValue("num_frames", BeNumerically("==", 36)))
			Expect(body).To(HaveKeyWithValue("fps", BeNumerically("==", 24)))
			_, err = cl.GenerateVideo(ctx, localaitools.GenerateVideoRequest{Prompt: "p", Model: "v"})
			Expect(err).ToNot(HaveOccurred())
			body = srv.body("/video")
			for _, k := range []string{"num_frames", "fps", "width", "height", "seed", "cfg_scale", "step"} {
				Expect(body).ToNot(HaveKey(k), k)
			}
		})

		It("sends start and end images, audio and references as data URLs, and reference videos as frame lists", func() {
			_, err := cl.GenerateVideo(ctx, localaitools.GenerateVideoRequest{
				Prompt: "p", Model: "v",
				RefImages: []string{"/files/a.png"}, RefAudios: []string{"/files/a.wav"},
				RefVideos: []localaitools.VideoRefInput{{Frames: []string{"/files/a.png", "/files/b.png"}, FPS: 8, Audio: "/files/a.wav"}},
			})
			Expect(err).ToNot(HaveOccurred())
			body := srv.body("/video")
			Expect(body["ref_images"]).To(HaveLen(1))
			Expect(body["ref_images"].([]any)[0]).To(HavePrefix("data:image/png;base64,"))
			Expect(body["ref_audios"]).To(HaveLen(1))
			vids := body["ref_videos"].([]any)
			Expect(vids).To(HaveLen(1))
			v := vids[0].(map[string]any)
			Expect(v["frames"]).To(HaveLen(2))
			Expect(v).To(HaveKeyWithValue("fps", BeNumerically("==", 8)))
			Expect(v["audio"]).To(HavePrefix("data:"))

			_, err = cl.GenerateVideo(ctx, localaitools.GenerateVideoRequest{Prompt: "p", Model: "v", StartImage: "/files/a.png", EndImage: "/files/b.png", Audio: "/files/a.wav"})
			Expect(err).ToNot(HaveOccurred())
			body = srv.body("/video")
			Expect(body).To(HaveKey("start_image"))
			Expect(body).To(HaveKey("end_image"))
			Expect(body["audio"]).To(HavePrefix("data:"))
			Expect(body).ToNot(HaveKey("ref_images"))
		})

		It("never forwards a file path, only the bytes of files from the store", func() {
			_, err := cl.GenerateVideo(ctx, localaitools.GenerateVideoRequest{Prompt: "p", Model: "v", RefImages: []string{"/etc/passwd"}})
			Expect(err).To(HaveOccurred())
			Expect(srv.seen).To(BeEmpty())
			_, err = cl.GenerateVideo(ctx, localaitools.GenerateVideoRequest{Prompt: "p", Model: "v", RefImages: []string{"/files/a.png"}})
			Expect(err).ToNot(HaveOccurred())
			Expect(string(srv.seen["/video"])).ToNot(ContainSubstring("/files/"))
			Expect(srv.body("/video")).ToNot(HaveKey("params"))
		})

		It("refuses what the handler would refuse, before it sends anything", func() {
			cases := map[string]localaitools.GenerateVideoRequest{
				"size":           {Prompt: "p", Model: "v", Size: "big"},
				"at most 16":     {Prompt: "p", Model: "v", RefImages: make([]string, 17)},
				"cannot be used": {Prompt: "p", Model: "v", StartImage: "/files/a.png", RefImages: []string{"/files/b.png"}},
				"frames":         {Prompt: "p", Model: "v", RefVideos: []localaitools.VideoRefInput{{}}},
				"cfg_scale":      {Prompt: "p", Model: "v", CFGScale: func() *float32 { v := float32(-1); return &v }()},
				"steps":          {Prompt: "p", Model: "v", Steps: 100000},
				"audio":          {Prompt: "p", Model: "v", Audio: "/files/a.png"},
			}
			for want, req := range cases {
				_, err := cl.GenerateVideo(ctx, req)
				Expect(err).To(MatchError(ContainSubstring(want)), want)
			}
			Expect(srv.seen).To(BeEmpty())
		})
	})

	Describe("generate_sound", func() {
		It("sends every field of the route, under its API name", func() {
			dur, infl := float32(30), float32(0.7)
			bpm, yes, no := 120, true, false
			_, err := cl.GenerateSound(ctx, localaitools.GenerateSoundRequest{
				Text: "rain", Model: "snd", Duration: &dur, Lyrics: "la la", Instrumental: &no,
				Caption: "calm piano", BPM: &bpm, Keyscale: "C major", Timesignature: "4/4",
				Language: "en", VocalLanguage: "it", Think: &yes, DoSample: &yes, PromptInfluence: &infl,
				ExpansionModel: "expander",
			})
			Expect(err).ToNot(HaveOccurred())
			body := srv.body("/v1/sound-generation")
			Expect(body).To(HaveKeyWithValue("model_id", "snd"))
			Expect(body).To(HaveKeyWithValue("text", "rain"))
			Expect(body).To(HaveKeyWithValue("duration_seconds", BeNumerically("==", 30)))
			Expect(body).To(HaveKeyWithValue("lyrics", "la la"))
			Expect(body).To(HaveKeyWithValue("instrumental", false))
			Expect(body).To(HaveKeyWithValue("caption", "calm piano"))
			Expect(body).To(HaveKeyWithValue("bpm", BeNumerically("==", 120)))
			Expect(body).To(HaveKeyWithValue("keyscale", "C major"))
			Expect(body).To(HaveKeyWithValue("timesignature", "4/4"))
			Expect(body).To(HaveKeyWithValue("language", "en"))
			Expect(body).To(HaveKeyWithValue("vocal_language", "it"))
			Expect(body).To(HaveKeyWithValue("think", true))
			Expect(body).To(HaveKeyWithValue("do_sample", true))
			Expect(body).To(HaveKeyWithValue("prompt_influence", BeNumerically("~", 0.7, 0.001)))
			Expect(body).To(HaveKeyWithValue("expansion_model", "expander"))
			// The route has no source audio on purpose, and the tool has no way to send one.
			for _, k := range []string{"src", "source", "audio", "params"} {
				Expect(body).ToNot(HaveKey(k), k)
			}
		})

		It("sends nothing for what is not set, and refuses a bad bpm or influence", func() {
			_, err := cl.GenerateSound(ctx, localaitools.GenerateSoundRequest{Text: "rain", Model: "snd"})
			Expect(err).ToNot(HaveOccurred())
			Expect(srv.body("/v1/sound-generation")).To(HaveLen(2))
			bad, big := 5, float32(9)
			_, err = cl.GenerateSound(ctx, localaitools.GenerateSoundRequest{Text: "x", Model: "snd", BPM: &bad})
			Expect(err).To(MatchError(ContainSubstring("bpm")))
			_, err = cl.GenerateSound(ctx, localaitools.GenerateSoundRequest{Text: "x", Model: "snd", PromptInfluence: &big})
			Expect(err).To(MatchError(ContainSubstring("prompt_influence")))
		})
	})

	Describe("speak", func() {
		It("sends sample_rate, and refuses a rate out of range", func() {
			_, err := cl.Speak(ctx, localaitools.SpeakRequest{Text: "hi", Model: "tts", SampleRate: 24000})
			Expect(err).ToNot(HaveOccurred())
			Expect(srv.body("/v1/audio/speech")).To(HaveKeyWithValue("sample_rate", BeNumerically("==", 24000)))
			_, err = cl.Speak(ctx, localaitools.SpeakRequest{Text: "hi", Model: "tts"})
			Expect(err).ToNot(HaveOccurred())
			Expect(srv.body("/v1/audio/speech")).ToNot(HaveKey("sample_rate"))
			for _, bad := range []int{-1, 100, 1000000} {
				_, err = cl.Speak(ctx, localaitools.SpeakRequest{Text: "hi", Model: "tts", SampleRate: bad})
				Expect(err).To(MatchError(ContainSubstring("sample_rate")))
			}
		})
	})

	Describe("detect", func() {
		It("sends points and boxes as the flat lists the route reads", func() {
			_, err := cl.Detect(ctx, localaitools.DetectRequest{Image: "/files/a.png", Model: "sam", Points: []float32{10, 20, 1, 30, 40, 0}, Boxes: []float32{1, 2, 3, 4}})
			Expect(err).ToNot(HaveOccurred())
			body := srv.body("/v1/detection")
			Expect(body["points"]).To(Equal([]any{10.0, 20.0, 1.0, 30.0, 40.0, 0.0}))
			Expect(body["boxes"]).To(Equal([]any{1.0, 2.0, 3.0, 4.0}))
			_, err = cl.Detect(ctx, localaitools.DetectRequest{Image: "/files/a.png", Model: "sam"})
			Expect(err).ToNot(HaveOccurred())
			Expect(srv.body("/v1/detection")).ToNot(HaveKey("points"))
		})

		It("refuses lists that are not whole groups, labels other than 0 and 1, and too many groups", func() {
			cases := map[string]localaitools.DetectRequest{
				"points":  {Image: "/files/a.png", Model: "m", Points: []float32{1, 2}},
				"label":   {Image: "/files/a.png", Model: "m", Points: []float32{1, 2, 5}},
				"boxes":   {Image: "/files/a.png", Model: "m", Boxes: []float32{1, 2, 3}},
				"finite":  {Image: "/files/a.png", Model: "m", Boxes: []float32{1, 2, 3, -4}},
				"at most": {Image: "/files/a.png", Model: "m", Boxes: make([]float32, 4*65)},
			}
			for want, req := range cases {
				_, err := cl.Detect(ctx, req)
				Expect(err).To(MatchError(ContainSubstring(want)), want)
			}
			Expect(srv.seen).To(BeEmpty())
		})
	})

	Describe("voice and face verify", func() {
		It("send anti_spoofing only when asked", func() {
			_, err := cl.VerifyFace(ctx, localaitools.VerifyFaceRequest{Image1: "/files/a.png", Image2: "/files/b.png", Model: "f", AntiSpoofing: true})
			Expect(err).ToNot(HaveOccurred())
			Expect(srv.body("/v1/face/verify")).To(HaveKeyWithValue("anti_spoofing", true))
			_, err = cl.VerifyVoice(ctx, localaitools.VoiceVerifyRequest{Audio1: "/files/a.wav", Audio2: "/files/a.wav", Model: "v", AntiSpoofing: true})
			Expect(err).ToNot(HaveOccurred())
			Expect(srv.body("/v1/voice/verify")).To(HaveKeyWithValue("anti_spoofing", true))
			_, err = cl.VerifyFace(ctx, localaitools.VerifyFaceRequest{Image1: "/files/a.png", Image2: "/files/b.png", Model: "f"})
			Expect(err).ToNot(HaveOccurred())
			Expect(srv.body("/v1/face/verify")).ToNot(HaveKey("anti_spoofing"))
		})

		It("reports the liveness fields of the answer, and leaves them out when the route sent none", func() {
			res, err := cl.VerifyFace(ctx, localaitools.VerifyFaceRequest{Image1: "/files/a.png", Image2: "/files/b.png", Model: "f"})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Img1IsReal).To(BeNil())
			raw, _ := json.Marshal(res)
			Expect(string(raw)).ToNot(ContainSubstring("is_real"))
			srv.verifyExtra = `,"img1_is_real":true,"img1_antispoof_score":0.97,"img2_is_real":false,"img2_antispoof_score":0.1`
			res, err = cl.VerifyFace(ctx, localaitools.VerifyFaceRequest{Image1: "/files/a.png", Image2: "/files/b.png", Model: "f", AntiSpoofing: true})
			Expect(err).ToNot(HaveOccurred())
			Expect(*res.Img1IsReal).To(BeTrue())
			Expect(*res.Img2IsReal).To(BeFalse())
			Expect(*res.Img1AntispoofScore).To(BeNumerically("~", 0.97, 0.001))
		})
	})
})
