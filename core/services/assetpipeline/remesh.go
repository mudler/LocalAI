// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"context"
	"fmt"
	"maps"
	"os"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	pipeline "github.com/mudler/LocalAI/pkg/assetpipeline"
)

func remeshParams(a pipeline.Arguments, cfg *config.ModelConfig) (map[string]string, error) {
	if cfg.Backend != "meshremesh" {
		return nil, fmt.Errorf("select the standalone meshremesh backend for this stage")
	}
	if a.Input.Type != "mesh" || a.Input.Artifact == "" || len(a.Inputs) != 0 {
		return nil, fmt.Errorf("remeshing requires one mesh artifact")
	}

	if err := schema.ValidateMeshRemeshParameters(a.Node.Params); err != nil {
		return nil, err
	}

	params := maps.Clone(a.Node.Params)
	if params == nil {
		params = map[string]string{}
	}

	params["operation"] = "remesh"
	return params, nil
}

func (e *Executor) remesh(ctx context.Context, resource string, a pipeline.Arguments, cfg *config.ModelConfig, progress func(string)) (out pipeline.Result, err error) {
	params, err := remeshParams(a, cfg)
	if err != nil {
		return out, err
	}

	input, _, err := e.Store.OpenArtifact(ctx, resource, a.Input.Artifact)
	if err != nil {
		return out, err
	}
	defer func() { _ = input.Close() }()
	f, err := os.CreateTemp("", "asset-remesh-*.glb")
	if err != nil {
		return out, err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err := f.Close(); err != nil {
		return out, err
	}
	progress("Remeshing on CPU and projecting materials. No generation weights are loaded; backend logs report wrapping, topology and projection stages.")
	run, err := backend.Model3DGenerationContext(ctx, backend.Model3DGenerationOptions{Image: input.Name(), Destination: f.Name(), Params: params}, e.Loader, *cfg, e.Config)
	if err != nil {
		return out, err
	}

	if err := run(); err != nil {
		return out, err
	}

	value, err := e.saveFile(ctx, resource, f.Name(), "mesh", pipeline.MIMEGLB)
	if err != nil {
		return out, err
	}

	out.Outputs = []pipeline.Value{value}
	out.Explanation = "Remeshed asset saved with quality statistics in GLB extras.localai_remesh."
	if params["mode"] != "wrap" {
		out.Explanation += " Quad targets are density hints; the GLB is triangulated and may contain open boundaries."
	}

	return out, nil
}
