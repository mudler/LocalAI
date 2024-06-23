package main

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"fmt"
	"os"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/langchain"
)

type LLM struct {
	base.Base

	langchain *langchain.HuggingFace
	model     string
}

func (llm *LLM) Load(opts *pb.ModelOptions) error {
	var err error
	hfToken := os.Getenv("HUGGINGFACEHUB_API_TOKEN")
	if hfToken == "" {
		return fmt.Errorf("no huggingface token provided")
	}
	llm.langchain, err = langchain.NewHuggingFace(opts.Model, hfToken)
	llm.model = opts.Model
	return err
}

func (llm *LLM) Predict(opts *pb.PredictOptions) (string, error) {
	o := []langchain.PredictOption{
		langchain.SetModel(llm.model),
		langchain.SetMaxTokens(int(opts.Tokens)),
		langchain.SetTemperature(float64(opts.Temperature)),
		langchain.SetStopWords(opts.StopPrompts),
	}
	pred, err := llm.langchain.PredictHuggingFace(opts.Prompt, o...)
	if err != nil {
		return "", err
	}
	return pred.Completion, nil
}

func (llm *LLM) PredictStream(opts *pb.PredictOptions, results chan string) error {
	o := []langchain.PredictOption{
		langchain.SetModel(llm.model),
		langchain.SetMaxTokens(int(opts.Tokens)),
		langchain.SetTemperature(float64(opts.Temperature)),
		langchain.SetStopWords(opts.StopPrompts),
	}
	go func() {
		res, err := llm.langchain.PredictHuggingFace(opts.Prompt, o...)

		if err != nil {
			fmt.Println("err: ", err)
		}
		results <- res.Completion
		close(results)
	}()

	return nil
}
