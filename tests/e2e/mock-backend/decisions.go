// SPDX-License-Identifier: MIT
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"os"
	"strings"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/systemone"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// This is the real native decision wire protocol, not an HTTP test handler.
// Independent scores deliberately overlap, unlike a softmax classifier.
func mockDecision(ctx context.Context, in *pb.ScoreRequest) (*pb.ScoreResponse, error) {
	opts := snapshotLoadParams()
	if strings.Contains(opts.Model, "mm-cancel") {
		audit := ""
		for _, option := range opts.Options {
			if strings.HasPrefix(option, "decision_audit:") {
				audit = strings.TrimPrefix(option, "decision_audit:")
			}
		}
		if audit == "" {
			return nil, status.Error(codes.InvalidArgument, "missing decision audit path")
		}
		if err := os.WriteFile(audit+".entered", []byte("entered"), 0600); err != nil {
			return nil, err
		}
		<-ctx.Done()
		if err := os.WriteFile(audit+".cancelled", []byte("cancelled"), 0600); err != nil {
			return nil, err
		}
		return nil, ctx.Err()
	}
	if strings.Contains(opts.Model, "mm-unsupported") {
		return nil, status.Error(codes.Unimplemented, "mock has no projector")
	}
	if strings.Contains(opts.Model, "mm-error") {
		return nil, status.Error(codes.Internal, "mock classifier failure")
	}
	var req schema.SystemOneRequest
	if err := json.Unmarshal([]byte(in.Prompt), &req); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	images, err := systemone.CollectImages(&req)
	if err != nil {
		return nil, err
	}
	blue := false
	if len(images) > 0 {
		b, err := base64.StdEncoding.DecodeString(strings.SplitN(images[0], ",", 2)[1])
		if err != nil {
			return nil, err
		}
		im, err := png.Decode(strings.NewReader(string(b)))
		if err != nil {
			return nil, err
		}
		r, _, bval, _ := im.At(0, 0).RGBA()
		blue = bval > r
	}
	answers := map[string]any{}
	for id, q := range req.Questions {
		v := 0.6
		if (strings.Contains(string(q.Instructions), "predominantly blue") && blue) || (strings.Contains(string(q.Instructions), "predominantly red") && !blue) {
			v = 0.95
		}
		answers[id] = map[string]any{"type": "noul", "noul": v}
	}
	var received any
	if err := json.Unmarshal([]byte(in.Prompt), &received); err != nil {
		return nil, err
	}
	out, err := json.Marshal(map[string]any{"answers": answers, "received": received, "usage": map[string]int{"input_tokens": 7, "output_tokens": 0}})
	return &pb.ScoreResponse{ResponseJson: string(out)}, err
}
