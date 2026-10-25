// SPDX-License-Identifier: MIT
package localai

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/finetune"
	"github.com/mudler/LocalAI/core/services/quantization"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// engineBackendInfo is one backend that the fine-tuning and quantization
// backend endpoints list.
type engineBackendInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// listTaggedBackends returns the installed gallery backends that carry tag. The
// backend endpoints and the add-on host operations share it, so both answer
// with the same list.
func listTaggedBackends(ctx context.Context, appConfig *config.ApplicationConfig, clusterCapabilities ClusterCapabilityProvider, clusterInstalled ClusterInstalledProvider, tag string) ([]engineBackendInfo, error) {
	capabilities := ResolveClusterCapabilities(ctx, clusterCapabilities)
	installed := resolveClusterInstalled(ctx, clusterInstalled)
	backends, err := gallery.AvailableBackendsForCapabilities(appConfig.BackendGalleries, appConfig.SystemState, capabilities)
	if err != nil {
		return nil, err
	}
	result := []engineBackendInfo{}
	for _, b := range backends {
		if !installedInCluster(b, installed) {
			continue
		}
		hasTag := false
		for _, t := range b.Tags {
			if strings.EqualFold(t, tag) {
				hasTag = true
				break
			}
		}
		if !hasTag {
			continue
		}
		name := b.Name
		if b.Alias != "" {
			name = b.Alias
		}
		result = append(result, engineBackendInfo{Name: name, Description: b.Description, Tags: b.Tags})
	}
	return result, nil
}

// EngineBackends names the fine-tuning and quantization backends the host
// offers, for the add-on host operations. A lookup that fails names none, so
// a start operation then refuses.
type EngineBackends struct {
	AppConfig    *config.ApplicationConfig
	Capabilities ClusterCapabilityProvider
	Installed    ClusterInstalledProvider
}

var _ hostops.BackendLister = EngineBackends{}

func (e EngineBackends) FineTune() []string { return e.names("fine-tuning") }

func (e EngineBackends) Quantization() []string { return e.names("quantization") }

func (e EngineBackends) names(tag string) []string {
	// The interface has no request context, so the lookup gets its own bound.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	infos, err := listTaggedBackends(ctx, e.AppConfig, e.Capabilities, e.Installed, tag)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(infos))
	for _, i := range infos {
		out = append(out, i.Name)
	}
	return out
}

// FineTuneJobs adapts the fine-tuning service to the add-on host operations.
type FineTuneJobs struct{ Svc *finetune.FineTuneService }

var _ hostops.FineTuner = FineTuneJobs{}

func (f FineTuneJobs) Start(ctx context.Context, userID string, req schema.FineTuneJobRequest) (*schema.FineTuneJobResponse, error) {
	return f.Svc.StartJob(ctx, userID, req)
}

// Get maps every failure to not found: the service has no other answer for a
// job the user does not hold.
func (f FineTuneJobs) Get(userID, jobID string) (*schema.FineTuneJob, error) {
	j, err := f.Svc.GetJob(userID, jobID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", hostops.ErrNotFound, err)
	}
	return j, nil
}

func (f FineTuneJobs) List(userID string) []*schema.FineTuneJob { return f.Svc.ListJobs(userID) }

func (f FineTuneJobs) Checkpoints(ctx context.Context, userID, jobID string) ([]hostops.Checkpoint, error) {
	if _, err := f.Get(userID, jobID); err != nil {
		return nil, err
	}
	cps, err := f.Svc.ListCheckpoints(ctx, userID, jobID)
	if err != nil {
		return nil, err
	}
	out := make([]hostops.Checkpoint, 0, len(cps))
	for _, cp := range cps {
		out = append(out, hostops.Checkpoint{Step: cp.Step, Path: cp.Path, CreatedAt: cp.CreatedAt})
	}
	return out, nil
}

// QuantizationJobs adapts the quantization service to the add-on host operations.
type QuantizationJobs struct {
	Svc *quantization.QuantizationService
}

var _ hostops.Quantizer = QuantizationJobs{}

func (q QuantizationJobs) Start(ctx context.Context, userID string, req schema.QuantizationJobRequest) (*schema.QuantizationJobResponse, error) {
	return q.Svc.StartJob(ctx, userID, req)
}

// Get maps every failure to not found, as FineTuneJobs.Get does.
func (q QuantizationJobs) Get(userID, jobID string) (*schema.QuantizationJob, error) {
	j, err := q.Svc.GetJob(userID, jobID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", hostops.ErrNotFound, err)
	}
	return j, nil
}

func (q QuantizationJobs) List(userID string) []*schema.QuantizationJob {
	return q.Svc.ListJobs(userID)
}

// FineTuneProgress adapts the fine-tuning service to the progress and stop
// operations. A job the user does not hold is not found, as in FineTuneJobs.
type FineTuneProgress struct{ Svc *finetune.FineTuneService }

var _ hostops.ProgressSource = FineTuneProgress{}

func (f FineTuneProgress) StreamProgress(ctx context.Context, userID, jobID string, cb func(hostops.Progress)) error {
	return f.Svc.StreamProgress(ctx, userID, jobID, func(ev *schema.FineTuneProgressEvent) {
		cb(hostops.Progress{JobID: ev.JobID, Status: ev.Status, Message: ev.Message, Percent: ev.ProgressPercent})
	})
}

// Snapshot carries no percent: a job keeps none between events.
func (f FineTuneProgress) Snapshot(userID, jobID string) (hostops.Progress, error) {
	j, err := f.Svc.GetJob(userID, jobID)
	if err != nil {
		return hostops.Progress{}, fmt.Errorf("%w: %w", hostops.ErrNotFound, err)
	}
	return hostops.Progress{JobID: j.ID, Status: j.Status, Message: j.Message}, nil
}

func (f FineTuneProgress) StopJob(ctx context.Context, userID, jobID string, saveCheckpoint bool) error {
	return f.Svc.StopJob(ctx, userID, jobID, saveCheckpoint)
}

// QuantizationProgress adapts the quantization service to the progress and
// stop operations. A quantization job keeps no checkpoint, so saveCheckpoint
// is ignored.
type QuantizationProgress struct {
	Svc *quantization.QuantizationService
}

var _ hostops.ProgressSource = QuantizationProgress{}

func (q QuantizationProgress) StreamProgress(ctx context.Context, userID, jobID string, cb func(hostops.Progress)) error {
	return q.Svc.StreamProgress(ctx, userID, jobID, func(ev *schema.QuantizationProgressEvent) {
		cb(hostops.Progress{JobID: ev.JobID, Status: ev.Status, Message: ev.Message, Percent: ev.ProgressPercent})
	})
}

// Snapshot carries no percent: a job keeps none between events.
func (q QuantizationProgress) Snapshot(userID, jobID string) (hostops.Progress, error) {
	j, err := q.Svc.GetJob(userID, jobID)
	if err != nil {
		return hostops.Progress{}, fmt.Errorf("%w: %w", hostops.ErrNotFound, err)
	}
	return hostops.Progress{JobID: j.ID, Status: j.Status, Message: j.Message}, nil
}

func (q QuantizationProgress) StopJob(ctx context.Context, userID, jobID string, _ bool) error {
	return q.Svc.StopJob(ctx, userID, jobID)
}

// fineTuneModelJobs is the part of the fine-tuning service the model
// registration adapter uses. The service implements it.
type fineTuneModelJobs interface {
	GetJob(userID, jobID string) (*schema.FineTuneJob, error)
	ListCheckpoints(ctx context.Context, userID, jobID string) ([]*pb.CheckpointInfo, error)
	ExportModel(ctx context.Context, userID, jobID string, req schema.ExportRequest) (string, error)
}

// FineTuneModels adapts the fine-tuning service to the model registration
// operation. A job the user does not hold is not found, and a job with an
// export already running is a conflict. The service reports both as plain
// errors, so they are checked here first.
type FineTuneModels struct{ Svc fineTuneModelJobs }

var _ hostops.Exporter = FineTuneModels{}

func (f FineTuneModels) job(userID, jobID string) (*schema.FineTuneJob, error) {
	j, err := f.Svc.GetJob(userID, jobID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", hostops.ErrNotFound, err)
	}
	return j, nil
}

// Checkpoints lists the checkpoints of a job the user holds. The path is the
// server path that the export reads; the host never returns it.
func (f FineTuneModels) Checkpoints(ctx context.Context, userID, jobID string) ([]hostops.Checkpoint, error) {
	if _, err := f.job(userID, jobID); err != nil {
		return nil, err
	}
	cps, err := f.Svc.ListCheckpoints(ctx, userID, jobID)
	if err != nil {
		return nil, err
	}
	out := make([]hostops.Checkpoint, 0, len(cps))
	for _, cp := range cps {
		out = append(out, hostops.Checkpoint{Step: cp.Step, Path: cp.Path, CreatedAt: cp.CreatedAt})
	}
	return out, nil
}

func (f FineTuneModels) ExportModel(ctx context.Context, userID, jobID string, req schema.ExportRequest) (string, error) {
	j, err := f.job(userID, jobID)
	if err != nil {
		return "", err
	}
	if j.ExportStatus == "exporting" {
		return "", fmt.Errorf("%w: an export of this job is running", hostops.ErrConflict)
	}
	return f.Svc.ExportModel(ctx, userID, jobID, req)
}

// quantizationModelJobs is the part of the quantization service the model
// registration adapter uses. The service implements it.
type quantizationModelJobs interface {
	GetJob(userID, jobID string) (*schema.QuantizationJob, error)
	ImportModel(ctx context.Context, userID, jobID string, req schema.QuantizationImportRequest) (string, error)
}

// QuantizationModels adapts the quantization service to the model registration
// operation. A job the user does not hold is not found, and a job that has not
// completed, or whose import is running, is a conflict.
type QuantizationModels struct{ Svc quantizationModelJobs }

var _ hostops.Importer = QuantizationModels{}

func (q QuantizationModels) ImportModel(ctx context.Context, userID, jobID string, req schema.QuantizationImportRequest) (string, error) {
	j, err := q.Svc.GetJob(userID, jobID)
	if err != nil {
		return "", fmt.Errorf("%w: %w", hostops.ErrNotFound, err)
	}
	if j.Status != "completed" {
		return "", fmt.Errorf("%w: the job is not completed", hostops.ErrConflict)
	}
	if j.ImportStatus == "importing" {
		return "", fmt.Errorf("%w: an import of this job is running", hostops.ErrConflict)
	}
	return q.Svc.ImportModel(ctx, userID, jobID, req)
}

// maxDatasetRefs bounds the paths the adapter keeps. The host forgets a
// reference at the same bound, so an older one resolves to nothing.
const maxDatasetRefs = 256

// FineTuneDatasets stores an inline dataset through the fine-tuning service and
// keeps the path of each reference it issued, for the host to resolve.
type FineTuneDatasets struct {
	Svc   *finetune.FineTuneService
	mu    sync.Mutex
	paths map[string]string
	order []string
}

var _ hostops.DatasetStore = (*FineTuneDatasets)(nil)

// NewFineTuneDatasets builds the dataset store over the fine-tuning service.
func NewFineTuneDatasets(svc *finetune.FineTuneService) *FineTuneDatasets {
	return &FineTuneDatasets{Svc: svc, paths: map[string]string{}}
}

// Put stores the dataset and returns its file name, which carries no directory.
func (f *FineTuneDatasets) Put(name string, data []byte) (string, error) {
	path, err := f.Svc.UploadDataset(name, data)
	if err != nil {
		return "", err
	}
	ref := filepath.Base(path)
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.paths[ref]; !ok {
		f.order = append(f.order, ref)
	}
	f.paths[ref] = path
	for len(f.order) > maxDatasetRefs {
		delete(f.paths, f.order[0])
		f.order = f.order[1:]
	}
	return ref, nil
}

// Resolve answers the stored path of a reference this adapter issued.
func (f *FineTuneDatasets) Resolve(ref string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.paths[ref]
	return p, ok
}
