package localaitools_test

import (
	"context"
	"encoding/base64"

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
})
