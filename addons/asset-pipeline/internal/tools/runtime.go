// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/atomicfile"
	"github.com/mudler/LocalAI/pkg/addons/sdk/hostclient"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
	pipeline "github.com/mudler/LocalAI/pkg/assetpipeline"
)

type Runtime struct {
	engine interface {
		Do(context.Context, string, string, any, any) error
	}
	store *pipeline.Store
	dir   string
	err   error
}

func New(s protocol.Startup) *Runtime {
	if s.DataDir == "" {
		return &Runtime{err: errors.New("pipeline data directory is required")}
	}
	dir := filepath.Join(s.DataDir, "pipeline")
	store, err := pipeline.Open(dir)
	if err == nil {
		err = os.MkdirAll(filepath.Join(dir, "receipts"), 0700)
	}
	return &Runtime{engine: hostclient.New(s), store: store, dir: dir, err: err}
}

type arguments struct {
	Tool     string           `json:"tool"`
	Name     string           `json:"name,omitempty"`
	Command  pipeline.Command `json:"command,omitempty"`
	Branch   string           `json:"branch,omitempty"`
	Before   int              `json:"before,omitempty"`
	Artifact string           `json:"artifact,omitempty"`
	MIME     string           `json:"mime,omitempty"`
	Data     string           `json:"data,omitempty"`
	Upload   string           `json:"upload,omitempty"`
	Offset   int              `json:"offset,omitempty"`
	Final    bool             `json:"final,omitempty"`
}

func decode(env contract.Envelope) (arguments, error) {
	var a arguments
	err := protocol.DecodeArgs(env.Request.Arguments, &a)
	return a, err
}
func payload(v pipeline.View) json.RawMessage {
	return protocol.Payload(map[string]any{"name": v.Name, "revision": contract.Revision{Schema: 1, Data: v.Revision}, "view": v})
}

func (r *Runtime) Read(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	if r.err != nil {
		return contract.Result{}, r.err
	}
	a, err := decode(env)
	if err != nil {
		return contract.Result{}, err
	}
	if a.Tool == "pipeline-artifact" {
		asset, err := r.store.Artifact(ctx, env.Request.ResourceID, a.Artifact)
		if err != nil {
			return contract.Result{}, protocol.ErrNotFound
		}
		return contract.Result{Payload: protocol.Payload(asset), Outputs: []contract.OutputDescription{{Handle: "asset-" + asset.ID, Name: asset.Name, MediaType: string(asset.MIME), Size: asset.Bytes}}}, nil
	}
	if a.Tool != "pipeline-inspect" && a.Tool != "resource-exists" {
		return contract.Result{}, protocol.ErrBadRequest
	}
	if a.Tool == "resource-exists" {
		// Recovery asks under the owner's reconciliation identity, which must
		// not acquire inference authority just to inspect durable backing.
		_, err := r.store.Read(ctx, env.Request.ResourceID)
		if err != nil && !errors.Is(err, pipeline.ErrNotFound) {
			return contract.Result{}, err
		}
		return contract.Result{Payload: protocol.Payload(map[string]bool{"exists": err == nil})}, nil
	}
	var view pipeline.View
	err = r.engine.Do(ctx, env.Request.OperationID, "pipeline.read", map[string]any{"branch": a.Branch, "before": a.Before}, &view)
	if err != nil {
		return contract.Result{}, protocol.FromHostError(err)
	}
	return contract.Result{Payload: payload(view)}, nil
}

func (r *Runtime) Preview(ctx context.Context, env contract.Envelope) (contract.Preview, error) {
	a, err := decode(env)
	if err != nil {
		return contract.Preview{}, err
	}
	p := contract.Preview{OperationID: env.Request.OperationID, ExpiresAt: time.Now().Add(time.Minute), Revision: contract.Revision{Schema: 1}, Summary: protocol.Payload(map[string]any{"count": 1, "sample": map[string]string{"action": a.Tool, "name": a.Name}})}
	if a.Tool == "pipeline-create" {
		return p, nil
	}
	if a.Tool != "pipeline-change" && a.Tool != "pipeline-upload" {
		return p, protocol.ErrBadRequest
	}
	var view pipeline.View
	if err := r.engine.Do(ctx, env.Request.OperationID, "pipeline.read", map[string]any{}, &view); err != nil {
		return p, protocol.FromHostError(err)
	}
	p.Revision.Data = view.Revision
	return p, nil
}

func (r *Runtime) Apply(ctx context.Context, env contract.Envelope, revision contract.Revision) (contract.Receipt, error) {
	if r.err != nil {
		return contract.Receipt{}, r.err
	}
	if rec, err := r.Receipt(ctx, env.Request.OperationID); err == nil {
		return rec, nil
	}
	a, err := decode(env)
	if err != nil {
		return contract.Receipt{}, err
	}
	var view pipeline.View
	var result contract.Result
	switch a.Tool {
	case "pipeline-create", "pipeline-change":
		cmd := a.Command
		if a.Tool == "pipeline-create" {
			cmd = pipeline.Command{Action: "create", Name: a.Name}
		}
		if err := r.engine.Do(ctx, env.Request.OperationID, "pipeline.apply", map[string]any{"revision": revision.Data, "command": cmd}, &view); err != nil {
			return contract.Receipt{}, protocol.FromHostError(err)
		}
		result.Payload = payload(view)
		revision.Data = view.Revision
	case "pipeline-upload":
		var asset pipeline.Artifact
		if err := r.engine.Do(ctx, env.Request.OperationID, "pipeline.upload", map[string]any{"name": a.Name, "mime": a.MIME, "data": a.Data, "upload": a.Upload, "offset": a.Offset, "final": a.Final}, &asset); err != nil {
			return contract.Receipt{}, protocol.FromHostError(err)
		}
		result.Payload = protocol.Payload(asset)
	default:
		return contract.Receipt{}, protocol.ErrBadRequest
	}
	rec := contract.Receipt{OperationID: env.Request.OperationID, State: contract.ReceiptCommitted, Revision: revision, Result: result}
	b, err := json.Marshal(rec)
	if err != nil {
		return rec, err
	}
	if err := atomicfile.Write(r.receiptPath(env.Request.OperationID), b, 0600); err != nil {
		return contract.Receipt{}, err
	}
	return rec, nil
}

func (r *Runtime) receiptPath(op string) string {
	sum := sha256.Sum256([]byte(op))
	return filepath.Join(r.dir, "receipts", hex.EncodeToString(sum[:])+".json")
}
func (r *Runtime) Receipt(_ context.Context, op string) (contract.Receipt, error) {
	b, err := os.ReadFile(r.receiptPath(op))
	if errors.Is(err, os.ErrNotExist) {
		return contract.Receipt{}, protocol.ErrNotFound
	}
	if err != nil {
		return contract.Receipt{}, err
	}
	var rec contract.Receipt
	err = json.Unmarshal(b, &rec)
	return rec, err
}
func (r *Runtime) OpenOutput(ctx context.Context, env contract.Envelope, handle string) (io.ReadCloser, error) {
	if r.err != nil {
		return nil, r.err
	}
	id, ok := strings.CutPrefix(handle, "asset-")
	if !ok {
		return nil, protocol.ErrNotFound
	}
	f, _, err := r.store.OpenArtifact(ctx, env.Request.ResourceID, id)
	return f, err
}
func (r *Runtime) Close(context.Context) error {
	if r.store != nil {
		return r.store.Close()
	}
	return nil
}
