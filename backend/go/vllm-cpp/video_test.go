package main

import (
	"os"
	"path/filepath"
	"unsafe"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The video PODs carry the same contract as the text ones in vllmcpp_test.go:
// these are the C offsets of vllm.h on LP64, and a drift here is silent memory
// corruption rather than a compile error.
var _ = Describe("C ABI video struct mirrors", func() {
	It("cVideoModelParams matches vllm_video_model_params", func() {
		var p cVideoModelParams
		Expect(unsafe.Offsetof(p.DitPath)).To(Equal(uintptr(0)))
		Expect(unsafe.Offsetof(p.EncoderPath)).To(Equal(uintptr(8)))
		Expect(unsafe.Offsetof(p.TokenizerPath)).To(Equal(uintptr(16)))
		Expect(unsafe.Offsetof(p.VideoVaePath)).To(Equal(uintptr(24)))
		Expect(unsafe.Offsetof(p.VideoVaeConfigPath)).To(Equal(uintptr(32)))
		Expect(unsafe.Offsetof(p.AudioVaePath)).To(Equal(uintptr(40)))
		Expect(unsafe.Offsetof(p.AudioVaeConfigPath)).To(Equal(uintptr(48)))
		Expect(unsafe.Offsetof(p.PromptEmbedsPath)).To(Equal(uintptr(56)))
		Expect(unsafe.Offsetof(p.Partition)).To(Equal(uintptr(64)))
		Expect(unsafe.Offsetof(p.Device)).To(Equal(uintptr(72)))
		Expect(unsafe.Offsetof(p.DequantBf16)).To(Equal(uintptr(76)))
		Expect(unsafe.Offsetof(p.Fp4Resident)).To(Equal(uintptr(80)))
		Expect(unsafe.Offsetof(p.Family)).To(Equal(uintptr(88)))
		Expect(unsafe.Offsetof(p.ExtraKeys)).To(Equal(uintptr(96)))
		Expect(unsafe.Offsetof(p.ExtraValues)).To(Equal(uintptr(104)))
		Expect(unsafe.Offsetof(p.NExtras)).To(Equal(uintptr(112)))
		Expect(unsafe.Sizeof(p)).To(Equal(uintptr(120)))
	})

	It("cVideoParams matches vllm_video_params", func() {
		var p cVideoParams
		Expect(unsafe.Offsetof(p.Prompt)).To(Equal(uintptr(0)))
		Expect(unsafe.Offsetof(p.Width)).To(Equal(uintptr(8)))
		Expect(unsafe.Offsetof(p.Height)).To(Equal(uintptr(12)))
		Expect(unsafe.Offsetof(p.NumFrames)).To(Equal(uintptr(16)))
		Expect(unsafe.Offsetof(p.Steps)).To(Equal(uintptr(20)))
		Expect(unsafe.Offsetof(p.Seed)).To(Equal(uintptr(24)))
		Expect(unsafe.Offsetof(p.HasSeed)).To(Equal(uintptr(32)))
		Expect(unsafe.Offsetof(p.FirstFrame)).To(Equal(uintptr(40)))
		Expect(unsafe.Offsetof(p.LastFrame)).To(Equal(uintptr(48)))
		Expect(unsafe.Offsetof(p.RefImage)).To(Equal(uintptr(56)))
		Expect(unsafe.Offsetof(p.RefVideo)).To(Equal(uintptr(64)))
		Expect(unsafe.Offsetof(p.RefAudio)).To(Equal(uintptr(72)))
		Expect(unsafe.Offsetof(p.NoiseAug)).To(Equal(uintptr(80)))
		Expect(unsafe.Offsetof(p.OutputDir)).To(Equal(uintptr(88)))
		Expect(unsafe.Offsetof(p.ExtraKeys)).To(Equal(uintptr(96)))
		Expect(unsafe.Offsetof(p.ExtraValues)).To(Equal(uintptr(104)))
		Expect(unsafe.Offsetof(p.NExtras)).To(Equal(uintptr(112)))
		Expect(unsafe.Sizeof(p)).To(Equal(uintptr(120)))
	})

	It("cVideoResult matches vllm_video_result", func() {
		var r cVideoResult
		Expect(unsafe.Offsetof(r.FrameDir)).To(Equal(uintptr(0)))
		Expect(unsafe.Offsetof(r.AudioPath)).To(Equal(uintptr(8)))
		Expect(unsafe.Offsetof(r.FrameCount)).To(Equal(uintptr(16)))
		Expect(unsafe.Offsetof(r.Width)).To(Equal(uintptr(20)))
		Expect(unsafe.Offsetof(r.Height)).To(Equal(uintptr(24)))
		Expect(unsafe.Offsetof(r.Fps)).To(Equal(uintptr(28)))
		Expect(unsafe.Offsetof(r.SampleRate)).To(Equal(uintptr(32)))
		Expect(unsafe.Offsetof(r.MuxArgv)).To(Equal(uintptr(40)))
		Expect(unsafe.Offsetof(r.MuxArgc)).To(Equal(uintptr(48)))
		Expect(unsafe.Sizeof(r)).To(Equal(uintptr(56)))
	})

	It("cVideoMuxParams matches vllm_video_mux_params", func() {
		var p cVideoMuxParams
		Expect(unsafe.Offsetof(p.Frames)).To(Equal(uintptr(0)))
		Expect(unsafe.Offsetof(p.AudioPath)).To(Equal(uintptr(8)))
		Expect(unsafe.Offsetof(p.OutputPath)).To(Equal(uintptr(16)))
		Expect(unsafe.Offsetof(p.Fps)).To(Equal(uintptr(24)))
		Expect(unsafe.Offsetof(p.Crf)).To(Equal(uintptr(28)))
		Expect(unsafe.Sizeof(p)).To(Equal(uintptr(32)))
	})
})

var _ = Describe("video load options", func() {
	It("stays disengaged for a plain text config", func() {
		lo := parseOptions(&pb.ModelOptions{Options: []string{"max_num_seqs:16"}})
		Expect(lo.video.engaged()).To(BeFalse())
	})

	It("reads the H3 checkpoint set from the options list", func() {
		lo := parseOptions(&pb.ModelOptions{Options: []string{
			"video_encoder:qwen3vl-32B-MiniMax-H3-Q4_K_M.gguf",
			"video_tokenizer:tokenizer.json",
			"video_vae:vae/diffusion_pytorch_model.safetensors",
			"audio_vae:audio_vae/model.safetensors",
			"video_partition:fl2va",
			"video_device:cuda",
			"video_dequant_bf16:true",
			"video_width:1344",
			"video_height:768",
			"video_num_frames:124",
			"video_steps:50",
		}})
		Expect(lo.video.engaged()).To(BeTrue())
		Expect(lo.video.encoderPath).To(Equal("qwen3vl-32B-MiniMax-H3-Q4_K_M.gguf"))
		Expect(lo.video.tokenizerPath).To(Equal("tokenizer.json"))
		Expect(lo.video.videoVaePath).To(Equal("vae/diffusion_pytorch_model.safetensors"))
		Expect(lo.video.audioVaePath).To(Equal("audio_vae/model.safetensors"))
		Expect(lo.video.partition).To(Equal(partitionFL2VA))
		Expect(lo.video.device).To(Equal(videoDeviceCUDA))
		Expect(lo.video.deviceSet).To(BeTrue())
		Expect(lo.video.dequantBf16).To(Equal(int32(1)))
		Expect(lo.video.width).To(Equal(int32(1344)))
		Expect(lo.video.height).To(Equal(int32(768)))
		Expect(lo.video.numFrames).To(Equal(int32(124)))
		Expect(lo.video.steps).To(Equal(int32(50)))
	})

	It("reads the same keys from engine_args", func() {
		lo := parseOptions(&pb.ModelOptions{
			EngineArgs: `{"video_vae":"vae/v.safetensors","audio_vae":"a.safetensors","video_num_frames":124,"video_dequant_bf16":true}`,
		})
		Expect(lo.video.engaged()).To(BeTrue())
		Expect(lo.video.videoVaePath).To(Equal("vae/v.safetensors"))
		Expect(lo.video.audioVaePath).To(Equal("a.safetensors"))
		Expect(lo.video.numFrames).To(Equal(int32(124)))
		Expect(lo.video.dequantBf16).To(Equal(int32(1)))
	})

	It("ignores an unknown video_device rather than guessing", func() {
		lo := parseOptions(&pb.ModelOptions{Options: []string{"video_vae:v", "video_device:tpu"}})
		Expect(lo.video.deviceSet).To(BeFalse())
		Expect(lo.video.device).To(Equal(videoDeviceCPU))
	})
})

var _ = Describe("per-request params", func() {
	It("maps the accepted keys", func() {
		extra, err := parseVideoRequestParams(map[string]string{
			"noise_aug": "0.5", "ref_image": "/tmp/ref.ppm", "crf": "20",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(extra.noiseAug).To(BeNumerically("~", 0.5, 1e-6))
		Expect(extra.refImage).To(Equal("/tmp/ref.ppm"))
		Expect(extra.crf).To(Equal(int32(20)))
	})

	It("refuses an unknown key instead of dropping it", func() {
		_, err := parseVideoRequestParams(map[string]string{"resolution": "480p"})
		Expect(err).To(MatchError(ContainSubstring("unknown params key")))
	})

	It("refuses a non-numeric noise_aug", func() {
		_, err := parseVideoRequestParams(map[string]string{"noise_aug": "high"})
		Expect(err).To(HaveOccurred())
	})
})

// The partition guard is the correctness rule this backend exists to enforce:
// the FL2VA DiT serves t2va and fl2va, and handing it reference conditioning
// renders a broken lattice over the frame after a multi-hour generation rather
// than failing.
var _ = Describe("partition conditioning guard", func() {
	It("accepts a plain t2va request on fl2va", func() {
		Expect(checkPartitionConditioning(partitionFL2VA,
			&pb.GenerateVideoRequest{Prompt: "a llama"}, videoExtraParams{})).To(Succeed())
	})

	It("accepts fl2va keyframes on fl2va", func() {
		Expect(checkPartitionConditioning(partitionFL2VA,
			&pb.GenerateVideoRequest{StartImage: "/tmp/a.png"}, videoExtraParams{})).To(Succeed())
	})

	It("refuses a reference image on fl2va", func() {
		err := checkPartitionConditioning(partitionFL2VA,
			&pb.GenerateVideoRequest{}, videoExtraParams{refImage: "/tmp/ref.ppm"})
		Expect(err).To(MatchError(ContainSubstring("ref2va")))
	})

	It("refuses reference audio on fl2va", func() {
		err := checkPartitionConditioning(partitionFL2VA,
			&pb.GenerateVideoRequest{Audio: "/tmp/voice.wav"}, videoExtraParams{})
		Expect(err).To(HaveOccurred())
	})

	It("refuses fl2va keyframes on ref2va", func() {
		err := checkPartitionConditioning(partitionRef2VA,
			&pb.GenerateVideoRequest{StartImage: "/tmp/a.png"}, videoExtraParams{})
		Expect(err).To(HaveOccurred())
	})

	It("refuses keyframes and references together on either partition", func() {
		err := checkPartitionConditioning(partitionRef2VA,
			&pb.GenerateVideoRequest{StartImage: "/tmp/a.png"}, videoExtraParams{refVideo: "/tmp/clip"})
		Expect(err).To(MatchError(ContainSubstring("exclusive")))
	})
})

var _ = Describe("H3 geometry", func() {
	It("keeps an explicitly requested canvas", func() {
		w, h, err := resolveCanvas(1280, 720)
		Expect(err).ToNot(HaveOccurred())
		Expect(w).To(Equal(int32(1280)))
		Expect(h).To(Equal(int32(720)))
	})

	It("falls back to the shipped 1344x768 canvas", func() {
		w, h, err := resolveCanvas(0, 0)
		Expect(err).ToNot(HaveOccurred())
		Expect(w).To(Equal(int32(1344)))
		Expect(h).To(Equal(int32(768)))
	})

	It("derives a landscape canvas from a keyframe's aspect", func() {
		path := writePPM(1920, 1080)
		w, h, err := resolveCanvas(0, 0, path)
		Expect(err).ToNot(HaveOccurred())
		Expect(h).To(Equal(int32(768)))
		// 768 * 16/9 = 1365.33; /32 = 42.67, round-half-to-even to 43, x32.
		Expect(w).To(Equal(int32(1376)))
	})

	It("derives a portrait canvas from a keyframe's aspect", func() {
		path := writePPM(1080, 1920)
		w, h, err := resolveCanvas(0, 0, path)
		Expect(err).ToNot(HaveOccurred())
		Expect(w).To(Equal(int32(768)))
		Expect(h).To(Equal(int32(1376)))
	})

	It("truncates onto the 32 grid the way the engine does", func() {
		Expect(truncateToGrid(1000)).To(Equal(int32(992)))
		Expect(truncateToGrid(768)).To(Equal(int32(768)))
	})

	It("reports the 17n+5 frame grid", func() {
		Expect(alignFrameCount(124)).To(Equal(int32(124)))
		Expect(alignFrameCount(120)).To(Equal(int32(124)))
		Expect(alignFrameCount(100)).To(Equal(int32(107)))
	})
})

var _ = Describe("keyframe staging", func() {
	It("parses a binary PPM header, comments included", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "commented.ppm")
		Expect(os.WriteFile(path, []byte("P6\n# made by a test\n64 32\n255\n"), 0o600)).To(Succeed())
		w, h, err := ppmDimensions(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(w).To(Equal(int32(64)))
		Expect(h).To(Equal(int32(32)))
	})

	It("refuses an ASCII PPM (P3): the engine reads P6 only", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "ascii.ppm")
		Expect(os.WriteFile(path, []byte("P3\n64 32\n255\n"), 0o600)).To(Succeed())
		_, _, err := ppmDimensions(path)
		Expect(err).To(HaveOccurred())
	})

	It("passes a P6 already at the canvas straight through, without ffmpeg", func() {
		path := writePPM(64, 32)
		out, err := stageKeyframe("", path, 64, 32, GinkgoT().TempDir(), "first")
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(Equal(path))
	})

	It("is a no-op for an absent keyframe", func() {
		out, err := stageKeyframe("", "", 64, 32, GinkgoT().TempDir(), "first")
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(BeEmpty())
	})
})

var _ = Describe("GenerateVideo preconditions", func() {
	It("refuses when the model is not a video engine", func() {
		v := &VllmCpp{}
		Expect(v.GenerateVideo(&pb.GenerateVideoRequest{Prompt: "x", Dst: "/tmp/o.mp4"})).
			To(MatchError(ContainSubstring("not a MiniMax-H3 video engine")))
	})
})

// writePPM writes a valid P6 header of the given geometry. Only the header is
// read by anything under test, so the pixel payload is left off.
func writePPM(width, height int) string {
	dir := GinkgoT().TempDir()
	path := filepath.Join(dir, "frame.ppm")
	header := []byte("P6\n" + itoa(width) + " " + itoa(height) + "\n255\n")
	Expect(os.WriteFile(path, header, 0o600)).To(Succeed())
	return path
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	digits := ""
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	return digits
}
