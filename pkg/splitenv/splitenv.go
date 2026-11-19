// Package splitenv sets the environment variables that the split-model
// engines read when a model loads. It changes the environment of the process,
// so only the code that owns the process (the CLI command, the worker) may
// call it: a service must not.
package splitenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// The environment variables that the engines read when a model loads.
const (
	// LlamaServers lists the llama.cpp RPC servers, separated by commas.
	LlamaServers = "LLAMACPP_GRPC_SERVERS"
	// MLXHostfile is the path of the MLX ring hostfile of rank 0.
	MLXHostfile = "MLX_DISTRIBUTED_HOSTFILE"
)

// mlxHostfileName is the file that SetMLX writes.
const mlxHostfileName = "localai_mlx_hostfile.json"

// SetLlama sets LlamaServers in the environment of this process, or
// unsets it with no address, so that llama.cpp does not read an empty list.
// A backend process that starts afterwards inherits it.
func SetLlama(addrs []string) error {
	if len(addrs) == 0 {
		return os.Unsetenv(LlamaServers)
	}
	return os.Setenv(LlamaServers, strings.Join(addrs, ","))
}

// SetMLX writes the ring hostfile under dir and sets MLXHostfile to it,
// or unsets it with no entry: MLX then runs on this machine alone.
func SetMLX(dir string, hostfile []string) error {
	if len(hostfile) == 0 {
		return os.Unsetenv(MLXHostfile)
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
	return os.Setenv(MLXHostfile, path)
}
