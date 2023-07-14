package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	rwkv "github.com/donomii/go-rwkv.cpp"
	whisper "github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	grpc "github.com/go-skynet/LocalAI/pkg/grpc"
	"github.com/go-skynet/LocalAI/pkg/langchain"
	"github.com/go-skynet/LocalAI/pkg/stablediffusion"
	"github.com/go-skynet/LocalAI/pkg/tts"
	bloomz "github.com/go-skynet/bloomz.cpp"
	bert "github.com/go-skynet/go-bert.cpp"
	transformers "github.com/go-skynet/go-ggml-transformers.cpp"
	"github.com/hashicorp/go-multierror"
	"github.com/hpcloud/tail"
	gpt4all "github.com/nomic-ai/gpt4all/gpt4all-bindings/golang"
	"github.com/phayes/freeport"
	"github.com/rs/zerolog/log"

	process "github.com/mudler/go-processmanager"
)

const tokenizerSuffix = ".tokenizer.json"

const (
	LlamaBackend           = "llama"
	BloomzBackend          = "bloomz"
	StarcoderBackend       = "starcoder"
	GPTJBackend            = "gptj"
	DollyBackend           = "dolly"
	MPTBackend             = "mpt"
	GPTNeoXBackend         = "gptneox"
	ReplitBackend          = "replit"
	Gpt2Backend            = "gpt2"
	Gpt4AllLlamaBackend    = "gpt4all-llama"
	Gpt4AllMptBackend      = "gpt4all-mpt"
	Gpt4AllJBackend        = "gpt4all-j"
	Gpt4All                = "gpt4all"
	FalconBackend          = "falcon"
	BertEmbeddingsBackend  = "bert-embeddings"
	RwkvBackend            = "rwkv"
	WhisperBackend         = "whisper"
	StableDiffusionBackend = "stablediffusion"
	PiperBackend           = "piper"
	LCHuggingFaceBackend   = "langchain-huggingface"
	//GGLLMFalconBackend     = "falcon"
)

var autoLoadBackends []string = []string{
	LlamaBackend,
	Gpt4All,
	RwkvBackend,
	//GGLLMFalconBackend,
	WhisperBackend,
	BertEmbeddingsBackend,
	GPTNeoXBackend,
	GPTJBackend,
	Gpt2Backend,
	DollyBackend,
	MPTBackend,
	ReplitBackend,
	StarcoderBackend,
	FalconBackend,
	BloomzBackend,
}

var starCoder = func(modelFile string) (interface{}, error) {
	return transformers.NewStarcoder(modelFile)
}

var mpt = func(modelFile string) (interface{}, error) {
	return transformers.NewMPT(modelFile)
}

var dolly = func(modelFile string) (interface{}, error) {
	return transformers.NewDolly(modelFile)
}

// func ggllmFalcon(opts ...ggllm.ModelOption) func(string) (interface{}, error) {
// 	return func(s string) (interface{}, error) {
// 		return ggllm.New(s, opts...)
// 	}
// }

var gptNeoX = func(modelFile string) (interface{}, error) {
	return transformers.NewGPTNeoX(modelFile)
}

var replit = func(modelFile string) (interface{}, error) {
	return transformers.NewReplit(modelFile)
}

var gptJ = func(modelFile string) (interface{}, error) {
	return transformers.NewGPTJ(modelFile)
}

var falcon = func(modelFile string) (interface{}, error) {
	return transformers.NewFalcon(modelFile)
}

var bertEmbeddings = func(modelFile string) (interface{}, error) {
	return bert.New(modelFile)
}

var bloomzLM = func(modelFile string) (interface{}, error) {
	return bloomz.New(modelFile)
}

var transformersLM = func(modelFile string) (interface{}, error) {
	return transformers.New(modelFile)
}

var stableDiffusion = func(assetDir string) (interface{}, error) {
	return stablediffusion.New(assetDir)
}

func piperTTS(assetDir string) func(s string) (interface{}, error) {
	return func(s string) (interface{}, error) {
		return tts.New(assetDir)
	}
}

var whisperModel = func(modelFile string) (interface{}, error) {
	return whisper.New(modelFile)
}

var lcHuggingFace = func(repoId string) (interface{}, error) {
	return langchain.NewHuggingFace(repoId)
}

// func llamaLM(opts ...llama.ModelOption) func(string) (interface{}, error) {
// 	return func(s string) (interface{}, error) {
// 		return llama.New(s, opts...)
// 	}
// }

func gpt4allLM(opts ...gpt4all.ModelOption) func(string) (interface{}, error) {
	return func(s string) (interface{}, error) {
		return gpt4all.New(s, opts...)
	}
}

func rwkvLM(tokenFile string, threads uint32) func(string) (interface{}, error) {
	return func(s string) (interface{}, error) {
		log.Debug().Msgf("Loading RWKV", s, tokenFile)

		model := rwkv.LoadFiles(s, tokenFile, threads)
		if model == nil {
			return nil, fmt.Errorf("could not load model")
		}
		return model, nil
	}
}

// starts the grpcModelProcess for the backend, and returns a grpc client
// It also loads the model
func (ml *ModelLoader) grpcModel(backend string, o *Options) func(string) (interface{}, error) {
	return func(s string) (interface{}, error) {
		log.Debug().Msgf("Loading GRPC Model", backend, *o)

		grpcProcess := filepath.Join(o.assetDir, "backend-assets", "grpc", backend)

		// Make sure the process is executable
		if err := os.Chmod(grpcProcess, 0755); err != nil {
			return nil, err
		}

		log.Debug().Msgf("Loading GRPC Process", grpcProcess)
		port, err := freeport.GetFreePort()
		if err != nil {
			return nil, err
		}

		serverAddress := fmt.Sprintf("localhost:%d", port)

		log.Debug().Msgf("GRPC Service for '%s' (%s) will be running at: '%s'", backend, o.modelFile, serverAddress)

		grpcControlProcess := process.New(
			process.WithTemporaryStateDir(),
			process.WithName(grpcProcess),
			process.WithArgs("--addr", serverAddress))

		ml.grpcProcesses[o.modelFile] = grpcControlProcess

		if err := grpcControlProcess.Run(); err != nil {
			return nil, err
		}

		go func() {
			t, err := tail.TailFile(grpcControlProcess.StderrPath(), tail.Config{Follow: true})
			if err != nil {
				log.Debug().Msgf("Could not tail stderr")
			}
			for line := range t.Lines {
				log.Debug().Msgf("GRPC(%s): stderr %s", strings.Join([]string{backend, o.modelFile, serverAddress}, "-"), line.Text)
			}
		}()
		go func() {
			t, err := tail.TailFile(grpcControlProcess.StdoutPath(), tail.Config{Follow: true})
			if err != nil {
				log.Debug().Msgf("Could not tail stdout")
			}
			for line := range t.Lines {
				log.Debug().Msgf("GRPC(%s): stderr %s", strings.Join([]string{backend, o.modelFile, serverAddress}, "-"), line.Text)
			}
		}()

		log.Debug().Msgf("GRPC Service Started")

		client := grpc.NewClient(serverAddress)

		// Wait for the service to start up
		ready := false
		for i := 0; i < 10; i++ {
			if client.HealthCheck(context.Background()) {
				log.Debug().Msgf("GRPC Service Ready")
				ready = true
				break
			}
			time.Sleep(1 * time.Second)
		}

		if !ready {
			log.Debug().Msgf("GRPC Service NOT ready")
			log.Debug().Msgf("Alive: ", grpcControlProcess.IsAlive())
			log.Debug().Msgf(fmt.Sprintf("GRPC Service Exitcode:"))

			log.Debug().Msgf(grpcControlProcess.ExitCode())

			return nil, fmt.Errorf("grpc service not ready")
		}

		options := *o.gRPCOptions
		options.Model = s

		log.Debug().Msgf("GRPC: Loading model with options: %+v", options)

		res, err := client.LoadModel(context.TODO(), &options)
		if err != nil {
			return nil, err
		}
		if !res.Success {
			return nil, fmt.Errorf("could not load model: %s", res.Message)
		}

		return client, nil
	}
}

func (ml *ModelLoader) BackendLoader(opts ...Option) (model interface{}, err error) {

	//backendString string, modelFile string, llamaOpts []llama.ModelOption, threads uint32, assetDir string) (model interface{}, err error) {

	o := NewOptions(opts...)

	log.Debug().Msgf("Loading model %s from %s", o.backendString, o.modelFile)
	switch strings.ToLower(o.backendString) {
	case LlamaBackend:
		//	return ml.LoadModel(o.modelFile, llamaLM(o.llamaOpts...))
		return ml.LoadModel(o.modelFile, ml.grpcModel(LlamaBackend, o))
	case BloomzBackend:
		return ml.LoadModel(o.modelFile, bloomzLM)
	case GPTJBackend:
		return ml.LoadModel(o.modelFile, gptJ)
	case DollyBackend:
		return ml.LoadModel(o.modelFile, dolly)
	case MPTBackend:
		return ml.LoadModel(o.modelFile, mpt)
	case Gpt2Backend:
		return ml.LoadModel(o.modelFile, transformersLM)
	case FalconBackend:
		return ml.LoadModel(o.modelFile, ml.grpcModel(FalconBackend, o))
	case GPTNeoXBackend:
		return ml.LoadModel(o.modelFile, gptNeoX)
	case ReplitBackend:
		return ml.LoadModel(o.modelFile, replit)
	case StableDiffusionBackend:
		return ml.LoadModel(o.modelFile, stableDiffusion)
	case PiperBackend:
		return ml.LoadModel(o.modelFile, piperTTS(filepath.Join(o.assetDir, "backend-assets", "espeak-ng-data")))
	case StarcoderBackend:
		return ml.LoadModel(o.modelFile, starCoder)
	case Gpt4AllLlamaBackend, Gpt4AllMptBackend, Gpt4AllJBackend, Gpt4All:
		return ml.LoadModel(o.modelFile, gpt4allLM(gpt4all.SetThreads(int(o.threads)), gpt4all.SetLibrarySearchPath(filepath.Join(o.assetDir, "backend-assets", "gpt4all"))))
	case BertEmbeddingsBackend:
		return ml.LoadModel(o.modelFile, bertEmbeddings)
	case RwkvBackend:
		return ml.LoadModel(o.modelFile, rwkvLM(filepath.Join(ml.ModelPath, o.modelFile+tokenizerSuffix), o.threads))
	case WhisperBackend:
		return ml.LoadModel(o.modelFile, whisperModel)
	case LCHuggingFaceBackend:
		return ml.LoadModel(o.modelFile, lcHuggingFace)
	default:
		return nil, fmt.Errorf("backend unsupported: %s", o.backendString)
	}
}

func (ml *ModelLoader) GreedyLoader(opts ...Option) (interface{}, error) {
	o := NewOptions(opts...)

	log.Debug().Msgf("Loading model '%s' greedly", o.modelFile)

	ml.mu.Lock()
	m, exists := ml.models[o.modelFile]
	if exists {
		log.Debug().Msgf("Model '%s' already loaded", o.modelFile)
		ml.mu.Unlock()
		return m, nil
	}
	ml.mu.Unlock()
	var err error

	for _, b := range autoLoadBackends {
		if b == BloomzBackend || b == WhisperBackend || b == RwkvBackend { // do not autoload bloomz/whisper/rwkv
			continue
		}
		log.Debug().Msgf("[%s] Attempting to load", b)

		model, modelerr := ml.BackendLoader(
			WithBackendString(b),
			WithModelFile(o.modelFile),
			WithLoadGRPCOpts(o.gRPCOptions),
			WithThreads(o.threads),
			WithAssetDir(o.assetDir),
		)
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
