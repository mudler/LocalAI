// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"context"
	"errors"
	"maps"
	"os"
	"strconv"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	pipeline "github.com/mudler/LocalAI/pkg/assetpipeline"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

func motionRequest(a pipeline.Arguments, cfg *config.ModelConfig) (*pb.Animate3DRequest, error) {
	if a.Input.Type != "text" || len(a.Inputs) != 0 {
		return nil, errors.New("motion generation requires a text prompt")
	}
	if a.Node.Seed < 0 {
		return nil, errors.New("motion seed must be non-negative")
	}

	params := maps.Clone(a.Node.Params)
	if params == nil {
		params = make(map[string]string)
	}
	// A second seed in Params would make the recorded node settings ambiguous.
	if _, exists := params["seed"]; exists {
		return nil, errors.New("set the motion seed on the node, not in parameters")
	}

	params["seed"] = strconv.FormatInt(a.Node.Seed, 10)
	input := &schema.Model3DAnimationRequest{
		Inputs: map[string]schema.AnimationInput{"prompt": {Type: "text", Data: a.Input.Text}},
		Params: params,
	}

	if err := cfg.ValidateAnimationRequest(input); err != nil {
		return nil, err
	}

	return &pb.Animate3DRequest{
		Inputs: map[string]*pb.AnimationInput{"prompt": {Type: "text", Data: a.Input.Text}},
		Params: params,
	}, nil
}

func (e *Executor) motion(ctx context.Context, resource string, a pipeline.Arguments, cfg *config.ModelConfig, progress func(string)) (out pipeline.Result, err error) {
	request, err := motionRequest(a, cfg)
	if err != nil {
		return out, err
	}

	f, err := os.CreateTemp("", "asset-motion-*.glb")
	if err != nil {
		return out, err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err := f.Close(); err != nil {
		return out, err
	}

	request.Dst = f.Name()
	progress("Generating skeleton motion. The backend does not stream intermediate poses; the completed motion must be applied to a compatible rig.")
	_, err = backend.Model3DAnimation(ctx, request, e.Loader, *cfg, e.Config)
	if err != nil {
		return out, err
	}

	value, err := e.saveFile(ctx, resource, request.Dst, "motion", pipeline.MIMEGLB)
	if err != nil {
		return out, err
	}

	out.Outputs = []pipeline.Value{value}
	out.Explanation = "Skeleton motion generated; this output does not contain a skinned mesh."
	return out, nil
}
