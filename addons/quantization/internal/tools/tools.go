// SPDX-License-Identifier: MIT

// Package tools is the runtime of the quantization add-on executable. A job
// that a tool starts is a resource of the host: the host reserves the resource
// ID, and this package remembers which engine job it stands for. A tool can only
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
	"time"

	"github.com/mudler/LocalAI/addons/internal/hostclient"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Tool names. They are the entries of capabilities.tools in the manifest and
// the exact values of arguments.tool.
const (
	ToolResourceExists = "resource-exists"
	ToolList           = "quantize-list"
	ToolStart          = "quantize-start"
	ToolStatus         = "quantize-status"
	ToolStop           = "quantize-stop"
	ToolImport         = "quantize-import"
)

// Tools is the declared tool set of this add-on, in manifest order.
//
// quantize-list is a read tool that names no resource: it lists the jobs of the
// calling user, the ones started in the UI included. The host runs it as that
// user and the host operation scopes the answer to the same user. Every other
// tool works on a resource that a tool created.
var Tools = []string{ToolResourceExists, ToolList, ToolStart, ToolStatus, ToolStop, ToolImport}

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
	modelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	// quantType is the rule of the host operation for a quantization name.
	quantType = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
)

const maxNameRunes = 80

type runtime struct {
	eng Engine
	j   *journal
	now func() time.Time
}

// New returns the runtime of the executable. dataDir holds the journal.
func New(eng Engine, dataDir string) contract.Runtime {
	return &runtime{eng: eng, j: openJournal(dataDir), now: time.Now}
}

// startArgs are the arguments of quantize-start. The host adds "tool"; the
// name is the label the host shows in the approval card and the resource list.
// There is no token: a secret never travels through a tool.
type startArgs struct {
	Tool             string `json:"tool"`
	Name             string `json:"name"`
	Model            string `json:"model"`
	Backend          string `json:"backend"`
	QuantizationType string `json:"quantizationType"`
}

// engineStart is the argument object of quantize.start. It has exactly the
// fields the host operation accepts and none of the add-on's own.
type engineStart struct {
	Model            string `json:"model"`
	Backend          string `json:"backend,omitempty"`
	QuantizationType string `json:"quantizationType,omitempty"`
}

type stopArgs struct {
	Tool string `json:"tool"`
}

type importArgs struct {
	Tool string `json:"tool"`
	Name string `json:"name"`
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

// jobOf is the job of the resource, or not found: a tool cannot control a job
// it did not start.
func (r *runtime) jobOf(env contract.Envelope) (job, error) {
	if env.Request.ResourceID == "" {
		return job{}, protocol.ErrNotFound
	}
	j, ok := r.j.job(env.Request.ResourceID)
	if !ok {
		return job{}, protocol.ErrNotFound
	}
	return j, nil
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
		err := r.eng.Do(ctx, op, "quantize.get", map[string]string{"jobId": j.JobID}, nil)
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
		if err := r.eng.Do(ctx, op, "quantize.list", nil, &got); err != nil {
			return contract.Result{}, mapErr(err)
		}
		return contract.Result{Payload: got}, nil
	case ToolStatus:
		j, err := r.jobOf(env)
		if err != nil {
			return contract.Result{}, err
		}
		var got map[string]any
		if err := r.eng.Do(ctx, op, "quantize.get", map[string]string{"jobId": j.JobID}, &got); err != nil {
			return contract.Result{}, mapErr(err)
		}
		// The engine job ID stays in the add-on. The name and the revision
		// are what the host reads to label and track the resource.
		if got == nil {
			got = map[string]any{}
		}
		delete(got, "id")
		got["name"] = j.Name
		got["revision"] = contract.Revision{Schema: schema, Data: j.Revision}
		return contract.Result{Payload: payload(got)}, nil
	}
	return contract.Result{}, protocol.ErrBadRequest
}

func checkStart(a startArgs) error {
	if a.Model == "" || len([]rune(a.Name)) > maxNameRunes || (a.QuantizationType != "" && !quantType.MatchString(a.QuantizationType)) {
		return protocol.ErrBadRequest
	}
	return nil
}

func checkImport(a importArgs) error {
	if a.Name != "" && !modelName.MatchString(a.Name) {
		return protocol.ErrBadRequest
	}
	return nil
}

func nameOf(a startArgs) string {
	if a.Name != "" {
		return a.Name
	}
	return a.Model
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
		if err := checkStart(a); err != nil {
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
		out.Summary = payload(map[string]any{"count": 1, "sample": map[string]string{
			"action": "start quantization job", "name": nameOf(a), "model": a.Model, "quantizationType": a.QuantizationType,
		}})
		return out, nil
	case ToolStop, ToolImport:
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
			sample["action"] = "stop quantization job"
		} else {
			var a importArgs
			if err := decode(env.Request.Arguments, &a); err != nil {
				return out, err
			}
			if err := checkImport(a); err != nil {
				return out, err
			}
			sample["action"], sample["modelName"] = "import quantized model", a.Name
		}
		out.Revision = contract.Revision{Schema: schema, Data: j.Revision}
		out.Summary = payload(map[string]any{"count": 1, "sample": sample})
		return out, nil
	}
	return out, protocol.ErrBadRequest
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
	case ToolStop:
		var a stopArgs
		if err := decode(env.Request.Arguments, &a); err != nil {
			return contract.Receipt{}, err
		}
		return r.mutate(ctx, env, rev, "quantize.stop", func(id string) any {
			return map[string]any{"jobId": id}
		})
	case ToolImport:
		var a importArgs
		if err := decode(env.Request.Arguments, &a); err != nil {
			return contract.Receipt{}, err
		}
		if err := checkImport(a); err != nil {
			return contract.Receipt{}, err
		}
		return r.mutate(ctx, env, rev, "quantize.import", func(id string) any {
			args := map[string]any{"jobId": id}
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
	if err := checkStart(a); err != nil {
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
	err := r.eng.Do(ctx, op, "quantize.start", engineStart{Model: a.Model, Backend: a.Backend, QuantizationType: a.QuantizationType}, &started)
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
