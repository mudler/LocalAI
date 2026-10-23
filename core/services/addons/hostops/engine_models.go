// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/core/schema"
)

// Exporter is the part of the fine-tuning service the export operation uses.
// The host writes the model config from the job's own output; the path the
// service returns is read only for its base name.
type Exporter interface {
	// Checkpoints lists the checkpoints of the job, as the user sees them.
	Checkpoints(ctx context.Context, userID, jobID string) ([]Checkpoint, error)
	ExportModel(ctx context.Context, userID, jobID string, req schema.ExportRequest) (string, error)
}

// Importer is the part of the quantization service the import operation uses.
type Importer interface {
	ImportModel(ctx context.Context, userID, jobID string, req schema.QuantizationImportRequest) (string, error)
}

// modelName is the shape of a model name an add-on may choose.
var modelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// exportFormats are the values of ExportRequest.ExportFormat that the
// fine-tuning docs list. Any other format is not reachable from a tool.
var exportFormats = map[string]bool{"lora": true, "merged_16bit": true, "merged_4bit": true, "gguf": true}

// RegisterEngineModels registers the model registration operations. A nil
// Exporter registers no finetune.export operation, a nil Importer no
// quantize.import operation.
func RegisterEngineModels(b *Broker, ex Exporter, im Importer) error {
	if ex != nil {
		if err := b.Register(exportSpec(ex)); err != nil {
			return err
		}
	}
	if im != nil {
		if err := b.Register(importSpec(im)); err != nil {
			return err
		}
	}
	return nil
}

func exportSpec(ex Exporter) Spec {
	return Spec{Op: "finetune.export", Scope: "apply", Feature: featureFineTuning, Idempotent: true, Admin: true, Handler: func(ctx context.Context, c Call) (json.RawMessage, error) {
		var a struct {
			JobID        string `json:"jobId"`
			Name         string `json:"name"`
			ExportFormat string `json:"exportFormat"`
		}
		if err := decodeArgs(c.Arguments, &a); err != nil {
			return nil, err
		}
		if err := checkJobID(a.JobID); err != nil {
			return nil, err
		}
		name, err := optionalName(a.Name)
		if err != nil {
			return nil, err
		}
		if a.ExportFormat != "" && !exportFormats[a.ExportFormat] {
			return nil, fmt.Errorf("%w: exportFormat is not an export format", ErrBadRequest)
		}
		// The export works from one checkpoint of the job. The service never
		// picks one, so the host takes the latest, and refuses a job with none.
		cps, err := ex.Checkpoints(ctx, userKey(c), a.JobID)
		if err != nil {
			return nil, err
		}
		ckpt, ok := latestCheckpoint(cps)
		if !ok {
			return nil, fmt.Errorf("%w: the job has no checkpoint to export; a checkpoint is saved when the job runs", ErrConflict)
		}
		path, err := ex.ExportModel(ctx, userKey(c), a.JobID, schema.ExportRequest{Name: name, ExportFormat: a.ExportFormat, CheckpointPath: ckpt.Path})
		if err != nil {
			return nil, err
		}
		return modelResult(path)
	}}
}

func importSpec(im Importer) Spec {
	return Spec{Op: "quantize.import", Scope: "apply", Feature: featureQuantization, Idempotent: true, Admin: true, Handler: func(ctx context.Context, c Call) (json.RawMessage, error) {
		var a struct {
			JobID string `json:"jobId"`
			Name  string `json:"name"`
		}
		if err := decodeArgs(c.Arguments, &a); err != nil {
			return nil, err
		}
		if err := checkJobID(a.JobID); err != nil {
			return nil, err
		}
		name, err := optionalName(a.Name)
		if err != nil {
			return nil, err
		}
		path, err := im.ImportModel(ctx, userKey(c), a.JobID, schema.QuantizationImportRequest{Name: name})
		if err != nil {
			return nil, err
		}
		return modelResult(path)
	}}
}

// optionalName returns the trimmed name, or "" when none was given. A name
// that is given but blank, or does not match the pattern, is refused.
func optionalName(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	name := strings.TrimSpace(raw)
	if !modelName.MatchString(name) {
		return "", fmt.Errorf("%w: name must be 1 to 64 letters, digits, dots, dashes or underscores", ErrBadRequest)
	}
	return name, nil
}

// modelResult projects the service path to the model name: its base name,
// and only when that passes the name pattern. The path never leaves the host.
// A path that does not project is an outage, not a refusal.
func modelResult(path string) (json.RawMessage, error) {
	name := filepath.Base(path)
	if !modelName.MatchString(name) {
		return nil, errors.New("registered model has no usable name")
	}
	return marshal(map[string]string{"model": name})
}

// latestCheckpoint returns the checkpoint with the highest step that has a
// path. The path stays on the host; no result carries it.
func latestCheckpoint(cps []Checkpoint) (Checkpoint, bool) {
	var best Checkpoint
	found := false
	for _, cp := range cps {
		if cp.Path == "" {
			continue
		}
		if !found || cp.Step > best.Step {
			best, found = cp, true
		}
	}
	return best, found
}
