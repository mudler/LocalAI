package transformers

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"fmt"

	"github.com/go-skynet/LocalAI/pkg/grpc/base"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"

	transformers "github.com/go-skynet/go-ggml-transformers.cpp"
)

type Starcoder struct {
	base.SingleThread

	starcoder *transformers.Starcoder
}

func (llm *Starcoder) Load(opts *pb.ModelOptions) error {
	model, err := transformers.NewStarcoder(opts.ModelFile)
	llm.starcoder = model
	return err
}

func (llm *Starcoder) Predict(opts *pb.PredictOptions) (string, error) {
	return llm.starcoder.Predict(opts.Prompt, buildPredictOptions(opts)...)
}

// fallback to Predict
func (llm *Starcoder) PredictStream(opts *pb.PredictOptions, results chan string) error {
	go func() {
		res, err := llm.starcoder.Predict(opts.Prompt, buildPredictOptions(opts)...)

		if err != nil {
			fmt.Println("err: ", err)
		}
		results <- res
		close(results)
	}()

	return nil
}
