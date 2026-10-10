package main

import (
	"os"
	"path/filepath"
	"testing"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestStableDiffusionGGML(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "stablediffusion-ggml backend test suite")
}

var _ = DescribeTable("parseVAETiling enablement",
	func(options []string, want bool) {
		Expect(parseVAETiling(options).enabled).To(Equal(want))
	},
	Entry("explicit true", []string{"vae_tiling:true"}, true),
	Entry("one is truthy", []string{"vae_tiling:1"}, true),
	// "diffusion_model" is already a bare flag in this option list, so accept
	// the same shape rather than making vae_tiling the one option that demands
	// a value.
	Entry("bare flag", []string{"vae_tiling"}, true),
	Entry("explicit false", []string{"vae_tiling:false"}, false),
	Entry("anything else is false", []string{"vae_tiling:maybe"}, false),
	// Tiling trades a little quality at the tile seams for a much smaller
	// compute buffer, so it must not switch itself on for the many models that
	// never needed it.
	Entry("absent leaves it off", []string{"diffusion_model", "sampler:euler"}, false),
	Entry("nil options", []string(nil), false),
)

var _ = DescribeTable("parseVAETiling tile size",
	func(options []string, wantSet bool, wantX, wantY int) {
		got := parseVAETiling(options)
		Expect(got.hasTileSize).To(Equal(wantSet))
		if wantSet {
			Expect(got.tileSizeX).To(Equal(wantX))
			Expect(got.tileSizeY).To(Equal(wantY))
		}
	},
	Entry("one number is a square tile", []string{"vae_tile_size:512"}, true, 512, 512),
	Entry("rectangular", []string{"vae_tile_size:512x384"}, true, 512, 384),
	// Absent must stay absent: the caller only invokes the upstream setter when
	// a size was given, so this is what preserves the library's own default
	// instead of pushing a zero.
	Entry("absent", []string{"vae_tiling:true"}, false, 0, 0),
	// A typo must not silently become a zero tile size and break a model that
	// would otherwise have worked on the default.
	Entry("unparseable", []string{"vae_tile_size:banana"}, false, 0, 0),
	Entry("zero rejected", []string{"vae_tile_size:0"}, false, 0, 0),
	Entry("negative rejected", []string{"vae_tile_size:-8"}, false, 0, 0),
	Entry("half unparseable", []string{"vae_tile_size:512xbanana"}, false, 0, 0),
)

var _ = DescribeTable("parseVAETiling target overlap",
	func(options []string, wantSet bool, want float32) {
		got := parseVAETiling(options)
		Expect(got.hasOverlap).To(Equal(wantSet))
		if wantSet {
			Expect(got.targetOverlap).To(Equal(want))
		}
	},
	Entry("fraction", []string{"vae_tile_overlap:0.25"}, true, float32(0.25)),
	Entry("zero is a legitimate overlap", []string{"vae_tile_overlap:0"}, true, float32(0)),
	Entry("absent", []string{"vae_tiling:true"}, false, float32(0)),
	Entry("unparseable", []string{"vae_tile_overlap:banana"}, false, float32(0)),
	Entry("negative rejected", []string{"vae_tile_overlap:-0.5"}, false, float32(0)),
)

// fakeSDLib swaps the purego bindings for recorders so the wiring between the
// parsed options and the upstream tiling setters can be exercised without the
// shared library. The bindings are package-level vars, which is the only seam
// available here; every one the code under test touches must be set or the
// call panics on a nil func.
type fakeSDLib struct {
	tilingEnabled bool
	tileSizeCalls int
	tileSizeX     int
	tileSizeY     int
	overlapCalls  int
	targetOverlap float32
}

// install points the bindings at the recorder and restores them afterwards, so
// one spec cannot leak fakes into the next.
func (f *fakeSDLib) install() {
	savedImgGenParamsNew := ImgGenParamsNew
	savedImgGenParamsSetPrompts := ImgGenParamsSetPrompts
	savedImgGenParamsSetDimensions := ImgGenParamsSetDimensions
	savedImgGenParamsSetSeed := ImgGenParamsSetSeed
	savedImgGenParamsGetVaeTilingParams := ImgGenParamsGetVaeTilingParams
	savedTilingParamsSetEnabled := TilingParamsSetEnabled
	savedTilingParamsSetTileSizes := TilingParamsSetTileSizes
	savedTilingParamsSetTargetOverlap := TilingParamsSetTargetOverlap
	savedGenImage := GenImage
	savedLoadModel := LoadModel

	DeferCleanup(func() {
		ImgGenParamsNew = savedImgGenParamsNew
		ImgGenParamsSetPrompts = savedImgGenParamsSetPrompts
		ImgGenParamsSetDimensions = savedImgGenParamsSetDimensions
		ImgGenParamsSetSeed = savedImgGenParamsSetSeed
		ImgGenParamsGetVaeTilingParams = savedImgGenParamsGetVaeTilingParams
		TilingParamsSetEnabled = savedTilingParamsSetEnabled
		TilingParamsSetTileSizes = savedTilingParamsSetTileSizes
		TilingParamsSetTargetOverlap = savedTilingParamsSetTargetOverlap
		GenImage = savedGenImage
		LoadModel = savedLoadModel
	})

	ImgGenParamsNew = func() uintptr { return 1 }
	ImgGenParamsSetPrompts = func(uintptr, string, string) {}
	ImgGenParamsSetDimensions = func(uintptr, int, int) {}
	ImgGenParamsSetSeed = func(uintptr, int64) {}
	ImgGenParamsGetVaeTilingParams = func(uintptr) uintptr { return 2 }
	TilingParamsSetEnabled = func(_ uintptr, enabled bool) { f.tilingEnabled = enabled }
	TilingParamsSetTileSizes = func(_ uintptr, x, y int) {
		f.tileSizeCalls++
		f.tileSizeX, f.tileSizeY = x, y
	}
	TilingParamsSetTargetOverlap = func(_ uintptr, o float32) {
		f.overlapCalls++
		f.targetOverlap = o
	}
	GenImage = func(uintptr, int, string, float32, string, float32, string, []uintptr, int) int { return 0 }
	LoadModel = func(string, string, []uintptr, int32, int) int { return 0 }
}

var _ = Describe("GenerateImage VAE tiling", func() {
	var fake *fakeSDLib

	BeforeEach(func() {
		fake = &fakeSDLib{}
		fake.install()
	})

	// generate drives the real Load and GenerateImage so the options travel the
	// path they travel in production, with only the C boundary faked.
	generate := func(options []string) {
		sd := &SDGGML{}
		Expect(sd.Load(&pb.ModelOptions{Options: options})).To(Succeed())
		Expect(sd.GenerateImage(&pb.GenerateImageRequest{Width: 1024, Height: 1024})).To(Succeed())
	}

	It("enables tiling when the model asks for it", func() {
		generate([]string{"vae_tiling:true"})

		Expect(fake.tilingEnabled).To(BeTrue())
	})

	// The pre-existing behaviour: every model that never asked for tiling must
	// still get it switched off.
	It("leaves tiling off by default", func() {
		generate([]string{"sampler:euler"})

		Expect(fake.tilingEnabled).To(BeFalse())
	})

	It("applies a configured tile size and overlap", func() {
		generate([]string{"vae_tiling:true", "vae_tile_size:512x384", "vae_tile_overlap:0.25"})

		Expect(fake.tileSizeCalls).To(Equal(1))
		Expect(fake.tileSizeX).To(Equal(512))
		Expect(fake.tileSizeY).To(Equal(384))
		Expect(fake.overlapCalls).To(Equal(1))
		Expect(fake.targetOverlap).To(Equal(float32(0.25)))
	})

	// Not calling the setters is what preserves the library's own defaults, so
	// unconfigured values must leave them untouched rather than send a zero.
	It("leaves unset tiling parameters alone", func() {
		generate([]string{"vae_tiling:true"})

		Expect(fake.tileSizeCalls).To(BeZero())
		Expect(fake.overlapCalls).To(BeZero())
	})
})

var _ = Describe("ESRGAN lifecycle", func() {
	var sd *SDGGML
	var root, modelPath string
	var creates, destroys, runs, loads int
	var scale, code int32
	var fail bool
	var entered, release chan struct{}
	BeforeEach(func() {
		sd = &SDGGML{}
		root = GinkgoT().TempDir()
		var err error
		root, err = filepath.EvalSymlinks(root)
		Expect(err).NotTo(HaveOccurred())
		modelPath = filepath.Join(root, "esrgan.gguf")
		Expect(os.WriteFile(modelPath, []byte("model"), 0600)).To(Succeed())
		creates, destroys, runs, loads = 0, 0, 0, 0
		scale, code, fail = 4, 0, false
		entered, release = nil, nil
		oldCreate, oldScale, oldRun, oldDestroy, oldLoad := UpscalerCreate, UpscalerScale, UpscalerRun, UpscalerDestroy, LoadModel
		DeferCleanup(func() {
			UpscalerCreate, UpscalerScale, UpscalerRun, UpscalerDestroy, LoadModel = oldCreate, oldScale, oldRun, oldDestroy, oldLoad
		})
		UpscalerCreate = func(path string, direct bool, threads, tile int32, backend, params string) uintptr {
			creates++
			Expect(path).To(Equal(modelPath))
			Expect(tile).To(Equal(int32(128)))
			if fail {
				return 0
			}
			return 42
		}
		UpscalerScale = func(uintptr) int32 { return scale }
		UpscalerRun = func(uintptr, string, string, int32) int32 {
			runs++
			if entered != nil {
				close(entered)
				<-release
			}
			return code
		}
		UpscalerDestroy = func(h uintptr) { Expect(h).To(Equal(uintptr(42))); destroys++ }
		LoadModel = func(string, string, []uintptr, int32, int) int { loads++; return 0 }
	})
	load := func(extra ...string) error {
		return sd.Load(&pb.ModelOptions{ModelPath: root, ModelFile: "esrgan.gguf", Options: append([]string{"upscale_scale:4", "upscale_tile_size:128"}, extra...)})
	}
	run := func(s int32) error {
		return sd.UpscaleImage(&pb.UpscaleImageRequest{Src: "in.png", Dst: "out.png", Scale: s})
	}
	It("skips diffusion, creates lazily, reuses and frees once", func() {
		Expect(load()).To(Succeed())
		Expect(loads).To(BeZero())
		Expect(creates).To(BeZero())
		Expect(run(4)).To(Succeed())
		Expect(run(4)).To(Succeed())
		Expect(creates).To(Equal(1))
		Expect(runs).To(Equal(2))
		Expect(sd.Free()).To(Succeed())
		Expect(sd.Free()).To(Succeed())
		Expect(destroys).To(Equal(1))
		Expect(run(4)).NotTo(Succeed())
	})
	It("rejects invalid configuration and mixed usecases", func() {
		Expect(load("upscale_scale:0")).NotTo(Succeed())
		Expect(load("known_usecases:image,upscale")).NotTo(Succeed())
		Expect(load("upscale_tile_size:-1")).NotTo(Succeed())
	})
	It("rejects detected scale mismatch without caching", func() {
		Expect(load()).To(Succeed())
		scale = 2
		Expect(run(4)).To(MatchError(ContainSubstring("configured")))
		Expect(destroys).To(Equal(1))
		scale = 4
		Expect(run(4)).To(Succeed())
		Expect(creates).To(Equal(2))
	})
	It("rejects requested scale mismatch", func() {
		Expect(load()).To(Succeed())
		Expect(run(2)).To(MatchError(ContainSubstring("requested")))
		Expect(runs).To(BeZero())
	})
	It("does not cache failed initialization", func() {
		Expect(load()).To(Succeed())
		fail = true
		Expect(run(4)).NotTo(Succeed())
		fail = false
		Expect(run(4)).To(Succeed())
		Expect(creates).To(Equal(2))
	})
	It("rejects undetected scales", func() {
		Expect(load()).To(Succeed())
		scale = 0
		Expect(run(4)).NotTo(Succeed())
		Expect(destroys).To(Equal(1))
	})
	It("reports native failures", func() {
		Expect(load()).To(Succeed())
		for _, c := range []int32{1, 2, 3, 4, 5} {
			code = c
			Expect(run(4)).NotTo(Succeed())
		}
	})
	It("waits for inference before Free", func() {
		Expect(load()).To(Succeed())
		Expect(run(4)).To(Succeed())
		entered, release = make(chan struct{}), make(chan struct{})
		done := make(chan error, 1)
		freed := make(chan error, 1)
		go func() { done <- run(4) }()
		<-entered
		go func() { freed <- sd.Free() }()
		Consistently(freed, "50ms").ShouldNot(Receive())
		close(release)
		Eventually(done).Should(Receive(BeNil()))
		Eventually(freed).Should(Receive(BeNil()))
		Expect(destroys).To(Equal(1))
	})
})

func TestUpscaleSettings(t *testing.T) {
	root := upscaleTestRoot(t)
	opts := &pb.ModelOptions{ModelPath: root, ModelFile: filepath.Join(root, "model.gguf"), Threads: 7, Options: []string{
		"known_usecases:upscale", "upscale_scale:4", "upscale_tile_size:256",
		"upscale_direct:true", "backend:Vulkan0", "params_backend:CPU",
	}}
	got, err := parseUpscaleSettings(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !got.enabled || got.path != opts.ModelFile || got.scale != 4 || got.tile != 256 || got.threads != 7 || !got.direct || got.backend != "Vulkan0" || got.paramsBackend != "CPU" {
		t.Fatalf("wrong settings: %+v", got)
	}
	for _, options := range [][]string{
		{"known_usecases:upscale"}, {"upscale_scale:banana"}, {"upscale_scale:2147483648"},
		{"upscale_scale:4", "diffusion_model"}, {"upscale_scale:4", "upscale_direct:maybe"},
	} {
		opts.Options = options
		if _, err := parseUpscaleSettings(opts); err == nil {
			t.Fatalf("accepted invalid options %v", options)
		}
	}
}

func TestUpscaleSettingsPathContainment(t *testing.T) {
	root := upscaleTestRoot(t)
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested/model.gguf"), []byte("model"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, modelFile, want string
		wantErr               bool
	}{
		{"safe nested relative path", "nested/model.gguf", filepath.Join(root, "nested/model.gguf"), false},
		{"relative traversal", "../outside.gguf", "", true},
		{"allowed in-root absolute path", filepath.Join(root, "model.gguf"), filepath.Join(root, "model.gguf"), false},
		{"outside absolute path", "/other/model.gguf", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseUpscaleSettings(&pb.ModelOptions{ModelPath: root, ModelFile: tc.modelFile, Options: []string{"upscale_scale:4"}})
			if tc.wantErr {
				if err == nil {
					t.Fatal("accepted path outside model path")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.path != tc.want {
				t.Fatalf("path = %q, want %q", got.path, tc.want)
			}
		})
	}
}

func TestUpscaleSettingsRejectsDuplicateSettings(t *testing.T) {
	root := upscaleTestRoot(t)
	for _, setting := range []string{"upscale_scale:4", "upscale_tile_size:128", "upscale_direct:true", "backend:Vulkan", "params_backend:CPU"} {
		t.Run(setting, func(t *testing.T) {
			_, err := parseUpscaleSettings(&pb.ModelOptions{ModelPath: root, ModelFile: "model.gguf", Options: []string{"upscale_scale:4", setting, setting}})
			if err == nil {
				t.Fatalf("accepted duplicate %s", setting)
			}
		})
	}
	// known_usecases is aggregate metadata, not a last-wins setting; repeated
	// entries remain valid so diffusion callers can forward their options.
	if _, err := parseUpscaleSettings(&pb.ModelOptions{ModelPath: root, ModelFile: "model.gguf", Options: []string{"known_usecases:upscale", "known_usecases:upscale", "upscale_scale:4"}}); err != nil {
		t.Fatalf("rejected duplicate known_usecases: %v", err)
	}
}

func upscaleTestRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "model.gguf"), []byte("model"), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestUpscaleSettingsRealPathContainment(t *testing.T) {
	root := upscaleTestRoot(t)
	outside := upscaleTestRoot(t)
	for _, tc := range []struct {
		name, target string
		wantErr      bool
	}{
		{"in-root symlink", filepath.Join(root, "model.gguf"), false},
		{"outside symlink", filepath.Join(outside, "model.gguf"), true},
		{"dangling symlink", filepath.Join(root, "missing.gguf"), true},
		{"outside directory symlink", outside, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link := filepath.Join(root, tc.name)
			if err := os.Symlink(tc.target, link); err != nil {
				t.Skipf("symlink creation unsupported: %v", err)
			}
			file := link
			if tc.name == "outside directory symlink" {
				file = filepath.Join(link, "model.gguf")
			}
			got, err := parseUpscaleSettings(&pb.ModelOptions{ModelPath: root, ModelFile: file, Options: []string{"upscale_scale:4"}})
			if tc.wantErr {
				if err == nil {
					t.Fatal("accepted unsafe symlink")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.path != tc.target {
				t.Fatalf("cached path %q, want real path %q", got.path, tc.target)
			}
		})
	}
	t.Run("symlinked trusted root", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "root")
		if err := os.Symlink(root, link); err != nil {
			t.Skipf("symlink creation unsupported: %v", err)
		}
		got, err := parseUpscaleSettings(&pb.ModelOptions{ModelPath: link, ModelFile: "model.gguf", Options: []string{"upscale_scale:4"}})
		if err != nil {
			t.Fatal(err)
		}
		if got.path != filepath.Join(root, "model.gguf") {
			t.Fatalf("not resolved: %q", got.path)
		}
	})
	for _, file := range []string{"missing.gguf", "."} {
		t.Run(file, func(t *testing.T) {
			if _, err := parseUpscaleSettings(&pb.ModelOptions{ModelPath: root, ModelFile: file, Options: []string{"upscale_scale:4"}}); err == nil {
				t.Fatal("accepted missing or non-file model")
			}
		})
	}
}
