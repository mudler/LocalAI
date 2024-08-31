package worker

type WorkerFlags struct {
	BackendAssetsPath string `env:"LOCALAI_BACKEND_ASSETS_PATH,BACKEND_ASSETS_PATH" type:"path" default:"/tmp/localai/backend_data" help:"Path used to extract libraries that are required by some of the backends in runtime" group:"storage"`
	ExtraLLamaCPPArgs string `name:"llama-cpp-args" env:"LOCALAI_EXTRA_LLAMA_CPP_ARGS,EXTRA_LLAMA_CPP_ARGS" help:"Extra arguments to pass to llama-cpp-rpc-server"`
}

type Worker struct {
	P2P      P2P      `cmd:"" name:"p2p-llama-cpp-rpc" help:"Starts a LocalAI llama.cpp worker in P2P mode (requires a token)"`
	LLamaCPP LLamaCPP `cmd:"" name:"llama-cpp-rpc" help:"Starts a llama.cpp worker in standalone mode"`
}
