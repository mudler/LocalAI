package p2psplit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// The environment variables that the engines read when a model loads.
const (
	// LlamaServersEnv lists the llama.cpp RPC servers, separated by commas.
	LlamaServersEnv = "LLAMACPP_GRPC_SERVERS"
	// MLXHostfileEnv is the path of the MLX ring hostfile of rank 0.
	MLXHostfileEnv = "MLX_DISTRIBUTED_HOSTFILE"
)

// mlxHostfileName is the file that SetMLXEnv writes.
const mlxHostfileName = "localai_mlx_hostfile.json"

// SetLlamaEnv sets LlamaServersEnv in the environment of this process, or
// unsets it with no address, so that llama.cpp does not read an empty list.
// A backend process that starts afterwards inherits it.
func SetLlamaEnv(addrs []string) error {
	if len(addrs) == 0 {
		return os.Unsetenv(LlamaServersEnv)
	}
	return os.Setenv(LlamaServersEnv, strings.Join(addrs, ","))
}

// SetMLXEnv writes the ring hostfile under dir and sets MLXHostfileEnv to it,
// or unsets it with no entry: MLX then runs on this machine alone.
func SetMLXEnv(dir string, hostfile []string) error {
	if len(hostfile) == 0 {
		return os.Unsetenv(MLXHostfileEnv)
	}
	data, err := json.Marshal(hostfile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, mlxHostfileName)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Setenv(MLXHostfileEnv, path)
}
