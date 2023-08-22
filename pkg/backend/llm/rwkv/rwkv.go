package rwkv

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"fmt"
	"path/filepath"

	"github.com/donomii/go-rwkv.cpp"
	"github.com/go-skynet/LocalAI/pkg/grpc/base"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
)

const tokenizerSuffix = ".tokenizer.json"

type LLM struct {
	base.SingleThread

	rwkv *rwkv.RwkvState
}

func (llm *LLM) Load(opts *pb.ModelOptions) error {
	tokenizerFile := opts.Tokenizer
	if tokenizerFile == "" {
		modelFile := filepath.Base(opts.ModelFile)
		tokenizerFile = modelFile + tokenizerSuffix
	}
	modelPath := filepath.Dir(opts.ModelFile)
	tokenizerPath := filepath.Join(modelPath, tokenizerFile)

	model := rwkv.LoadFiles(opts.ModelFile, tokenizerPath, uint32(opts.GetThreads()))

	if model == nil {
		return fmt.Errorf("could not load model")
	}
	llm.rwkv = model
	return nil
}

func (llm *LLM) Predict(opts *pb.PredictOptions) (string, error) {
	stopWord := "\n"
	if len(opts.StopPrompts) > 0 {
		stopWord = opts.StopPrompts[0]
	}

	if err := llm.rwkv.ProcessInput(opts.Prompt); err != nil {
		return "", err
	}

	response := llm.rwkv.GenerateResponse(int(opts.Tokens), stopWord, float32(opts.Temperature), float32(opts.TopP), nil)

	return response, nil
}

func (llm *LLM) PredictStream(opts *pb.PredictOptions, results chan string) error {
	go func() {

		stopWord := "\n"
		if len(opts.StopPrompts) > 0 {
			stopWord = opts.StopPrompts[0]
		}

		if err := llm.rwkv.ProcessInput(opts.Prompt); err != nil {
			fmt.Println("Error processing input: ", err)
			return
		}

		llm.rwkv.GenerateResponse(int(opts.Tokens), stopWord, float32(opts.Temperature), float32(opts.TopP), func(s string) bool {
			results <- s
			return true
		})
		close(results)
	}()

	return nil
}

func (llm *LLM) TokenizeString(opts *pb.PredictOptions) (pb.TokenizationResponse, error) {
	tokens, err := llm.rwkv.Tokenizer.Encode(opts.Prompt)
	if err != nil {
		return pb.TokenizationResponse{}, err
	}

	l := len(tokens)
	i32Tokens := make([]int32, l)

	for i, t := range tokens {
		i32Tokens[i] = int32(t.ID)
	}

	return pb.TokenizationResponse{
		Length: int32(l),
		Tokens: i32Tokens,
	}, nil
}
