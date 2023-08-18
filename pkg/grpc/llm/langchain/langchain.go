package langchain

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"fmt"

	"github.com/go-skynet/LocalAI/pkg/grpc/base"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"github.com/go-skynet/LocalAI/pkg/langchain"
	"github.com/rs/zerolog/log"
)

type LLM struct {
	base.Base

	langchain *langchain.HuggingFace
	model     string
}

func (llm *LLM) Load(opts *pb.ModelOptions) error {
	if llm.Base.State != pb.StatusResponse_UNINITIALIZED {
		log.Warn().Msgf("langchain backend loading %s while already in state %s!", opts.Model, llm.Base.State.String())
	}

	llm.Base.Lock()
	defer llm.Base.Unlock()
	llm.langchain, _ = langchain.NewHuggingFace(opts.Model)
	llm.model = opts.Model
	return nil
}

func (llm *LLM) Predict(opts *pb.PredictOptions) (string, error) {
	llm.Base.Lock()
	defer llm.Base.Unlock()

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
	llm.Base.Lock()
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
		llm.Base.Unlock()
	}()

	return nil
}
