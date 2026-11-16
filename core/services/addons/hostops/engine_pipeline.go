// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	pipeline "github.com/mudler/LocalAI/pkg/assetpipeline"
)

// PipelineResolver opens an engine on the add-on owner's storage. Resource IDs
// and actor identity always come from the host envelope, not tool arguments.
type PipelineResolver func(addon string) (*pipeline.Runner, *pipeline.Store, error)

func RegisterPipeline(b *Broker, resolve PipelineResolver) error {
	wrap := func(fn func(context.Context, Call, *pipeline.Runner, *pipeline.Store) (json.RawMessage, error)) Handler {
		return func(ctx context.Context, c Call) (json.RawMessage, error) {
			if c.ResourceID == "" {
				return nil, ErrBadRequest
			}
			r, s, err := resolve(c.AddonID)
			if err != nil {
				return nil, err
			}
			out, err := fn(ctx, c, r, s)
			if errors.Is(err, pipeline.ErrInvalid) {
				err = fmt.Errorf("%w: %v", ErrBadRequest, err)
			}
			if errors.Is(err, pipeline.ErrNotFound) {
				err = ErrNotFound
			}
			if errors.Is(err, pipeline.ErrConflict) {
				err = ErrConflict
			}
			return out, err
		}
	}
	for _, spec := range []Spec{
		{Op: "pipeline.read", Scope: "read", Feature: "3d", Handler: wrap(func(ctx context.Context, c Call, r *pipeline.Runner, _ *pipeline.Store) (json.RawMessage, error) {
			var args struct {
				Branch string `json:"branch"`
				Before int    `json:"before"`
			}
			if err := decodeArgs(c.Arguments, &args); err != nil {
				return nil, err
			}
			w, err := r.Read(ctx, c.ResourceID)
			if err != nil {
				return nil, err
			}
			if args.Branch != "" {
				if _, err := w.Branch(args.Branch); err != nil {
					return nil, ErrNotFound
				}
			}
			return marshal(pipeline.MakeView(w, args.Branch, args.Before))
		})},
		{Op: "pipeline.apply", Scope: "apply", Feature: "3d", Idempotent: true, Handler: wrap(func(ctx context.Context, c Call, r *pipeline.Runner, _ *pipeline.Store) (json.RawMessage, error) {
			var args struct {
				Revision uint64           `json:"revision"`
				Command  pipeline.Command `json:"command"`
			}
			if err := decodeArgs(c.Arguments, &args); err != nil {
				return nil, err
			}
			principal, err := json.Marshal(c.Actor)
			if err != nil {
				return nil, ErrBadRequest
			}
			w, err := r.Apply(ctx, string(principal), c.ResourceID, c.OperationID, args.Revision, args.Command)
			if err != nil {
				return nil, err
			}
			branch := args.Command.Branch
			if args.Command.Action == pipeline.RunNodeCommand || args.Command.Action == pipeline.RunAllCommand || args.Command.Action == pipeline.ContinueCommand {
				branch = w.ResultBranch
			}
			if args.Command.Action == pipeline.ForkCommand || args.Command.Action == pipeline.RetryCommand {
				branch = w.Branches[len(w.Branches)-1].ID
			}
			return marshal(pipeline.MakeView(w, branch, 0))
		})},
		{Op: "pipeline.upload", Scope: "apply", Feature: "3d", Idempotent: true, Handler: wrap(func(ctx context.Context, c Call, _ *pipeline.Runner, s *pipeline.Store) (json.RawMessage, error) {
			var args struct {
				Name   string                `json:"name"`
				MIME   pipeline.ArtifactMIME `json:"mime"`
				Data   string                `json:"data"`
				Upload string                `json:"upload"`
				Offset int                   `json:"offset"`
				Final  bool                  `json:"final"`
			}
			if err := decodeArgs(c.Arguments, &args); err != nil {
				return nil, err
			}
			data, err := base64.StdEncoding.DecodeString(args.Data)
			if err != nil {
				return nil, ErrBadRequest
			}
			a, err := s.Upload(ctx, c.ResourceID, args.Upload, args.Name, args.MIME, args.Offset, data, args.Final)
			if err != nil {
				return nil, err
			}
			return marshal(a)
		})},
	} {
		if err := b.Register(spec); err != nil {
			return err
		}
	}
	return nil
}
