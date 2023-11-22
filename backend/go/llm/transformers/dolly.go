package transformers

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"fmt"

	"github.com/go-skynet/LocalAI/pkg/grpc/base"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"

	transformers "github.com/go-skynet/go-ggml-transformers.cpp"
)

type Dolly struct {
	base.SingleThread

	dolly *transformers.Dolly
}

func (llm *Dolly) Load(opts *pb.ModelOptions) error {
	model, err := transformers.NewDolly(opts.ModelFile)
	llm.dolly = model
	return err
}

func (llm *Dolly) Predict(opts *pb.PredictOptions) (string, error) {
	return llm.dolly.Predict(opts.Prompt, buildPredictOptions(opts)...)
}

// fallback to Predict
func (llm *Dolly) PredictStream(opts *pb.PredictOptions, results chan string) error {

	go func() {
		res, err := llm.dolly.Predict(opts.Prompt, buildPredictOptions(opts)...)

		if err != nil {
			fmt.Println("err: ", err)
		}
		results <- res
		close(results)
	}()

	return nil
}
