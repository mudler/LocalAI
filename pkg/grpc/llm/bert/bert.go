package bert

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	bert "github.com/go-skynet/go-bert.cpp"
	"github.com/rs/zerolog/log"

	"github.com/go-skynet/LocalAI/pkg/grpc/base"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
)

type Embeddings struct {
	base.Base
	bert *bert.Bert
}

func (llm *Embeddings) Load(opts *pb.ModelOptions) error {
	if llm.Base.State != pb.StatusResponse_UNINITIALIZED {
		log.Warn().Msgf("bert backend loading %s while already in state %s!", opts.Model, llm.Base.State.String())
	}

	llm.Base.Lock()
	defer llm.Base.Unlock()
	model, err := bert.New(opts.ModelFile)
	llm.bert = model
	return err
}

func (llm *Embeddings) Embeddings(opts *pb.PredictOptions) ([]float32, error) {
	llm.Base.Lock()
	defer llm.Base.Unlock()

	if len(opts.EmbeddingTokens) > 0 {
		tokens := []int{}
		for _, t := range opts.EmbeddingTokens {
			tokens = append(tokens, int(t))
		}
		return llm.bert.TokenEmbeddings(tokens, bert.SetThreads(int(opts.Threads)))
	}

	return llm.bert.Embeddings(opts.Embeddings, bert.SetThreads(int(opts.Threads)))
}

func (llm *Embeddings) Unload() error {
	llm.Base.Lock()
	defer llm.Base.Unlock()

	llm.bert.Free()

	return nil
}
