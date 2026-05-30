package worker

type WorkerFlags struct {
	BackendsPath            string `env:"LOCALAI_BACKENDS_PATH,BACKENDS_PATH" type:"path" default:"${basepath}/backends" help:"Path containing backends used for inferencing" group:"backends"`
	BackendGalleries        string `env:"LOCALAI_BACKEND_GALLERIES,BACKEND_GALLERIES" help:"JSON list of backend galleries" group:"backends" default:"${backends}"`
	BackendsSystemPath      string `env:"LOCALAI_BACKENDS_SYSTEM_PATH,BACKEND_SYSTEM_PATH" type:"path" default:"/var/lib/local-ai/backends" help:"Path containing system backends used for inferencing" group:"backends"`
	RequireBackendIntegrity bool   `env:"LOCALAI_REQUIRE_BACKEND_INTEGRITY,REQUIRE_BACKEND_INTEGRITY" help:"If true, reject backend installs without a configured signature verification policy (OCI URIs) or SHA256 (tarball/HTTP URIs)." group:"hardening" default:"false"`
	ExtraLLamaCPPArgs       string `name:"llama-cpp-args" env:"LOCALAI_EXTRA_LLAMA_CPP_ARGS,EXTRA_LLAMA_CPP_ARGS" help:"Extra arguments to pass to llama-cpp-rpc-server"`
}

type Worker struct {
	P2P             P2P             `cmd:"" name:"p2p-llama-cpp-rpc" help:"Starts a LocalAI llama.cpp worker in P2P mode (requires a token)"`
	P2PMLX          P2PMLX          `cmd:"" name:"p2p-mlx" help:"Starts a LocalAI MLX distributed worker in P2P mode (requires a token)"`
	LLamaCPP        LLamaCPP        `cmd:"" name:"llama-cpp-rpc" help:"Starts a llama.cpp worker in standalone mode"`
	MLXDistributed  MLXDistributed  `cmd:"" name:"mlx-distributed" help:"Starts an MLX distributed worker in standalone mode (requires --hostfile and --rank)"`
	VLLMDistributed VLLMDistributed `cmd:"" name:"vllm" help:"Starts a vLLM data-parallel follower process. Multi-node DP for a single model: head runs the existing vllm backend with engine_args.data_parallel_size>1, followers run this command."`
	DS4Distributed  DS4Distributed  `cmd:"" name:"ds4-distributed" help:"Starts a ds4 distributed worker in standalone mode: owns a layer slice and dials the coordinator (pass ds4-worker args after --)"`
}
