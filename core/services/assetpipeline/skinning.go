// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"context"
	"fmt"
	"maps"
	"os"
	"strconv"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	pipeline "github.com/mudler/LocalAI/pkg/assetpipeline"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

func skinningRequest(a pipeline.Arguments, cfg *config.ModelConfig) (*schema.Model3DAnimationRequest, error) {
	if a.Input.Type != "mesh" || a.Input.Artifact == "" {
		return nil, fmt.Errorf("this stage requires a mesh artifact")
	}

	secondary := ""
	switch a.Node.Kind {
	case pipeline.Rig:
	case pipeline.Skin:
		secondary = "skeleton"
	case pipeline.Retarget:
		secondary = "motion"
	default:
		return nil, fmt.Errorf("unsupported mesh processing stage")
	}
	if (secondary == "" && len(a.Inputs) != 0) || (secondary != "" && len(a.Inputs) != 1) {
		return nil, fmt.Errorf("incorrect secondary inputs for %s", a.Node.Kind)
	}

	inputs := map[string]schema.AnimationInput{"mesh": {Type: "mesh", Data: a.Input.Artifact}}
	if secondary != "" {
		value, ok := a.Inputs[secondary]
		if !ok || value.Artifact == "" {
			return nil, fmt.Errorf("%s artifact is required", secondary)
		}

		inputs[secondary] = schema.AnimationInput{Type: string(value.Type), Data: value.Artifact}
	}

	params := maps.Clone(a.Node.Params)
	if params == nil {
		params = map[string]string{}
	}
	if a.Node.Kind != pipeline.Retarget {
		if a.Node.Seed < 0 {
			return nil, fmt.Errorf("skinning seed must be non-negative")
		}

		if _, ok := params["seed"]; ok {
			return nil, fmt.Errorf("set the seed on the node, not in parameters")
		}

		params["seed"] = strconv.FormatInt(a.Node.Seed, 10)
	}

	request := &schema.Model3DAnimationRequest{Inputs: inputs, Params: params}
	if err := cfg.ValidateAnimationRequest(request); err != nil {
		return nil, err
	}

	return request, nil
}

func (e *Executor) skinning(ctx context.Context, resource string, a pipeline.Arguments, cfg *config.ModelConfig, progress func(string)) (out pipeline.Result, err error) {
	input, err := skinningRequest(a, cfg)
	if err != nil {
		return out, err
	}

	request := &pb.Animate3DRequest{Inputs: map[string]*pb.AnimationInput{}, Params: input.Params}
	for name, value := range input.Inputs {
		file, _, err := e.Store.OpenArtifact(ctx, resource, value.Data)
		if err != nil {
			return out, err
		}
		defer func() { _ = file.Close() }()
		request.Inputs[name] = &pb.AnimationInput{Type: value.Type, Data: file.Name()}
	}

	file, err := os.CreateTemp("", "asset-rig-*.glb")
	if err != nil {
		return out, err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if err := file.Close(); err != nil {
		return out, err
	}

	request.Dst = file.Name()
	switch a.Node.Kind {
	case pipeline.Rig:
		progress("Generating a skeleton and learned skinning weights. The backend does not stream intermediate rig predictions.")
	case pipeline.Skin:
		progress("Generating learned skinning weights for the supplied skeleton. This can take several minutes.")
	case pipeline.Retarget:
		progress("Checking humanoid compatibility and applying SOMA30 motion. Incompatible rigs are rejected rather than forced into a match.")
	}

	if _, err := backend.Model3DAnimation(ctx, request, e.Loader, *cfg, e.Config); err != nil {
		return out, err
	}

	value, err := e.saveFile(ctx, resource, request.Dst, "mesh", pipeline.MIMEGLB)
	if err != nil {
		return out, err
	}

	out.Outputs = []pipeline.Value{value}
	return out, nil
}
