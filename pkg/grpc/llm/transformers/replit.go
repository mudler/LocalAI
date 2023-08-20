package transformers

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"fmt"

	"github.com/go-skynet/LocalAI/pkg/grpc/base"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"github.com/rs/zerolog/log"

	transformers "github.com/go-skynet/go-ggml-transformers.cpp"
)

type Replit struct {
	base.BaseSingleton

	replit *transformers.Replit
}

func (llm *Replit) Load(opts *pb.ModelOptions) error {
	if llm.Base.State != pb.StatusResponse_UNINITIALIZED {
		log.Warn().Msgf("replit backend loading %s while already in state %s!", opts.Model, llm.Base.State.String())
	}

	model, err := transformers.NewReplit(opts.ModelFile)
	llm.replit = model
	return err
}

func (llm *Replit) Predict(opts *pb.PredictOptions) (string, error) {
	return llm.replit.Predict(opts.Prompt, buildPredictOptions(opts)...)
}

// fallback to Predict
func (llm *Replit) PredictStream(opts *pb.PredictOptions, results chan string) error {
	go func() {
		res, err := llm.replit.Predict(opts.Prompt, buildPredictOptions(opts)...)

		if err != nil {
			fmt.Println("err: ", err)
		}
		results <- res
		close(results)
	}()
	return nil
}
