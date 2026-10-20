// SPDX-License-Identifier: MIT

// Package tools is the runtime of the fine-tuning add-on executable. A job that
// a tool starts is a resource of the host: the host reserves the resource ID,
// and this package remembers which engine job it stands for. A tool can only
// control a job that a tool started, because only those have a resource.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/internal/hostclient"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Tool names. They are the entries of capabilities.tools in the manifest and
// the exact values of arguments.tool.
const (
	ToolResourceExists = "resource-exists"
	ToolList           = "fine-tune-list"
	ToolStart          = "fine-tune-start"
	ToolDataset        = "fine-tune-dataset"
	ToolStatus         = "fine-tune-status"
	ToolCheckpoints    = "fine-tune-checkpoints"
	ToolStop           = "fine-tune-stop"
	ToolExport         = "fine-tune-export"
)

// Tools is the declared tool set of this add-on, in manifest order.
//
// fine-tune-list is a read tool that names no resource: it lists the jobs of the
// calling user, the ones started in the UI included. The host runs it as that
// user and the host operation scopes the answer to the same user. Every other
// tool works on a resource that a tool created.
var Tools = []string{ToolResourceExists, ToolList, ToolStart, ToolDataset, ToolStatus, ToolCheckpoints, ToolStop, ToolExport}

// Engine is the host operation channel. *hostclient.Client implements it, and
// a nil client answers hostclient.ErrNoHostOps.
type Engine interface {
	Do(ctx context.Context, operationID, op string, args, out any) error
}

var _ Engine = (*hostclient.Client)(nil)

// schema is the schema revision of every job. The data revision counts the
// changes a tool made.
const schema uint64 = 1

var (
	modelName     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	exportFormats = map[string]bool{"lora": true, "merged_16bit": true, "merged_4bit": true, "gguf": true}
)

const maxNameRunes = 80

// maxDatasetBytes is the bound of an inline dataset, the one of the host
// operation.
const maxDatasetBytes = 900 << 10

var datasetExt = []string{".jsonl", ".json", ".csv", ".txt"}

type runtime struct {
	eng Engine
	j   *journal
	now func() time.Time
}

// New returns the runtime of the executable. dataDir holds the journal.
func New(eng Engine, dataDir string) contract.Runtime {
	return &runtime{eng: eng, j: openJournal(dataDir), now: time.Now}
}

// startArgs are the arguments of fine-tune-start. The host adds "tool"; the
// name is the label the host shows in the approval card and the resource list.
type startArgs struct {
	Tool           string  `json:"tool"`
	Name           string  `json:"name"`
	Model          string  `json:"model"`
	Backend        string  `json:"backend"`
	TrainingType   string  `json:"trainingType"`
	TrainingMethod string  `json:"trainingMethod"`
	DatasetSource  string  `json:"datasetSource"`
	DatasetSplit   string  `json:"datasetSplit"`
	LearningRate   float64 `json:"learningRate"`
	NumEpochs      int64   `json:"numEpochs"`
	BatchSize      int64   `json:"batchSize"`
	MaxSteps       int64   `json:"maxSteps"`
	AdapterRank    int64   `json:"adapterRank"`
	AdapterAlpha   int64   `json:"adapterAlpha"`
}

// engineStart is the argument object of finetune.start. It has exactly the
// fields the host operation accepts and none of the add-on's own.
type engineStart struct {
	Model          string  `json:"model"`
	Backend        string  `json:"backend,omitempty"`
	TrainingType   string  `json:"trainingType,omitempty"`
	TrainingMethod string  `json:"trainingMethod,omitempty"`
	DatasetSource  string  `json:"datasetSource"`
	DatasetSplit   string  `json:"datasetSplit,omitempty"`
	LearningRate   float64 `json:"learningRate,omitempty"`
	NumEpochs      int64   `json:"numEpochs,omitempty"`
	BatchSize      int64   `json:"batchSize,omitempty"`
	MaxSteps       int64   `json:"maxSteps,omitempty"`
	AdapterRank    int64   `json:"adapterRank,omitempty"`
	AdapterAlpha   int64   `json:"adapterAlpha,omitempty"`
}

// datasetArgs are the arguments of fine-tune-dataset. The name is the file
// name of the dataset and the label of the resource.
type datasetArgs struct {
	Tool    string `json:"tool"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

type stopArgs struct {
	Tool           string `json:"tool"`
	SaveCheckpoint bool   `json:"saveCheckpoint"`
}

type exportArgs struct {
	Tool         string `json:"tool"`
	ExportFormat string `json:"exportFormat"`
	Name         string `json:"name"`
}

// decode reads the arguments into a closed struct: an unknown field or
// trailing data is a bad request.
func decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return protocol.ErrBadRequest
	}
	if _, err := dec.Token(); err != io.EOF {
		return protocol.ErrBadRequest
	}
	return nil
}

func toolOf(env contract.Envelope) (string, error) {
	var a struct {
		Tool string `json:"tool"`
	}
	if json.Unmarshal(env.Request.Arguments, &a) != nil || a.Tool == "" {
		return "", protocol.ErrBadRequest
	}
	return a.Tool, nil
}

func payload(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

// mapErr turns the refusal of a host operation into the protocol error that
// tells the owner what happened. A failure that does not say the request was
// wrong stays as it is, and the owner reads it as uncertain.
func mapErr(err error) error {
	var he *contract.HostError
	if !errors.As(err, &he) {
		return err
	}
	switch he.Code {
	case contract.HostBadRequest, contract.HostTooLarge:
		return fmt.Errorf("%w: %s", protocol.ErrBadRequest, he.Message)
	case contract.HostNotFound:
		return fmt.Errorf("%w: %s", protocol.ErrNotFound, he.Message)
	case contract.HostConflict:
		return fmt.Errorf("%w: %s", protocol.ErrConflict, he.Message)
	case contract.HostFeature, contract.HostScope:
		return fmt.Errorf("%w: %s", protocol.ErrForbidden, he.Message)
	}
	return err
}

// jobOf is the job of the resource. A dataset resource is not a job.
func (r *runtime) jobOf(env contract.Envelope) (job, error) {
	j, ok := r.entryOf(env)
	if !ok || j.Dataset {
		return job{}, protocol.ErrNotFound
	}
	return j, nil
}

func (r *runtime) entryOf(env contract.Envelope) (job, bool) {
	if env.Request.ResourceID == "" {
		return job{}, false
	}
	return r.j.job(env.Request.ResourceID)
}

func (r *runtime) Read(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	tool, err := toolOf(env)
	if err != nil {
		return contract.Result{}, err
	}
	op := env.Request.OperationID
	switch tool {
	case ToolResourceExists:
		j, ok := r.j.job(env.Request.ResourceID)
		if !ok {
			return contract.Result{Payload: payload(map[string]bool{"exists": false})}, nil
		}
		if j.Dataset {
			// The reference lives in the host and the journal; there is no
			// engine job to ask.
			return contract.Result{Payload: payload(map[string]bool{"exists": true})}, nil
		}
		err := r.eng.Do(ctx, op, "finetune.get", map[string]string{"jobId": j.JobID}, nil)
		var he *contract.HostError
		switch {
		case err == nil:
			return contract.Result{Payload: payload(map[string]bool{"exists": true})}, nil
		case errors.As(err, &he) && he.Code == contract.HostNotFound:
			return contract.Result{Payload: payload(map[string]bool{"exists": false})}, nil
		}
		// The engine could not answer. The host settles a creation only on an
		// explicit answer, so this is an error and never "does not exist".
		return contract.Result{}, err
	case ToolList:
		// No resource: the host runs this read as the calling user and the
		// host operation lists only that user's jobs.
		var a struct {
			Tool string `json:"tool"`
		}
		if err := decode(env.Request.Arguments, &a); err != nil {
			return contract.Result{}, err
		}
		var got json.RawMessage
		if err := r.eng.Do(ctx, op, "finetune.list", nil, &got); err != nil {
			return contract.Result{}, mapErr(err)
		}
		return contract.Result{Payload: got}, nil
	case ToolStatus:
		j, ok := r.entryOf(env)
		if !ok {
			return contract.Result{}, protocol.ErrNotFound
		}
		if j.Dataset {
			return contract.Result{Payload: payload(map[string]any{
				"name": j.Name, "revision": contract.Revision{Schema: schema, Data: j.Revision}, "kind": "dataset", "datasetRef": j.Ref,
			})}, nil
		}
		var got map[string]any
		if err := r.eng.Do(ctx, op, "finetune.get", map[string]string{"jobId": j.JobID}, &got); err != nil {
			return contract.Result{}, mapErr(err)
		}
		// The engine job ID stays in the add-on. The name and the revision
		// are what the host reads to label and track the resource.
		delete(got, "id")
		got["name"] = j.Name
		got["revision"] = contract.Revision{Schema: schema, Data: j.Revision}
		return contract.Result{Payload: payload(got)}, nil
	case ToolCheckpoints:
		j, err := r.jobOf(env)
		if err != nil {
			return contract.Result{}, err
		}
		var got json.RawMessage
		if err := r.eng.Do(ctx, op, "finetune.checkpoints", map[string]string{"jobId": j.JobID}, &got); err != nil {
			return contract.Result{}, mapErr(err)
		}
		return contract.Result{Payload: got}, nil
	}
	return contract.Result{}, protocol.ErrBadRequest
}

func (r *runtime) checkStart(a startArgs) error {
	if a.Model == "" || a.DatasetSource == "" || len([]rune(a.Name)) > maxNameRunes ||
		a.LearningRate < 0 || a.NumEpochs < 0 || a.BatchSize < 0 || a.MaxSteps < 0 || a.AdapterRank < 0 || a.AdapterAlpha < 0 {
		return protocol.ErrBadRequest
	}
	return nil
}

func checkExport(a exportArgs) error {
	if a.ExportFormat != "" && !exportFormats[a.ExportFormat] {
		return protocol.ErrBadRequest
	}
	if a.Name != "" && !modelName.MatchString(a.Name) {
		return protocol.ErrBadRequest
	}
	return nil
}

func (r *runtime) Preview(_ context.Context, env contract.Envelope) (contract.Preview, error) {
	tool, err := toolOf(env)
	if err != nil {
		return contract.Preview{}, err
	}
	out := contract.Preview{OperationID: env.Request.OperationID, ExpiresAt: r.now().Add(time.Minute)}
	switch tool {
	case ToolStart:
		var a startArgs
		if err := decode(env.Request.Arguments, &a); err != nil {
			return out, err
		}
		if err := r.checkStart(a); err != nil {
			return out, err
		}
		// The resource does not exist yet; an operation that already
		// committed is replayed by Apply, so a repeat is not a clash.
		if _, taken := r.j.job(env.Request.ResourceID); taken {
			if _, done := r.j.receipt(env.Request.OperationID); !done {
				return out, protocol.ErrConflict
			}
		}
		out.Revision = contract.Revision{Schema: schema, Data: 0}
		out.Summary = payload(map[string]any{"count": 1, "sample": map[string]string{"action": "start fine-tuning job", "name": nameOf(a), "model": a.Model}})
		return out, nil
	case ToolDataset:
		var a datasetArgs
		if err := decode(env.Request.Arguments, &a); err != nil {
			return out, err
		}
		if err := checkDataset(a); err != nil {
			return out, err
		}
		if _, taken := r.j.job(env.Request.ResourceID); taken {
			if _, done := r.j.receipt(env.Request.OperationID); !done {
				return out, protocol.ErrConflict
			}
		}
		out.Revision = contract.Revision{Schema: schema, Data: 0}
		// The card shows the name and the size, never the content.
		out.Summary = payload(map[string]any{"count": 1, "sample": map[string]any{"action": "send dataset", "name": a.Name, "bytes": len(a.Content)}})
		return out, nil
	case ToolStop, ToolExport:
		j, err := r.jobOf(env)
		if err != nil {
			return out, err
		}
		sample := map[string]any{"name": j.Name}
		if tool == ToolStop {
			var a stopArgs
			if err := decode(env.Request.Arguments, &a); err != nil {
				return out, err
			}
			sample["action"], sample["saveCheckpoint"] = "stop fine-tuning job", a.SaveCheckpoint
		} else {
			var a exportArgs
			if err := decode(env.Request.Arguments, &a); err != nil {
				return out, err
			}
			if err := checkExport(a); err != nil {
				return out, err
			}
			sample["action"], sample["exportFormat"] = "export fine-tuned model", a.ExportFormat
		}
		out.Revision = contract.Revision{Schema: schema, Data: j.Revision}
		out.Summary = payload(map[string]any{"count": 1, "sample": sample})
		return out, nil
	}
	return out, protocol.ErrBadRequest
}

// checkDataset applies the rules of the host operation early, so a card is not
// shown for a dataset the host would refuse.
func checkDataset(a datasetArgs) error {
	if a.Content == "" || len(a.Content) > maxDatasetBytes || !utf8.ValidString(a.Content) || len(a.Name) == 0 || len(a.Name) > 255 || strings.ContainsAny(a.Name, `/\`) || a.Name == "." || a.Name == ".." {
		return protocol.ErrBadRequest
	}
	for _, ext := range datasetExt {
		if strings.HasSuffix(a.Name, ext) && len(a.Name) > len(ext) {
			return nil
		}
	}
	return protocol.ErrBadRequest
}

func nameOf(a startArgs) string {
	if a.Name != "" {
		return a.Name
	}
	return a.Model
}

func (r *runtime) Apply(ctx context.Context, env contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	tool, err := toolOf(env)
	if err != nil {
		return contract.Receipt{}, err
	}
	// An operation that already committed is answered from its receipt: the
	// engine ran once and runs no more.
	if rec, ok := r.j.receipt(env.Request.OperationID); ok {
		return rec, nil
	}
	switch tool {
	case ToolStart:
		return r.applyStart(ctx, env)
	case ToolDataset:
		return r.applyDataset(ctx, env)
	case ToolStop:
		var a stopArgs
		if err := decode(env.Request.Arguments, &a); err != nil {
			return contract.Receipt{}, err
		}
		return r.mutate(ctx, env, rev, "finetune.stop", func(id string) any {
			return map[string]any{"jobId": id, "saveCheckpoint": a.SaveCheckpoint}
		})
	case ToolExport:
		var a exportArgs
		if err := decode(env.Request.Arguments, &a); err != nil {
			return contract.Receipt{}, err
		}
		if err := checkExport(a); err != nil {
			return contract.Receipt{}, err
		}
		return r.mutate(ctx, env, rev, "finetune.export", func(id string) any {
			args := map[string]any{"jobId": id}
			if a.ExportFormat != "" {
				args["exportFormat"] = a.ExportFormat
			}
			if a.Name != "" {
				args["name"] = a.Name
			}
			return args
		})
	}
	return contract.Receipt{}, protocol.ErrBadRequest
}

func (r *runtime) applyStart(ctx context.Context, env contract.Envelope) (contract.Receipt, error) {
	var a startArgs
	if err := decode(env.Request.Arguments, &a); err != nil {
		return contract.Receipt{}, err
	}
	if err := r.checkStart(a); err != nil {
		return contract.Receipt{}, err
	}
	res, op := env.Request.ResourceID, env.Request.OperationID
	if res == "" {
		return contract.Receipt{}, protocol.ErrBadRequest
	}
	if _, taken := r.j.job(res); taken {
		return contract.Receipt{}, protocol.ErrConflict
	}
	var started struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	err := r.eng.Do(ctx, op, "finetune.start", engineStart{
		Model: a.Model, Backend: a.Backend, TrainingType: a.TrainingType, TrainingMethod: a.TrainingMethod,
		DatasetSource: a.DatasetSource, DatasetSplit: a.DatasetSplit, LearningRate: a.LearningRate,
		NumEpochs: a.NumEpochs, BatchSize: a.BatchSize, MaxSteps: a.MaxSteps, AdapterRank: a.AdapterRank, AdapterAlpha: a.AdapterAlpha,
	}, &started)
	if err != nil {
		return contract.Receipt{}, mapErr(err)
	}
	if started.ID == "" {
		return contract.Receipt{}, errors.New("the engine started a job and returned no ID")
	}
	rec := contract.Receipt{
		OperationID: op, State: contract.ReceiptCommitted, Revision: contract.Revision{Schema: schema, Data: 1},
		Result: contract.Result{Payload: payload(map[string]string{"status": started.Status, "message": started.Message})},
	}
	// If this write fails the job runs without a mapping. The error is
	// uncertain, so the host asks again with the same operation ID and the
	// engine answers it with the same job.
	if err := r.j.commit(res, &job{JobID: started.ID, Name: nameOf(a), Revision: 1}, rec); err != nil {
		return contract.Receipt{}, err
	}
	return rec, nil
}

// applyDataset sends the inline dataset through the host operation, which
// returns a reference only the calling user can use, and remembers it for the
// resource. The reference is in the receipt the person sees and in the status
// of the resource, which only the owner of the resource can read.
func (r *runtime) applyDataset(ctx context.Context, env contract.Envelope) (contract.Receipt, error) {
	var a datasetArgs
	if err := decode(env.Request.Arguments, &a); err != nil {
		return contract.Receipt{}, err
	}
	if err := checkDataset(a); err != nil {
		return contract.Receipt{}, err
	}
	res, op := env.Request.ResourceID, env.Request.OperationID
	if res == "" {
		return contract.Receipt{}, protocol.ErrBadRequest
	}
	if _, taken := r.j.job(res); taken {
		return contract.Receipt{}, protocol.ErrConflict
	}
	var stored struct {
		DatasetRef string `json:"datasetRef"`
	}
	if err := r.eng.Do(ctx, op, "finetune.dataset", map[string]string{"name": a.Name, "content": a.Content}, &stored); err != nil {
		return contract.Receipt{}, mapErr(err)
	}
	if stored.DatasetRef == "" {
		return contract.Receipt{}, errors.New("the engine stored a dataset and returned no reference")
	}
	rec := contract.Receipt{
		OperationID: op, State: contract.ReceiptCommitted, Revision: contract.Revision{Schema: schema, Data: 1},
		Result: contract.Result{Payload: payload(map[string]string{"datasetRef": stored.DatasetRef})},
	}
	if err := r.j.commit(res, &job{Name: a.Name, Dataset: true, Ref: stored.DatasetRef, Revision: 1}, rec); err != nil {
		return contract.Receipt{}, err
	}
	return rec, nil
}

// mutate runs a host operation on the job of the resource, once, if the
// resource is still at the revision the person approved.
func (r *runtime) mutate(ctx context.Context, env contract.Envelope, rev contract.Revision, op string, args func(jobID string) any) (contract.Receipt, error) {
	j, err := r.jobOf(env)
	if err != nil {
		return contract.Receipt{}, err
	}
	id := env.Request.OperationID
	if rev.Schema != schema || rev.Data != j.Revision {
		return contract.Receipt{OperationID: id, State: contract.ReceiptRejected,
			Result: contract.Result{Payload: payload(map[string]any{"error": "stale", "revision": contract.Revision{Schema: schema, Data: j.Revision}})}}, nil
	}
	var out json.RawMessage
	if err := r.eng.Do(ctx, id, op, args(j.JobID), &out); err != nil {
		return contract.Receipt{}, mapErr(err)
	}
	next := j
	next.Revision++
	rec := contract.Receipt{OperationID: id, State: contract.ReceiptCommitted, Revision: contract.Revision{Schema: schema, Data: next.Revision}}
	if len(out) > 0 && json.Valid(out) {
		rec.Result.Payload = out
	} else {
		rec.Result.Payload = payload(map[string]any{})
	}
	if err := r.j.commit(env.Request.ResourceID, &next, rec); err != nil {
		return contract.Receipt{}, err
	}
	return rec, nil
}

func (r *runtime) Receipt(_ context.Context, op string) (contract.Receipt, error) {
	if rec, ok := r.j.receipt(op); ok {
		return rec, nil
	}
	return contract.Receipt{}, protocol.ErrNotFound
}

// OpenOutput refuses: no tool of this add-on produces a file.
func (r *runtime) OpenOutput(context.Context, contract.Envelope, string) (io.ReadCloser, error) {
	return nil, protocol.ErrNotFound
}
