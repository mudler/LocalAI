package transformers

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"fmt"

	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"

	transformers "github.com/go-skynet/go-ggml-transformers.cpp"
)

type GPT2 struct {
	gpt2 *transformers.GPT2
}

func (llm *GPT2) Load(opts *pb.ModelOptions) error {
	model, err := transformers.New(opts.Model)
	llm.gpt2 = model
	return err
}

func (llm *GPT2) Embeddings(opts *pb.PredictOptions) ([]float32, error) {
	return nil, fmt.Errorf("not implemented")
}

func (llm *GPT2) Predict(opts *pb.PredictOptions) (string, error) {
	return llm.gpt2.Predict(opts.Prompt, buildPredictOptions(opts)...)
}

// fallback to Predict
func (llm *GPT2) PredictStream(opts *pb.PredictOptions, results chan string) {
	go func() {
		res, err := llm.gpt2.Predict(opts.Prompt, buildPredictOptions(opts)...)

		if err != nil {
			fmt.Println("err: ", err)
		}
		results <- res
		close(results)
	}()
}
