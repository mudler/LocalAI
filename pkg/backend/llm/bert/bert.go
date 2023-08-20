package bert

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	bert "github.com/go-skynet/go-bert.cpp"

	"github.com/go-skynet/LocalAI/pkg/grpc/base"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
)

type Embeddings struct {
	base.SingleThread
	bert *bert.Bert
}

func (llm *Embeddings) Load(opts *pb.ModelOptions) error {
	model, err := bert.New(opts.ModelFile)
	llm.bert = model
	return err
}

func (llm *Embeddings) Embeddings(opts *pb.PredictOptions) ([]float32, error) {

	if len(opts.EmbeddingTokens) > 0 {
		tokens := []int{}
		for _, t := range opts.EmbeddingTokens {
			tokens = append(tokens, int(t))
		}
		return llm.bert.TokenEmbeddings(tokens, bert.SetThreads(int(opts.Threads)))
	}

	return llm.bert.Embeddings(opts.Embeddings, bert.SetThreads(int(opts.Threads)))
}
