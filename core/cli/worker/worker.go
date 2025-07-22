package worker

type WorkerFlags struct {
	BackendsPath      string `env:"LOCALAI_BACKENDS_PATH,BACKENDS_PATH" type:"path" default:"${basepath}/backends" help:"Path containing backends used for inferencing" group:"backends"`
	ExtraLLamaCPPArgs string `name:"llama-cpp-args" env:"LOCALAI_EXTRA_LLAMA_CPP_ARGS,EXTRA_LLAMA_CPP_ARGS" help:"Extra arguments to pass to llama-cpp-rpc-server"`
}

type Worker struct {
	P2P      P2P      `cmd:"" name:"p2p-llama-cpp-rpc" help:"Starts a LocalAI llama.cpp worker in P2P mode (requires a token)"`
	LLamaCPP LLamaCPP `cmd:"" name:"llama-cpp-rpc" help:"Starts a llama.cpp worker in standalone mode"`
}
