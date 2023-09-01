package gpt4all

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"fmt"

	"github.com/go-skynet/LocalAI/pkg/grpc/base"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
	gpt4all "github.com/nomic-ai/gpt4all/gpt4all-bindings/golang"
)

type LLM struct {
	base.SingleThread

	gpt4all *gpt4all.Model
}

func (llm *LLM) Load(opts *pb.ModelOptions) error {
	model, err := gpt4all.New(opts.ModelFile,
		gpt4all.SetThreads(int(opts.Threads)),
		gpt4all.SetLibrarySearchPath(opts.LibrarySearchPath))
	llm.gpt4all = model
	return err
}

func buildPredictOptions(opts *pb.PredictOptions) []gpt4all.PredictOption {
	predictOptions := []gpt4all.PredictOption{
		gpt4all.SetTemperature(float64(opts.Temperature)),
		gpt4all.SetTopP(float64(opts.TopP)),
		gpt4all.SetTopK(int(opts.TopK)),
		gpt4all.SetTokens(int(opts.Tokens)),
	}

	if opts.Batch != 0 {
		predictOptions = append(predictOptions, gpt4all.SetBatch(int(opts.Batch)))
	}
	return predictOptions
}

func (llm *LLM) Predict(opts *pb.PredictOptions) (string, error) {
	return llm.gpt4all.Predict(opts.Prompt, buildPredictOptions(opts)...)
}

func (llm *LLM) PredictStream(opts *pb.PredictOptions, results chan string) error {
	predictOptions := buildPredictOptions(opts)

	go func() {
		llm.gpt4all.SetTokenCallback(func(token string) bool {
			results <- token
			return true
		})
		_, err := llm.gpt4all.Predict(opts.Prompt, predictOptions...)
		if err != nil {
			fmt.Println("err: ", err)
		}
		llm.gpt4all.SetTokenCallback(nil)
		close(results)
	}()

	return nil
}
