package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/utils"
)

// b64 is the base64 encoding a spec expects the proxy to have produced from
// a local file's content, matching what the REST endpoints take inline.
func b64(content string) string {
	return base64.StdEncoding.EncodeToString([]byte(content))
}

var _ = Describe("media methods", func() {
	var up *fakeUpstream

	BeforeEach(func() {
		up = newFakeUpstream()
		DeferCleanup(up.Close)
	})

	Describe("GenerateImage", func() {
		It("posts base64 src/ref_images and decodes data[0].b64_json into Dst", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/images/generations", map[string]any{
				"data": []map[string]any{{"b64_json": b64("png-bytes")}},
			})
			src := writeInput("src.png", "src-bytes")
			ref := writeInput("ref.png", "ref-bytes")
			dst := filepath.Join(GinkgoT().TempDir(), "out.png")

			Expect(p.GenerateImage(&pb.GenerateImageRequest{
				Width: 64, Height: 32, Step: 20, Seed: 7,
				PositivePrompt: "a cat", NegativePrompt: "blurry",
				Src: src, RefImages: []string{ref}, Dst: dst,
			})).To(Succeed())

			got, err := os.ReadFile(dst)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(got)).To(Equal("png-bytes"))

			req := up.last()
			Expect(req.Path).To(Equal("/v1/images/generations"))
			Expect(req.JSON).To(HaveKeyWithValue("model", "remote-model"))
			Expect(req.JSON).To(HaveKeyWithValue("prompt", "a cat"))
			Expect(req.JSON).To(HaveKeyWithValue("negative_prompt", "blurry"))
			Expect(req.JSON).To(HaveKeyWithValue("size", "64x32"))
			Expect(req.JSON).To(HaveKeyWithValue("step", BeNumerically("==", 20)))
			Expect(req.JSON).To(HaveKeyWithValue("seed", BeNumerically("==", 7)))
			Expect(req.JSON).To(HaveKeyWithValue("response_format", "b64_json"))
			Expect(req.JSON).To(HaveKeyWithValue("file", b64("src-bytes")))
			Expect(req.JSON).To(HaveKeyWithValue("ref_images", ConsistOf(b64("ref-bytes"))))
		})

		It("downloads a URL reply relative to the upstream into Dst", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/images/generations", map[string]any{
				"data": []map[string]any{{"url": up.URL + "/generated-images/out.png"}},
			})
			up.script("/generated-images/out.png", scriptedResponse{Status: http.StatusOK, ContentType: "image/png", Body: "downloaded-bytes"})
			dst := filepath.Join(GinkgoT().TempDir(), "out.png")

			Expect(p.GenerateImage(&pb.GenerateImageRequest{PositivePrompt: "a cat", Dst: dst})).To(Succeed())

			got, err := os.ReadFile(dst)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(got)).To(Equal("downloaded-bytes"))
		})

		It("maps an upstream failure and leaves no partial file", func() {
			p := loadProxy(up, nil)
			up.script("/v1/images/generations", scriptedResponse{Status: http.StatusInternalServerError, Body: "boom"})
			dst := filepath.Join(GinkgoT().TempDir(), "out.png")

			err := p.GenerateImage(&pb.GenerateImageRequest{PositivePrompt: "x", Dst: dst})
			Expect(codeOf(err)).To(Equal(codes.Unavailable))
			Expect(dst).NotTo(BeAnExistingFile())
		})

		It("downloads a reply URL from the configured upstream even when its host does not match", func() {
			// A reverse proxy, LOCALAI_BASE_URL, or X-Forwarded-Host can all make
			// the upstream hand back a URL on a different host than the one this
			// proxy is configured with. Only the path should matter: the proxy
			// must still fetch it from cfg.base (this fake upstream), with its
			// own bearer key, never from the host named in the URL.
			p := loadProxy(up, nil)
			up.replyJSON("/v1/images/generations", map[string]any{
				"data": []map[string]any{{"url": "http://mismatched-host.invalid:1/generated-images/out.png"}},
			})
			up.script("/generated-images/out.png", scriptedResponse{Status: http.StatusOK, ContentType: "image/png", Body: "downloaded-bytes"})
			dst := filepath.Join(GinkgoT().TempDir(), "out.png")

			Expect(p.GenerateImage(&pb.GenerateImageRequest{PositivePrompt: "a cat", Dst: dst})).To(Succeed())

			got, err := os.ReadFile(dst)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(got)).To(Equal("downloaded-bytes"))
		})

		It("refuses a reply URL whose path is not a known generated-content path", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/images/generations", map[string]any{
				"data": []map[string]any{{"url": up.URL + "/etc/passwd"}},
			})
			dst := filepath.Join(GinkgoT().TempDir(), "out.png")

			err := p.GenerateImage(&pb.GenerateImageRequest{PositivePrompt: "a cat", Dst: dst})
			Expect(codeOf(err)).To(Equal(codes.InvalidArgument))
			Expect(dst).NotTo(BeAnExistingFile())
		})
	})

	Describe("UpscaleImage", func() {
		It("uploads the image as multipart and downloads the returned URL into Dst", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/images/upscale", map[string]any{
				"data": []map[string]any{{"url": up.URL + "/generated-images/big.png"}},
			})
			up.script("/generated-images/big.png", scriptedResponse{Status: http.StatusOK, ContentType: "image/png", Body: "big-bytes"})
			src := writeInput("small.png", "small-bytes")
			dst := filepath.Join(GinkgoT().TempDir(), "big.png")

			Expect(p.UpscaleImage(&pb.UpscaleImageRequest{Src: src, Dst: dst, Scale: 4})).To(Succeed())

			got, err := os.ReadFile(dst)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(got)).To(Equal("big-bytes"))

			req := up.recorded()[0]
			Expect(req.Path).To(Equal("/v1/images/upscale"))
			Expect(req.Fields).To(HaveKeyWithValue("model", "remote-model"))
			Expect(req.Fields).To(HaveKeyWithValue("scale", "4"))
			Expect(req.Files).To(HaveKeyWithValue("image", "small-bytes"))
		})
	})

	Describe("GenerateVideo", func() {
		It("posts base64 media fields and decodes data[0].b64_json into Dst", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/video", map[string]any{
				"data": []map[string]any{{"b64_json": b64("mp4-bytes")}},
			})
			start := writeInput("start.png", "start-bytes")
			dst := filepath.Join(GinkgoT().TempDir(), "out.mp4")

			Expect(p.GenerateVideo(&pb.GenerateVideoRequest{
				Prompt: "a dog running", NegativePrompt: "static", StartImage: start,
				Width: 512, Height: 288, NumFrames: 24, Fps: 8, Seed: 3, CfgScale: 5.5, Step: 10,
				Dst: dst, Params: map[string]string{"motion": "high"},
			})).To(Succeed())

			got, err := os.ReadFile(dst)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(got)).To(Equal("mp4-bytes"))

			req := up.last()
			Expect(req.Path).To(Equal("/video"))
			Expect(req.JSON).To(HaveKeyWithValue("model", "remote-model"))
			Expect(req.JSON).To(HaveKeyWithValue("prompt", "a dog running"))
			Expect(req.JSON).To(HaveKeyWithValue("start_image", b64("start-bytes")))
			Expect(req.JSON).To(HaveKeyWithValue("num_frames", BeNumerically("==", 24)))
			Expect(req.JSON).To(HaveKeyWithValue("fps", BeNumerically("==", 8)))
			Expect(req.JSON).To(HaveKeyWithValue("params", HaveKeyWithValue("motion", "high")))
		})
	})

	Describe("Generate3D", func() {
		It("posts the base64 conditioning image and decodes data[0].b64_json into Dst", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/3d/generations", map[string]any{
				"data": []map[string]any{{"b64_json": b64("glb-bytes")}},
			})
			src := writeInput("cond.png", "cond-bytes")
			dst := filepath.Join(GinkgoT().TempDir(), "out.glb")

			Expect(p.Generate3D(&pb.Generate3DRequest{
				Src: src, Dst: dst, Seed: 1, Step: 12, CfgScale: 7.5, TextureSteps: 12, Quality: "auto", Background: "keep",
			})).To(Succeed())

			got, err := os.ReadFile(dst)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(got)).To(Equal("glb-bytes"))

			req := up.last()
			Expect(req.Path).To(Equal("/3d/generations"))
			Expect(req.JSON).To(HaveKeyWithValue("image", b64("cond-bytes")))
			Expect(req.JSON).To(HaveKeyWithValue("quality", "auto"))
			Expect(req.JSON).To(HaveKeyWithValue("background", "keep"))
		})
	})

	Describe("Animate3D", func() {
		It("base64-encodes non-text inputs, keeps text raw, and writes the reply to Dst", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/3d/animate", map[string]any{
				"data":     []map[string]any{{"b64_json": b64("anim-bytes")}},
				"metadata": map[string]any{"frames": float64(30)},
			})
			mesh := writeInput("mesh.glb", "mesh-bytes")
			dst := filepath.Join(GinkgoT().TempDir(), "out.glb")

			Expect(p.Animate3D(&pb.Animate3DRequest{
				Inputs: map[string]*pb.AnimationInput{
					"mesh":   {Type: "mesh", Data: mesh},
					"prompt": {Type: "text", Data: "wave hello"},
				},
				Dst: dst,
			})).To(Succeed())

			got, err := os.ReadFile(dst)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(got)).To(Equal("anim-bytes"))

			req := up.last()
			Expect(req.Path).To(Equal("/3d/animate"))
			Expect(req.JSON["inputs"]).To(HaveKeyWithValue("mesh", map[string]any{"type": "mesh", "data": b64("mesh-bytes")}))
			Expect(req.JSON["inputs"]).To(HaveKeyWithValue("prompt", map[string]any{"type": "text", "data": "wave hello"}))
		})

		It("Animate3DWithMetadata returns the upstream metadata and writes Dst", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/3d/animate", map[string]any{
				"data":     []map[string]any{{"b64_json": b64("anim-bytes")}},
				"metadata": map[string]any{"frames": float64(30)},
			})
			dst := filepath.Join(GinkgoT().TempDir(), "out.glb")

			metadata, err := p.Animate3DWithMetadata(&pb.Animate3DRequest{
				Inputs: map[string]*pb.AnimationInput{"prompt": {Type: "text", Data: "wave"}},
				Dst:    dst,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(metadata)).To(MatchJSON(`{"frames":30}`))
			Expect(os.ReadFile(dst)).To(BeEquivalentTo("anim-bytes"))
		})
	})

	// pngBytes is a minimal payload with a real PNG magic number, so
	// http.DetectContentType (which toDataURI uses) sniffs "image/png" the
	// way it would for a real image, and decodeAndCheckImage below can tell
	// this test wrote a valid data URI apart from one that merely echoes bare
	// base64.
	pngBytes := append([]byte("\x89PNG\r\n\x1a\n"), []byte("fake-png-body")...)

	// decodeAndCheckImage extracts field from an upstream request body and
	// decodes it exactly the way the real REST handlers do — with
	// utils.GetContentURIAsBase64 (core/http/endpoints/localai/images.go's
	// decodeImageInput calls the same function) — so a spec here fails the
	// same way a live upstream would if the proxy ever regressed to sending
	// bare base64, which that function rejects outright.
	decodeAndCheckImage := func(body map[string]any, field string, want []byte) {
		raw, _ := body[field].(string)
		decoded, err := utils.GetContentURIAsBase64(raw)
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "the real upstream would reject %s the same way", field)
		got, err := base64.StdEncoding.DecodeString(decoded)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		ExpectWithOffset(1, got).To(Equal(want))
	}

	Describe("Detect", func() {
		It("wraps the bare base64 image as a data URI the real upstream decoder accepts, and maps detections", func() {
			up = newFakeUpstreamWithHandler(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
				decodeAndCheckImage(body, "image", pngBytes)
				Expect(body["prompt"]).To(Equal("cat"))

				mask := base64.StdEncoding.EncodeToString([]byte("png-mask"))
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"detections": []map[string]any{
						{"x": 1.0, "y": 2.0, "width": 3.0, "height": 4.0, "confidence": 0.9, "class_name": "cat", "mask": mask},
					},
				})
			})
			DeferCleanup(up.Close)
			p := loadProxy(up, nil)

			res, err := p.Detect(&pb.DetectOptions{
				Src: base64.StdEncoding.EncodeToString(pngBytes), Prompt: "cat", Threshold: 0.5,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Detections).To(HaveLen(1))
			Expect(res.Detections[0].ClassName).To(Equal("cat"))
			Expect(res.Detections[0].Confidence).To(BeNumerically("~", 0.9, 1e-6))
			Expect(res.Detections[0].Mask).To(Equal([]byte("png-mask")))
		})
	})

	Describe("Depth", func() {
		It("wraps the bare base64 image as a data URI and maps the full depth response", func() {
			up = newFakeUpstreamWithHandler(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
				decodeAndCheckImage(body, "image", pngBytes)
				Expect(body["include_depth"]).To(Equal(true))

				colors := base64.StdEncoding.EncodeToString([]byte("rgb"))
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"width": 2, "height": 1, "depth": []float64{0.1, 0.2},
					"point_colors": colors, "is_metric": true,
				})
			})
			DeferCleanup(up.Close)
			p := loadProxy(up, nil)

			res, err := p.Depth(&pb.DepthRequest{Src: base64.StdEncoding.EncodeToString(pngBytes), IncludeDepth: true})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Width).To(Equal(int32(2)))
			Expect(res.Height).To(Equal(int32(1)))
			Expect(res.Depth).To(Equal([]float32{0.1, 0.2}))
			Expect(res.PointColors).To(Equal([]byte("rgb")))
			Expect(res.IsMetric).To(BeTrue())
		})

		It("refuses a request for exports or a dst directory without calling the upstream, so failover moves on", func() {
			p := loadProxy(up, nil)

			_, exportsErr := p.Depth(&pb.DepthRequest{Src: "irrelevant", Exports: []string{"glb"}})
			Expect(codeOf(exportsErr)).To(Equal(codes.Unimplemented))

			_, dstErr := p.Depth(&pb.DepthRequest{Src: "irrelevant", Dst: "/some/output/dir"})
			Expect(codeOf(dstErr)).To(Equal(codes.Unimplemented))

			Expect(up.recorded()).To(BeEmpty(), "an exports/dst request must never reach the upstream")
		})
	})

	Describe("FaceVerify", func() {
		It("wraps both bare base64 images as data URIs and maps the response, including liveness fields", func() {
			img2Bytes := append([]byte("\x89PNG\r\n\x1a\n"), []byte("other-face")...)
			up = newFakeUpstreamWithHandler(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
				decodeAndCheckImage(body, "img1", pngBytes)
				decodeAndCheckImage(body, "img2", img2Bytes)
				Expect(body["anti_spoofing"]).To(Equal(true))

				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"verified": true, "distance": 0.1, "threshold": 0.4, "confidence": 92.0, "model": "buffalo_l",
					"img1_area":    map[string]any{"x": 1.0, "y": 2.0, "w": 3.0, "h": 4.0},
					"img2_area":    map[string]any{"x": 5.0, "y": 6.0, "w": 7.0, "h": 8.0},
					"img1_is_real": true, "img1_antispoof_score": 0.99,
					"img2_is_real": false, "img2_antispoof_score": 0.1,
				})
			})
			DeferCleanup(up.Close)
			p := loadProxy(up, nil)

			res, err := p.FaceVerify(&pb.FaceVerifyRequest{
				Img1: base64.StdEncoding.EncodeToString(pngBytes), Img2: base64.StdEncoding.EncodeToString(img2Bytes),
				AntiSpoofing: true,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Verified).To(BeTrue())
			Expect(res.Model).To(Equal("buffalo_l"))
			Expect(res.Img1Area.W).To(BeNumerically("==", 3))
			Expect(res.Img1IsReal).To(BeTrue())
			Expect(res.Img2IsReal).To(BeFalse())
		})
	})

	Describe("FaceAnalyze", func() {
		It("wraps the bare base64 image as a data URI and maps per-face demographic attributes", func() {
			up = newFakeUpstreamWithHandler(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
				decodeAndCheckImage(body, "img", pngBytes)
				Expect(body["actions"]).To(ConsistOf("age", "gender"))

				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"faces": []map[string]any{
						{
							"region":          map[string]any{"x": 1.0, "y": 2.0, "w": 3.0, "h": 4.0},
							"face_confidence": 0.95, "age": 30.0, "dominant_gender": "Man",
							"gender": map[string]any{"Man": 0.9, "Woman": 0.1},
						},
					},
				})
			})
			DeferCleanup(up.Close)
			p := loadProxy(up, nil)

			res, err := p.FaceAnalyze(&pb.FaceAnalyzeRequest{
				Img: base64.StdEncoding.EncodeToString(pngBytes), Actions: []string{"age", "gender"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Faces).To(HaveLen(1))
			Expect(res.Faces[0].DominantGender).To(Equal("Man"))
			Expect(res.Faces[0].Gender).To(HaveKeyWithValue("Man", Equal(float32(0.9))))
		})
	})

	Describe("VoiceVerify", func() {
		It("base64-encodes both audio clips and maps the response", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/voice/verify", map[string]any{
				"verified": true, "distance": 0.2, "threshold": 0.5, "confidence": 88.0, "model": "ecapa",
			})
			a1 := writeInput("a1.wav", "audio-one")
			a2 := writeInput("a2.wav", "audio-two")

			res, err := p.VoiceVerify(&pb.VoiceVerifyRequest{Audio1: a1, Audio2: a2, Threshold: 0.5})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Verified).To(BeTrue())
			Expect(res.Model).To(Equal("ecapa"))

			req := up.last()
			Expect(req.Path).To(Equal("/v1/voice/verify"))
			Expect(req.JSON).To(HaveKeyWithValue("audio1", b64("audio-one")))
			Expect(req.JSON).To(HaveKeyWithValue("audio2", b64("audio-two")))
		})
	})

	Describe("VoiceAnalyze", func() {
		It("base64-encodes the audio clip and maps demographic segments", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/voice/analyze", map[string]any{
				"segments": []map[string]any{
					{"start": 0.0, "end": 1.5, "age": 25.0, "dominant_gender": "Woman"},
				},
			})
			audio := writeInput("clip.wav", "clip-bytes")

			res, err := p.VoiceAnalyze(&pb.VoiceAnalyzeRequest{Audio: audio, Actions: []string{"age"}})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Segments).To(HaveLen(1))
			Expect(res.Segments[0].DominantGender).To(Equal("Woman"))

			req := up.last()
			Expect(req.Path).To(Equal("/v1/voice/analyze"))
			Expect(req.JSON).To(HaveKeyWithValue("audio", b64("clip-bytes")))
		})
	})

	Describe("VoiceEmbed", func() {
		It("base64-encodes the audio clip and returns the embedding", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/voice/embed", map[string]any{
				"embedding": []float64{0.1, 0.2, 0.3}, "dim": 3.0, "model": "ecapa",
			})
			audio := writeInput("clip.wav", "clip-bytes")

			res, err := p.VoiceEmbed(&pb.VoiceEmbedRequest{Audio: audio})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Embedding).To(Equal([]float32{0.1, 0.2, 0.3}))
			Expect(res.Model).To(Equal("ecapa"))

			req := up.last()
			Expect(req.Path).To(Equal("/v1/voice/embed"))
			Expect(req.JSON).To(HaveKeyWithValue("audio", b64("clip-bytes")))
		})
	})

	Describe("Stores", func() {
		It("StoresSet posts the store name, keys and values", func() {
			p := loadProxy(up, nil)
			up.script("/stores/set", scriptedResponse{Status: http.StatusOK})

			Expect(p.StoresSet(&pb.StoresSetOptions{
				Keys:   []*pb.StoresKey{{Floats: []float32{1, 2}}},
				Values: []*pb.StoresValue{{Bytes: []byte("v1")}},
			})).To(Succeed())

			req := up.last()
			Expect(req.Path).To(Equal("/stores/set"))
			Expect(req.JSON).To(HaveKeyWithValue("store", "remote-model"))
			Expect(req.JSON).To(HaveKeyWithValue("values", ConsistOf("v1")))
		})

		It("StoresDelete posts the store name and keys", func() {
			p := loadProxy(up, nil)
			up.script("/stores/delete", scriptedResponse{Status: http.StatusOK})

			Expect(p.StoresDelete(&pb.StoresDeleteOptions{Keys: []*pb.StoresKey{{Floats: []float32{1, 2}}}})).To(Succeed())

			req := up.last()
			Expect(req.Path).To(Equal("/stores/delete"))
			Expect(req.JSON).To(HaveKeyWithValue("store", "remote-model"))
		})

		It("StoresGet posts keys and maps the returned keys/values", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/stores/get", map[string]any{
				"keys": [][]float64{{1, 2}}, "values": []string{"v1"},
			})

			res, err := p.StoresGet(&pb.StoresGetOptions{Keys: []*pb.StoresKey{{Floats: []float32{1, 2}}}})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Keys).To(HaveLen(1))
			Expect(res.Keys[0].Floats).To(Equal([]float32{1, 2}))
			Expect(res.Values).To(HaveLen(1))
			Expect(res.Values[0].Bytes).To(Equal([]byte("v1")))
		})

		It("StoresFind posts the query key/topk and maps keys/values/similarities", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/stores/find", map[string]any{
				"keys": [][]float64{{1, 2}}, "values": []string{"v1"}, "similarities": []float64{0.9},
			})

			res, err := p.StoresFind(&pb.StoresFindOptions{Key: &pb.StoresKey{Floats: []float32{1, 2}}, TopK: 5})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Similarities).To(Equal([]float32{0.9}))

			req := up.last()
			Expect(req.Path).To(Equal("/stores/find"))
			Expect(req.JSON).To(HaveKeyWithValue("topk", BeNumerically("==", 5)))
			Expect(req.JSON).To(HaveKeyWithValue("key", ConsistOf(BeNumerically("==", 1), BeNumerically("==", 2))))
		})
	})
})
