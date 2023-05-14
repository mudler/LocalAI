package model

import (
	"fmt"
	"path/filepath"
	"strings"

	rwkv "github.com/donomii/go-rwkv.cpp"
	whisper "github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	bloomz "github.com/go-skynet/bloomz.cpp"
	bert "github.com/go-skynet/go-bert.cpp"
	gpt2 "github.com/go-skynet/go-gpt2.cpp"
	llama "github.com/go-skynet/go-llama.cpp"
	"github.com/hashicorp/go-multierror"
	gpt4all "github.com/nomic/gpt4all/gpt4all-bindings/golang"
	"github.com/rs/zerolog/log"
)

const tokenizerSuffix = ".tokenizer.json"

const (
	LlamaBackend          = "llama"
	BloomzBackend         = "bloomz"
	StarcoderBackend      = "starcoder"
	StableLMBackend       = "stablelm"
	DollyBackend          = "dolly"
	RedPajamaBackend      = "redpajama"
	GPTNeoXBackend        = "gptneox"
	ReplitBackend         = "replit"
	Gpt2Backend           = "gpt2"
	Gpt4AllLlamaBackend   = "gpt4all-llama"
	Gpt4AllMptBackend     = "gpt4all-mpt"
	Gpt4AllJBackend       = "gpt4all-j"
	BertEmbeddingsBackend = "bert-embeddings"
	RwkvBackend           = "rwkv"
	WhisperBackend        = "whisper"
)

var backends []string = []string{
	LlamaBackend,
	Gpt4AllLlamaBackend,
	Gpt4AllMptBackend,
	Gpt4AllJBackend,
	Gpt2Backend,
	WhisperBackend,
	RwkvBackend,
	BloomzBackend,
	StableLMBackend,
	DollyBackend,
	RedPajamaBackend,
	GPTNeoXBackend,
	ReplitBackend,
	BertEmbeddingsBackend,
	StarcoderBackend,
}

var starCoder = func(modelFile string) (interface{}, error) {
	return gpt2.NewStarcoder(modelFile)
}

var redPajama = func(modelFile string) (interface{}, error) {
	return gpt2.NewRedPajama(modelFile)
}

var dolly = func(modelFile string) (interface{}, error) {
	return gpt2.NewDolly(modelFile)
}

var gptNeoX = func(modelFile string) (interface{}, error) {
	return gpt2.NewGPTNeoX(modelFile)
}

var replit = func(modelFile string) (interface{}, error) {
	return gpt2.NewReplit(modelFile)
}

var stableLM = func(modelFile string) (interface{}, error) {
	return gpt2.NewStableLM(modelFile)
}

var bertEmbeddings = func(modelFile string) (interface{}, error) {
	return bert.New(modelFile)
}

var bloomzLM = func(modelFile string) (interface{}, error) {
	return bloomz.New(modelFile)
}
var gpt2LM = func(modelFile string) (interface{}, error) {
	return gpt2.New(modelFile)
}

var whisperModel = func(modelFile string) (interface{}, error) {
	return whisper.New(modelFile)
}

func llamaLM(opts ...llama.ModelOption) func(string) (interface{}, error) {
	return func(s string) (interface{}, error) {
		return llama.New(s, opts...)
	}
}

func gpt4allLM(opts ...gpt4all.ModelOption) func(string) (interface{}, error) {
	return func(s string) (interface{}, error) {
		return gpt4all.New(s, opts...)
	}
}

func rwkvLM(tokenFile string, threads uint32) func(string) (interface{}, error) {
	return func(s string) (interface{}, error) {
		model := rwkv.LoadFiles(s, tokenFile, threads)
		if model == nil {
			return nil, fmt.Errorf("could not load model")
		}
		return model, nil
	}
}

func (ml *ModelLoader) BackendLoader(backendString string, modelFile string, llamaOpts []llama.ModelOption, threads uint32) (model interface{}, err error) {
	switch strings.ToLower(backendString) {
	case LlamaBackend:
		return ml.LoadModel(modelFile, llamaLM(llamaOpts...))
	case BloomzBackend:
		return ml.LoadModel(modelFile, bloomzLM)
	case StableLMBackend:
		return ml.LoadModel(modelFile, stableLM)
	case DollyBackend:
		return ml.LoadModel(modelFile, dolly)
	case RedPajamaBackend:
		return ml.LoadModel(modelFile, redPajama)
	case Gpt2Backend:
		return ml.LoadModel(modelFile, gpt2LM)
	case GPTNeoXBackend:
		return ml.LoadModel(modelFile, gptNeoX)
	case ReplitBackend:
		return ml.LoadModel(modelFile, replit)
	case StarcoderBackend:
		return ml.LoadModel(modelFile, starCoder)
	case Gpt4AllLlamaBackend:
		return ml.LoadModel(modelFile, gpt4allLM(gpt4all.SetThreads(int(threads)), gpt4all.SetModelType(gpt4all.LLaMAType)))
	case Gpt4AllMptBackend:
		return ml.LoadModel(modelFile, gpt4allLM(gpt4all.SetThreads(int(threads)), gpt4all.SetModelType(gpt4all.MPTType)))
	case Gpt4AllJBackend:
		return ml.LoadModel(modelFile, gpt4allLM(gpt4all.SetThreads(int(threads)), gpt4all.SetModelType(gpt4all.GPTJType)))
	case BertEmbeddingsBackend:
		return ml.LoadModel(modelFile, bertEmbeddings)
	case RwkvBackend:
		return ml.LoadModel(modelFile, rwkvLM(filepath.Join(ml.ModelPath, modelFile+tokenizerSuffix), threads))
	case WhisperBackend:
		return ml.LoadModel(modelFile, whisperModel)
	default:
		return nil, fmt.Errorf("backend unsupported: %s", backendString)
	}
}

func (ml *ModelLoader) GreedyLoader(modelFile string, llamaOpts []llama.ModelOption, threads uint32) (interface{}, error) {
	log.Debug().Msgf("Loading models greedly")

	ml.mu.Lock()
	m, exists := ml.models[modelFile]
	if exists {
		ml.mu.Unlock()
		return m, nil
	}
	ml.mu.Unlock()
	var err error

	for _, b := range backends {
		if b == BloomzBackend || b == WhisperBackend || b == RwkvBackend { // do not autoload bloomz/whisper/rwkv
			continue
		}
		log.Debug().Msgf("[%s] Attempting to load", b)
		model, modelerr := ml.BackendLoader(b, modelFile, llamaOpts, threads)
		if modelerr == nil && model != nil {
			log.Debug().Msgf("[%s] Loads OK", b)
			return model, nil
		} else if modelerr != nil {
			err = multierror.Append(err, modelerr)
			log.Debug().Msgf("[%s] Fails: %s", b, modelerr.Error())
		}
	}

	return nil, fmt.Errorf("could not load model - all backends returned error: %s", err.Error())
}
