// SPDX-License-Identifier: MIT
package hostops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/core/schema"
)

// Features the operations check for the actor. They are the names the route
// middleware checks (auth.FeatureFineTuning and auth.FeatureQuantization).
const (
	featureFineTuning   = "fine_tuning"
	featureQuantization = "quantization"
)

const (
	maxListed   = 50
	maxRefBytes = 256
)

// FineTuner is the part of the fine-tuning service the operations use. The
// user key is the one the API route passes: empty with authentication off.
type FineTuner interface {
	Start(ctx context.Context, userID string, req schema.FineTuneJobRequest) (*schema.FineTuneJobResponse, error)
	Get(userID, jobID string) (*schema.FineTuneJob, error)
	List(userID string) []*schema.FineTuneJob
	Checkpoints(ctx context.Context, userID, jobID string) ([]Checkpoint, error)
}

// Quantizer is the part of the quantization service the operations use.
type Quantizer interface {
	Start(ctx context.Context, userID string, req schema.QuantizationJobRequest) (*schema.QuantizationJobResponse, error)
	Get(userID, jobID string) (*schema.QuantizationJob, error)
	List(userID string) []*schema.QuantizationJob
}

// BackendLister names the backends of each engine feature that the gallery
// offers. A start operation accepts only these.
type BackendLister interface {
	FineTune() []string
	Quantization() []string
}

// Checkpoint is what the operations need of a saved checkpoint. The path is
// a server path; it is never returned.
type Checkpoint struct {
	Step      int32
	Path      string
	CreatedAt string
}

// RegisterEngineJobs registers the engine job operations. A nil FineTuner
// registers no finetune.* operation, a nil Quantizer no quantize.* operation.
func RegisterEngineJobs(b *Broker, ft FineTuner, q Quantizer, backends BackendLister) error {
	if (ft != nil || q != nil) && backends == nil {
		return errors.New("engine jobs: a backend lister is required")
	}
	if ft != nil {
		for _, s := range fineTuneSpecs(b, ft, backends) {
			if err := b.Register(s); err != nil {
				return err
			}
		}
	}
	if q != nil {
		for _, s := range quantizeSpecs(q, backends) {
			if err := b.Register(s); err != nil {
				return err
			}
		}
	}
	return nil
}

func fineTuneSpecs(b *Broker, ft FineTuner, backends BackendLister) []Spec {
	return []Spec{
		{Op: "finetune.start", Scope: "apply", Feature: featureFineTuning, Idempotent: true, Handler: func(ctx context.Context, c Call) (json.RawMessage, error) {
			var a struct {
				Model          string  `json:"model"`
				Backend        string  `json:"backend"`
				TrainingType   string  `json:"trainingType"`
				TrainingMethod string  `json:"trainingMethod"`
				DatasetSource  string  `json:"datasetSource"`
				DatasetSplit   string  `json:"datasetSplit"`
				LearningRate   float32 `json:"learningRate"`
				NumEpochs      int32   `json:"numEpochs"`
				BatchSize      int32   `json:"batchSize"`
				MaxSteps       int32   `json:"maxSteps"`
				AdapterRank    int32   `json:"adapterRank"`
				AdapterAlpha   int32   `json:"adapterAlpha"`
			}
			if err := decodeArgs(c.Arguments, &a); err != nil {
				return nil, err
			}
			if !validRef(a.Model) {
				return nil, refusedField("model")
			}
			if datasetRefShape.MatchString(a.DatasetSource) {
				path, ok := b.resolveDataset(userKey(c), a.DatasetSource)
				if !ok {
					return nil, fmt.Errorf("%w: datasetSource is not a dataset reference of this user", ErrBadRequest)
				}
				a.DatasetSource = path
			} else if !datasetID(a.DatasetSource) {
				return nil, refusedField("datasetSource")
			}
			if a.LearningRate < 0 || a.NumEpochs < 0 || a.BatchSize < 0 || a.MaxSteps < 0 || a.AdapterRank < 0 || a.AdapterAlpha < 0 {
				return nil, fmt.Errorf("%w: numeric arguments must not be negative", ErrBadRequest)
			}
			backend := a.Backend
			if backend == "" {
				backend = "trl"
			}
			if !contains(backends.FineTune(), backend) {
				return nil, fmt.Errorf("%w: backend is not a fine-tuning backend", ErrBadRequest)
			}
			resp, err := ft.Start(ctx, userKey(c), schema.FineTuneJobRequest{
				Model: a.Model, Backend: backend, TrainingType: a.TrainingType, TrainingMethod: a.TrainingMethod,
				DatasetSource: a.DatasetSource, DatasetSplit: a.DatasetSplit, LearningRate: a.LearningRate,
				NumEpochs: a.NumEpochs, BatchSize: a.BatchSize, MaxSteps: a.MaxSteps,
				AdapterRank: a.AdapterRank, AdapterAlpha: a.AdapterAlpha,
			})
			if err != nil {
				return nil, err
			}
			return marshal(struct {
				ID      string `json:"id"`
				Status  string `json:"status"`
				Message string `json:"message,omitempty"`
			}{resp.ID, resp.Status, scrub(resp.Message)})
		}},
		{Op: "finetune.get", Scope: "read", Feature: featureFineTuning, Handler: func(_ context.Context, c Call) (json.RawMessage, error) {
			id, err := jobIDArg(c.Arguments)
			if err != nil {
				return nil, err
			}
			j, err := ft.Get(userKey(c), id)
			if err != nil {
				return nil, err
			}
			return marshal(fineTuneView(j))
		}},
		{Op: "finetune.list", Scope: "read", Feature: featureFineTuning, Handler: func(_ context.Context, c Call) (json.RawMessage, error) {
			if err := decodeArgs(c.Arguments, &struct{}{}); err != nil {
				return nil, err
			}
			jobs := ft.List(userKey(c))
			out := make([]any, 0, min(len(jobs), maxListed))
			for _, j := range jobs[:min(len(jobs), maxListed)] {
				out = append(out, fineTuneView(j))
			}
			return marshal(map[string]any{"jobs": out})
		}},
		{Op: "finetune.checkpoints", Scope: "read", Feature: featureFineTuning, Handler: func(ctx context.Context, c Call) (json.RawMessage, error) {
			id, err := jobIDArg(c.Arguments)
			if err != nil {
				return nil, err
			}
			cps, err := ft.Checkpoints(ctx, userKey(c), id)
			if err != nil {
				return nil, err
			}
			type view struct {
				Step      int32  `json:"step"`
				CreatedAt string `json:"createdAt,omitempty"`
			}
			out := make([]view, 0, len(cps))
			for _, cp := range cps {
				out = append(out, view{Step: cp.Step, CreatedAt: cp.CreatedAt})
			}
			return marshal(map[string]any{"checkpoints": out})
		}},
	}
}

func quantizeSpecs(q Quantizer, backends BackendLister) []Spec {
	return []Spec{
		{Op: "quantize.start", Scope: "apply", Feature: featureQuantization, Idempotent: true, Handler: func(ctx context.Context, c Call) (json.RawMessage, error) {
			var a struct {
				Model            string `json:"model"`
				Backend          string `json:"backend"`
				QuantizationType string `json:"quantizationType"`
			}
			if err := decodeArgs(c.Arguments, &a); err != nil {
				return nil, err
			}
			if !validRef(a.Model) {
				return nil, refusedField("model")
			}
			if a.QuantizationType != "" && !quantType.MatchString(a.QuantizationType) {
				return nil, fmt.Errorf("%w: quantizationType is not a quantization name", ErrBadRequest)
			}
			backend := a.Backend
			if backend == "" {
				backend = "llama-cpp-quantization"
			}
			if !contains(backends.Quantization(), backend) {
				return nil, fmt.Errorf("%w: backend is not a quantization backend", ErrBadRequest)
			}
			resp, err := q.Start(ctx, userKey(c), schema.QuantizationJobRequest{
				Model: a.Model, Backend: backend, QuantizationType: a.QuantizationType,
			})
			if err != nil {
				return nil, err
			}
			return marshal(struct {
				ID      string `json:"id"`
				Status  string `json:"status"`
				Message string `json:"message,omitempty"`
			}{resp.ID, resp.Status, scrub(resp.Message)})
		}},
		{Op: "quantize.get", Scope: "read", Feature: featureQuantization, Handler: func(_ context.Context, c Call) (json.RawMessage, error) {
			id, err := jobIDArg(c.Arguments)
			if err != nil {
				return nil, err
			}
			j, err := q.Get(userKey(c), id)
			if err != nil {
				return nil, err
			}
			return marshal(quantizeView(j))
		}},
		{Op: "quantize.list", Scope: "read", Feature: featureQuantization, Handler: func(_ context.Context, c Call) (json.RawMessage, error) {
			if err := decodeArgs(c.Arguments, &struct{}{}); err != nil {
				return nil, err
			}
			jobs := q.List(userKey(c))
			out := make([]any, 0, min(len(jobs), maxListed))
			for _, j := range jobs[:min(len(jobs), maxListed)] {
				out = append(out, quantizeView(j))
			}
			return marshal(map[string]any{"jobs": out})
		}},
	}
}

// fineTuneView is the projection a result carries. It has no path field.
func fineTuneView(j *schema.FineTuneJob) map[string]any {
	v := map[string]any{
		"id": j.ID, "status": j.Status, "message": scrub(j.Message), "model": j.Model,
		"backend": j.Backend, "trainingType": j.TrainingType, "trainingMethod": j.TrainingMethod,
		"createdAt": j.CreatedAt, "exported": j.ExportStatus == "completed",
	}
	if j.ExportModelName != "" {
		v["exportedModelName"] = j.ExportModelName
	}
	return v
}

// quantizeView is the projection a result carries. It has no path field.
func quantizeView(j *schema.QuantizationJob) map[string]any {
	v := map[string]any{
		"id": j.ID, "status": j.Status, "message": scrub(j.Message), "model": j.Model,
		"backend": j.Backend, "quantizationType": j.QuantizationType, "createdAt": j.CreatedAt,
		"imported": j.ImportStatus == "completed",
	}
	if j.ImportModelName != "" {
		v["importedModelName"] = j.ImportModelName
	}
	return v
}

// userKey is the user the API route would pass. With authentication off the
// route passes "", so a tool-started job shows in the UI list of the same user.
func userKey(c Call) string {
	if c.Actor.Mode == "local-owner" {
		return ""
	}
	return c.Actor.UserID
}

// decodeArgs decodes into a closed struct: an unknown field or trailing data
// is a bad request.
func decodeArgs(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %s", ErrBadRequest, err.Error())
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("%w: trailing data", ErrBadRequest)
	}
	return nil
}

func jobIDArg(raw json.RawMessage) (string, error) {
	var a struct {
		JobID string `json:"jobId"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return "", err
	}
	if err := checkJobID(a.JobID); err != nil {
		return "", err
	}
	return a.JobID, nil
}

// validRef accepts a gallery or Hugging Face id. A local path is refused: a
// person who needs one uses the UI.
func validRef(s string) bool {
	return s != "" && len(s) <= maxRefBytes && s[0] != '/' && s[0] != '.' &&
		!strings.Contains(s, "..") && !strings.Contains(s, `\`)
}

// refusedField is the 400 answer for a reference field that is not an id. It
// names the field, so the add-on can tell the model what to fix.
func refusedField(field string) error {
	return fmt.Errorf("%w: %s must be a gallery or Hugging Face id", ErrBadRequest, field)
}

// datasetID accepts a gallery or Hugging Face dataset id: a name, or an owner
// and a name joined by one slash. A URL, a scheme, a path, a space or a parent
// segment does not match.
func datasetID(s string) bool {
	return len(s) <= maxRefBytes && datasetIDPattern.MatchString(s)
}

var datasetIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9][A-Za-z0-9._-]*)?$`)

var quantType = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// absPath matches a server path inside free text, such as an engine message.
var absPath = regexp.MustCompile(`(^|[\s'"(=])/[^\s'"),]*`)

// scrub replaces server paths in engine messages, so a result never carries
// one even inside free text.
func scrub(s string) string { return absPath.ReplaceAllString(s, "${1}[path]") }

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func marshal(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	return b, err
}
