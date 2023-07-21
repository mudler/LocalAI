package model

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	grpc "github.com/go-skynet/LocalAI/pkg/grpc"
	"github.com/hashicorp/go-multierror"
	"github.com/hpcloud/tail"
	"github.com/phayes/freeport"
	"github.com/rs/zerolog/log"

	process "github.com/mudler/go-processmanager"
)

const tokenizerSuffix = ".tokenizer.json"

const (
	LlamaBackend        = "llama"
	BloomzBackend       = "bloomz"
	StarcoderBackend    = "starcoder"
	GPTJBackend         = "gptj"
	DollyBackend        = "dolly"
	MPTBackend          = "mpt"
	GPTNeoXBackend      = "gptneox"
	ReplitBackend       = "replit"
	Gpt2Backend         = "gpt2"
	Gpt4AllLlamaBackend = "gpt4all-llama"
	Gpt4AllMptBackend   = "gpt4all-mpt"
	Gpt4AllJBackend     = "gpt4all-j"
	Gpt4All             = "gpt4all"
	FalconBackend       = "falcon"
	FalconGGMLBackend   = "falcon-ggml"
	LlamaGrammarBackend = "llama-grammar"

	BertEmbeddingsBackend  = "bert-embeddings"
	RwkvBackend            = "rwkv"
	WhisperBackend         = "whisper"
	StableDiffusionBackend = "stablediffusion"
	PiperBackend           = "piper"
	LCHuggingFaceBackend   = "langchain-huggingface"
	//GGLLMFalconBackend     = "falcon"
)

var AutoLoadBackends []string = []string{
	LlamaBackend,
	Gpt4All,
	FalconBackend,
	GPTNeoXBackend,
	BertEmbeddingsBackend,
	LlamaGrammarBackend,
	FalconGGMLBackend,
	GPTJBackend,
	Gpt2Backend,
	DollyBackend,
	MPTBackend,
	ReplitBackend,
	StarcoderBackend,
}

func (ml *ModelLoader) StopGRPC() {
	for _, p := range ml.grpcProcesses {
		p.Stop()
	}
}

// starts the grpcModelProcess for the backend, and returns a grpc client
// It also loads the model
func (ml *ModelLoader) grpcModel(backend string, o *Options) func(string) (*grpc.Client, error) {
	return func(s string) (*grpc.Client, error) {
		log.Debug().Msgf("Loading GRPC Model %s: %+v", backend, *o)

		grpcProcess := filepath.Join(o.assetDir, "backend-assets", "grpc", backend)

		// Check if the file exists
		if _, err := os.Stat(grpcProcess); os.IsNotExist(err) {
			return nil, fmt.Errorf("grpc process not found: %s. some backends(stablediffusion, tts) require LocalAI compiled with GO_TAGS", grpcProcess)
		}

		// Make sure the process is executable
		if err := os.Chmod(grpcProcess, 0755); err != nil {
			return nil, err
		}

		log.Debug().Msgf("Loading GRPC Process %s", grpcProcess)
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

		// clean up process
		go func() {
			c := make(chan os.Signal, 1)
			signal.Notify(c, os.Interrupt, syscall.SIGTERM)
			<-c
			grpcControlProcess.Stop()
		}()

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
			log.Debug().Msgf("Alive: %t", grpcControlProcess.IsAlive())
			exitCode, exitErr := grpcControlProcess.ExitCode()
			log.Debug().Msgf(fmt.Sprintf("GRPC Service Exitcode: %s (%+v)", exitCode, exitErr))

			log.Debug().Msgf(grpcControlProcess.ExitCode())

			return nil, fmt.Errorf("grpc service not ready")
		}

		options := *o.gRPCOptions
		options.Model = s

		log.Debug().Msgf("GRPC: Loading model with options: %+v", options)

		res, err := client.LoadModel(o.context, &options)
		if err != nil {
			return nil, err
		}
		if !res.Success {
			return nil, fmt.Errorf("could not load model: %s", res.Message)
		}

		return client, nil
	}
}

func (ml *ModelLoader) BackendLoader(opts ...Option) (model *grpc.Client, err error) {
	o := NewOptions(opts...)

	log.Debug().Msgf("Loading model %s from %s", o.backendString, o.modelFile)

	backend := strings.ToLower(o.backendString)
	switch backend {
	case LlamaBackend, LlamaGrammarBackend, GPTJBackend, DollyBackend,
		MPTBackend, Gpt2Backend, FalconBackend,
		GPTNeoXBackend, ReplitBackend, StarcoderBackend, BloomzBackend,
		RwkvBackend, LCHuggingFaceBackend, BertEmbeddingsBackend, FalconGGMLBackend, StableDiffusionBackend, WhisperBackend:
		return ml.LoadModel(o.modelFile, ml.grpcModel(backend, o))
	case Gpt4AllLlamaBackend, Gpt4AllMptBackend, Gpt4AllJBackend, Gpt4All:
		o.gRPCOptions.LibrarySearchPath = filepath.Join(o.assetDir, "backend-assets", "gpt4all")
		return ml.LoadModel(o.modelFile, ml.grpcModel(Gpt4All, o))
	case PiperBackend:
		o.gRPCOptions.LibrarySearchPath = filepath.Join(o.assetDir, "backend-assets", "espeak-ng-data")
		return ml.LoadModel(o.modelFile, ml.grpcModel(PiperBackend, o))
	default:
		return nil, fmt.Errorf("backend unsupported: %s", o.backendString)
	}
}

func (ml *ModelLoader) GreedyLoader(opts ...Option) (*grpc.Client, error) {
	o := NewOptions(opts...)

	log.Debug().Msgf("Loading model '%s' greedly", o.modelFile)

	// Is this really needed? BackendLoader already does this
	ml.mu.Lock()
	if m := ml.checkIsLoaded(o.modelFile); m != nil {
		log.Debug().Msgf("Model '%s' already loaded", o.modelFile)
		ml.mu.Unlock()
		return m, nil
	}
	ml.mu.Unlock()
	var err error

	for _, b := range AutoLoadBackends {
		log.Debug().Msgf("[%s] Attempting to load", b)

		model, modelerr := ml.BackendLoader(
			WithBackendString(b),
			WithModelFile(o.modelFile),
			WithLoadGRPCLLMModelOpts(o.gRPCOptions),
			WithThreads(o.threads),
			WithAssetDir(o.assetDir),
		)
		if modelerr == nil && model != nil {
			log.Debug().Msgf("[%s] Loads OK", b)
			return model, nil
		} else if modelerr != nil {
			err = multierror.Append(err, modelerr)
			log.Debug().Msgf("[%s] Fails: %s", b, modelerr.Error())
		} else if model == nil {
			err = multierror.Append(err, modelerr)
			log.Debug().Msgf("[%s] Fails: %s", b, "backend returned no usable model")
		}
	}

	return nil, fmt.Errorf("could not load model - all backends returned error: %s", err.Error())
}
