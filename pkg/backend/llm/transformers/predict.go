package transformers

import (
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
	transformers "github.com/go-skynet/go-ggml-transformers.cpp"
)

func buildPredictOptions(opts *pb.PredictOptions) []transformers.PredictOption {
	predictOptions := []transformers.PredictOption{
		transformers.SetTemperature(float64(opts.Temperature)),
		transformers.SetTopP(float64(opts.TopP)),
		transformers.SetTopK(int(opts.TopK)),
		transformers.SetTokens(int(opts.Tokens)),
		transformers.SetThreads(int(opts.Threads)),
	}

	if opts.Batch != 0 {
		predictOptions = append(predictOptions, transformers.SetBatch(int(opts.Batch)))
	}

	if opts.Seed != 0 {
		predictOptions = append(predictOptions, transformers.SetSeed(int(opts.Seed)))
	}

	return predictOptions
}
