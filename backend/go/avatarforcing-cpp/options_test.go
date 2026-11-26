package main

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAvatarForcingCpp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "avatarforcing-cpp Backend Suite")
}

func decodeJSON(s string) map[string]any {
	GinkgoHelper()
	m := map[string]any{}
	Expect(json.Unmarshal([]byte(s), &m)).To(Succeed())
	return m
}

var _ = Describe("parseModelOptions", func() {
	It("maps the context and request options to the load JSON", func() {
		co, err := parseModelOptions([]string{
			"backend:cuda:1", "threads:8", "decode_batch:0", "overlap:true",
			"nfe:4", "cfg_scale:1.5", "seed:7", "audio_mode:raw", "max_seconds:0",
		}, 0)
		Expect(err).ToNot(HaveOccurred())
		Expect(co.backend).To(Equal("cuda:1"))
		Expect(decodeJSON(co.loadJSON(co.backend))).To(Equal(map[string]any{
			"backend": "cuda:1", "threads": 8.0, "decode_batch": 0.0, "overlap": true,
			"nfe": 4.0, "cfg_scale": 1.5, "seed": 7.0, "audio_mode": "raw", "max_seconds": 0.0,
		}))
	})

	It("leaves out what is not set and uses the Threads field of the model config", func() {
		co, err := parseModelOptions(nil, 6)
		Expect(err).ToNot(HaveOccurred())
		Expect(co.backend).To(BeEmpty())
		Expect(decodeJSON(co.loadJSON("cpu"))).To(Equal(map[string]any{"backend": "cpu", "threads": 6.0}))
	})

	It("lets the threads option win over the Threads field", func() {
		co, err := parseModelOptions([]string{"threads:2"}, 6)
		Expect(err).ToNot(HaveOccurred())
		Expect(co.threads).To(Equal(2))
	})

	It("treats backend auto as no backend", func() {
		co, err := parseModelOptions([]string{"backend:auto"}, 0)
		Expect(err).ToNot(HaveOccurred())
		Expect(co.backend).To(BeEmpty())
	})

	DescribeTable("rejects bad options",
		func(opt, want string) {
			_, err := parseModelOptions([]string{opt}, 0)
			Expect(err).To(MatchError(ContainSubstring(want)))
		},
		Entry("unknown key", "steps:4", `unknown option "steps"`),
		Entry("no value separator", "nfe", "key:value"),
		Entry("unsupported backend", "backend:vulkan", "only the CPU and CUDA"),
		Entry("bad device index", "backend:cuda:x", "device index"),
		Entry("nfe too small", "nfe:2", "nfe must be 3 or more"),
		Entry("negative threads", "threads:-1", "threads must be 0 or more"),
		Entry("bad audio mode", "audio_mode:loud", "normalized or raw"),
		Entry("bad overlap", "overlap:maybe", "true or false"),
		Entry("bad cfg_scale", "cfg_scale:high", "cfg_scale must be a number"),
		Entry("empty ffmpeg", "ffmpeg:", "ffmpeg is empty"),
	)
})

var _ = Describe("requestFromProto", func() {
	It("gives no JSON for a request that sets nothing", func() {
		r, err := requestFromProto(&pb.GenerateVideoRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(r.json()).To(BeEmpty())
	})

	It("maps step to nfe = step + 1, and cfg_scale and seed", func() {
		r, err := requestFromProto(&pb.GenerateVideoRequest{Step: 5, CfgScale: 2.5, Seed: 11})
		Expect(err).ToNot(HaveOccurred())
		Expect(decodeJSON(r.json())).To(Equal(map[string]any{"nfe": 6.0, "cfg_scale": 2.5, "seed": 11.0}))
	})

	It("lets params win over the proto fields", func() {
		r, err := requestFromProto(&pb.GenerateVideoRequest{
			Step:   5,
			Params: map[string]string{"nfe": " 4 ", "audio_mode": "raw", "max_seconds": "10"},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(decodeJSON(r.json())).To(Equal(map[string]any{"nfe": 4.0, "audio_mode": "raw", "max_seconds": 10.0}))
	})

	It("rejects a step below two", func() {
		_, err := requestFromProto(&pb.GenerateVideoRequest{Step: 1})
		Expect(err).To(MatchError(ContainSubstring("step must be 2 or more")))
	})

	It("rejects a context option in params", func() {
		_, err := requestFromProto(&pb.GenerateVideoRequest{Params: map[string]string{"threads": "4"}})
		Expect(err).To(MatchError(ContainSubstring(`unknown params key "threads"`)))
	})

	It("rejects a bad params value", func() {
		_, err := requestFromProto(&pb.GenerateVideoRequest{Params: map[string]string{"seed": "x"}})
		Expect(err).To(MatchError(ContainSubstring("seed must be an integer")))
	})
})

var _ = Describe("checkVideoRequest", func() {
	It("accepts a portrait, audio and an output path", func() {
		Expect(checkVideoRequest(&pb.GenerateVideoRequest{StartImage: "p", Audio: "a", Dst: "o.mp4"})).To(Succeed())
	})

	It("names every missing input", func() {
		err := checkVideoRequest(&pb.GenerateVideoRequest{Dst: "o.mp4"})
		Expect(err).To(MatchError(ContainSubstring("a portrait (start_image) and speech audio (audio)")))
	})

	It("names a missing audio", func() {
		err := checkVideoRequest(&pb.GenerateVideoRequest{StartImage: "p", Dst: "o.mp4"})
		Expect(err).To(MatchError(ContainSubstring("needs speech audio (audio)")))
	})

	It("rejects an end image and references", func() {
		err := checkVideoRequest(&pb.GenerateVideoRequest{StartImage: "p", Audio: "a", Dst: "o.mp4", EndImage: "e"})
		Expect(err).To(MatchError(ContainSubstring("not supported")))
		err = checkVideoRequest(&pb.GenerateVideoRequest{StartImage: "p", Audio: "a", Dst: "o.mp4", RefImages: []string{"r"}})
		Expect(err).To(MatchError(ContainSubstring("not supported")))
	})
})

var _ = Describe("resolveFfmpeg", func() {
	It("names ffmpeg when it is missing", func() {
		_, err := resolveFfmpeg("ffmpeg-that-does-not-exist")
		Expect(err).To(MatchError(ContainSubstring(`ffmpeg "ffmpeg-that-does-not-exist" not found`)))
	})
})

var _ = Describe("input preparation", func() {
	var ffmpeg, work string

	BeforeEach(func() {
		p, err := exec.LookPath("ffmpeg")
		if err != nil {
			Skip("ffmpeg is not on PATH")
		}
		ffmpeg = p
		work = GinkgoT().TempDir()
	})

	writePNG := func(name string, w, h int) string {
		GinkgoHelper()
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
			}
		}
		path := filepath.Join(work, name)
		f, err := os.Create(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(png.Encode(f, img)).To(Succeed())
		Expect(f.Close()).To(Succeed())
		return path
	}

	It("uses a portrait at the model size as it is", func() {
		src := writePNG("in.png", 64, 64)
		out, err := preparePortrait(ffmpeg, src, 64, 64, work)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(Equal(src))
	})

	It("crops and scales any other portrait to the model size", func() {
		src := writePNG("in.png", 120, 80)
		out, err := preparePortrait(ffmpeg, src, 64, 64, work)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).ToNot(Equal(src))
		w, h, err := imageSize(out)
		Expect(err).ToNot(HaveOccurred())
		Expect([]int{w, h}).To(Equal([]int{64, 64}))
	})

	It("reports a portrait that is not an image", func() {
		src := filepath.Join(work, "bad")
		Expect(os.WriteFile(src, []byte("not an image"), 0o600)).To(Succeed())
		_, err := preparePortrait(ffmpeg, src, 64, 64, work)
		Expect(err).To(MatchError(ContainSubstring("reading the portrait")))
	})

	It("converts audio to a PCM WAV file", func() {
		src := filepath.Join(work, "tone.flac")
		Expect(runFfmpeg(ffmpeg, "-f", "lavfi", "-i", "sine=frequency=440:duration=0.5", src)).To(Succeed())
		out, err := prepareAudio(ffmpeg, src, work)
		Expect(err).ToNot(HaveOccurred())
		b, err := os.ReadFile(out)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(b[0:4])).To(Equal("RIFF"))
		Expect(string(b[8:12])).To(Equal("WAVE"))
	})

	It("reports audio that ffmpeg cannot read", func() {
		src := filepath.Join(work, "bad")
		Expect(os.WriteFile(src, []byte("not audio"), 0o600)).To(Succeed())
		_, err := prepareAudio(ffmpeg, src, work)
		Expect(err).To(MatchError(ContainSubstring("reading the audio")))
	})
})
