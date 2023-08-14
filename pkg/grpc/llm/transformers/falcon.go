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

type Falcon struct {
	base.Base

	falcon *transformers.Falcon
}

func (llm *Falcon) Load(opts *pb.ModelOptions) error {
	if llm.Base.State != pb.StatusResponse_UNINITIALIZED {
		log.Warn().Msgf("transformers-falcon backend loading %s while already in state %s!", opts.Model, llm.Base.State.String())
	}

	llm.Base.Lock()
	defer llm.Base.Unlock()
	model, err := transformers.NewFalcon(opts.ModelFile)
	llm.falcon = model
	return err
}

func (llm *Falcon) Predict(opts *pb.PredictOptions) (string, error) {
	llm.Base.Lock()
	defer llm.Base.Unlock()
	return llm.falcon.Predict(opts.Prompt, buildPredictOptions(opts)...)
}

// fallback to Predict
func (llm *Falcon) PredictStream(opts *pb.PredictOptions, results chan string) error {
	llm.Base.Lock()
	go func() {
		res, err := llm.falcon.Predict(opts.Prompt, buildPredictOptions(opts)...)

		if err != nil {
			fmt.Println("err: ", err)
		}
		results <- res
		close(results)
		llm.Base.Unlock()
	}()

	return nil
}
