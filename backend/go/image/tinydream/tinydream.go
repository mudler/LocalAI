package main

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"github.com/go-skynet/LocalAI/pkg/grpc/base"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"github.com/go-skynet/LocalAI/pkg/tinydream"
)

type Image struct {
	base.SingleThread
	tinydream *tinydream.TinyDream
}

func (image *Image) Load(opts *pb.ModelOptions) error {
	var err error
	// Note: the Model here is a path to a directory containing the model files
	image.tinydream, err = tinydream.New(opts.ModelFile)
	return err
}

func (image *Image) GenerateImage(opts *pb.GenerateImageRequest) error {
	return image.tinydream.GenerateImage(
		int(opts.Height),
		int(opts.Width),
		int(opts.Step),
		int(opts.Seed),
		opts.PositivePrompt,
		opts.NegativePrompt,
		opts.Dst)
}
