package main

import (
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
