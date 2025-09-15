package main

import (
	"flag"

	"github.com/ebitengine/purego"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

type LibFuncs struct {
	FuncPtr any
	Name    string
}

func main() {
	gosd, err := purego.Dlopen("./libgosd.so", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	libFuncs := []LibFuncs{
		{&LoadModel, "load_model"},
		{&GenImage, "gen_image"},
		{&TilingParamsSetEnabled, "sd_tiling_params_set_enabled"},
		{&TilingParamsSetTileSizes, "sd_tiling_params_set_tile_sizes"},
		{&TilingParamsSetRelSizes, "sd_tiling_params_set_rel_sizes"},
		{&TilingParamsSetTargetOverlap, "sd_tiling_params_set_target_overlap"},

		{&ImgGenParamsNew, "sd_img_gen_params_new"},
		{&ImgGenParamsSetPrompts, "sd_img_gen_params_set_prompts"},
		{&ImgGenParamsSetDimensions, "sd_img_gen_params_set_dimensions"},
		{&ImgGenParamsSetSeed, "sd_img_gen_params_set_seed"},
		{&ImgGenParamsGetVaeTilingParams, "sd_img_gen_params_get_vae_tiling_params"},
	}

	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, gosd, lf.Name)
	}

	flag.Parse()

	if err := grpc.StartServer(*addr, &SDGGML{}); err != nil {
		panic(err)
	}
}
