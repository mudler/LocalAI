package quantization

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery/importers"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/syncstate"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
	"gopkg.in/yaml.v3"
)

// QuantizationService manages quantization jobs and their lifecycle.
type QuantizationService struct {
	appConfig    *config.ApplicationConfig
	modelLoader  *model.ModelLoader
	configLoader *config.ModelConfigLoader

	// mu serializes the read-modify-write of job values. The SyncedMap guards its
	// own map structure, but a job is a pointer mutated in place (e.g. the import
	// goroutine), so the service still needs a lock to keep those field updates and
	// the subsequent Set atomic with respect to readers.
	mu sync.Mutex

	// jobs is the cross-replica job store: an in-memory map kept consistent across
	// replicas via NATS, optionally read-through to PostgreSQL in distributed mode.
	jobs *syncstate.SyncedMap[string, *schema.QuantizationJob]

	// progressMu guards progressSubs.
	//
	// A backend's per-job progress stream has a single destructive consumer: the
	// backend pops each update off one queue and hands it to whoever is reading.
	// So the service opens that stream exactly once per job — in watchProgress,
	// started by StartJob — and fans the updates out in-process to the SSE clients
	// registered here. Opening a second stream per client would make the two
	// readers race for the same updates.
	progressMu   sync.Mutex
	progressSubs map[string][]chan *schema.QuantizationProgressEvent
}

// progressSubBuffer is the per-subscriber event buffer. It absorbs a client that
// is briefly slow; a client that falls further behind drops events rather than
// stalling the single reader of the backend stream.
const progressSubBuffer = 64

// isTerminalStatus reports whether a job status is final, i.e. no further
// progress update will follow.
func isTerminalStatus(status string) bool {
	return status == "stopped" || status == "completed" || status == "failed"
}

// NewQuantizationService creates a new QuantizationService. In distributed mode
// pass the shared NATS client and PostgreSQL store so jobs stay consistent across
// replicas; pass nil for both in standalone mode, where the disk Loader hydrates
// the map and there is nothing to broadcast.
func NewQuantizationService(
	appConfig *config.ApplicationConfig,
	modelLoader *model.ModelLoader,
	configLoader *config.ModelConfigLoader,
	nats messaging.MessagingClient,
	store *distributed.QuantStore,
) *QuantizationService {
	s := &QuantizationService{
		appConfig:    appConfig,
		modelLoader:  modelLoader,
		configLoader: configLoader,
		progressSubs: make(map[string][]chan *schema.QuantizationProgressEvent),
	}

	// Only attach a Store interface when a concrete store exists, otherwise the
	// SyncedMap would see a non-nil interface wrapping a nil pointer and try to
	// hydrate/write through a nil DB.
	var syncStore syncstate.Store[string, *schema.QuantizationJob]
	if store != nil {
		syncStore = &quantStoreAdapter{store: store}
	}

	s.jobs = syncstate.New(syncstate.Config[string, *schema.QuantizationJob]{
		Name:   "quant.jobs",
		Key:    func(j *schema.QuantizationJob) string { return j.ID },
		Nats:   nats,
		Store:  syncStore,
		Loader: s.loadJobsFromDisk, // ignored when Store is set (distributed mode)
	})

	// Hydrate + subscribe. A hydrate failure must not take the server down: log and
	// continue degraded (standalone), mirroring the FineTune/OpCache wiring.
	if err := s.jobs.Start(appConfig.Context); err != nil {
		xlog.Warn("Quantization SyncedMap start failed; running degraded", "error", err)
	}
	return s
}

// Close releases the SyncedMap subscription and background workers.
func (s *QuantizationService) Close() error {
	return s.jobs.Close()
}

// quantizationBaseDir returns the base directory for quantization job data.
func (s *QuantizationService) quantizationBaseDir() string {
	return filepath.Join(s.appConfig.DataPath, "quantization")
}

// jobDir returns the directory for a specific job.
func (s *QuantizationService) jobDir(jobID string) string {
	return filepath.Join(s.quantizationBaseDir(), jobID)
}

// saveJobState persists a job's state to disk as state.json.
func (s *QuantizationService) saveJobState(job *schema.QuantizationJob) {
	dir := s.jobDir(job.ID)
	if err := os.MkdirAll(dir, 0750); err != nil {
		xlog.Error("Failed to create quantization job directory", "job_id", job.ID, "error", err)
		return
	}

	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		xlog.Error("Failed to marshal quantization job state", "job_id", job.ID, "error", err)
		return
	}

	statePath := filepath.Join(dir, "state.json")
	if err := os.WriteFile(statePath, data, 0640); err != nil {
		xlog.Error("Failed to write quantization job state", "job_id", job.ID, "error", err)
	}
}

// loadJobsFromDisk scans the quantization directory for persisted jobs and
// returns them. It is the SyncedMap Loader used in standalone mode (no DB); the
// returned slice hydrates the map on Start.
func (s *QuantizationService) loadJobsFromDisk(_ context.Context) ([]*schema.QuantizationJob, error) {
	baseDir := s.quantizationBaseDir()
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		// Directory doesn't exist yet — that's fine, start empty.
		return nil, nil
	}

	var jobs []*schema.QuantizationJob
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		statePath := filepath.Join(baseDir, entry.Name(), "state.json")
		data, err := os.ReadFile(statePath)
		if err != nil {
			continue
		}

		var job schema.QuantizationJob
		if err := json.Unmarshal(data, &job); err != nil {
			xlog.Warn("Failed to parse quantization job state", "path", statePath, "error", err)
			continue
		}

		// Jobs that were running when we shut down are now stale
		if job.Status == "queued" || job.Status == "downloading" || job.Status == "converting" || job.Status == "quantizing" {
			job.Status = "stopped"
			job.Message = "Server restarted while job was running"
		}

		// Imports that were in progress are now stale
		if job.ImportStatus == "importing" {
			job.ImportStatus = "failed"
			job.ImportMessage = "Server restarted while import was running"
		}

		jobs = append(jobs, &job)
	}

	if len(jobs) > 0 {
		xlog.Info("Loaded persisted quantization jobs", "count", len(jobs))
	}
	return jobs, nil
}

// StartJob starts a new quantization job.
func (s *QuantizationService) StartJob(ctx context.Context, userID string, req schema.QuantizationJobRequest) (*schema.QuantizationJobResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobID := uuid.New().String()

	backendName := req.Backend
	if backendName == "" {
		backendName = "llama-cpp-quantization"
	}

	quantType := req.QuantizationType
	if quantType == "" {
		quantType = "q4_k_m"
	}

	// Always use DataPath for output — not user-configurable
	outputDir := filepath.Join(s.quantizationBaseDir(), jobID)

	// Build gRPC request
	grpcReq := &pb.QuantizationRequest{
		Model:            req.Model,
		QuantizationType: quantType,
		OutputDir:        outputDir,
		JobId:            jobID,
		ExtraOptions:     req.ExtraOptions,
	}

	// Load the quantization backend (per-job model ID so multiple jobs can run concurrently)
	modelID := backendName + "-quantize-" + jobID
	backendModel, err := s.modelLoader.Load(
		model.WithBackendString(backendName),
		model.WithModel(backendName),
		model.WithModelID(modelID),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load backend %s: %w", backendName, err)
	}

	// Start quantization via gRPC
	result, err := backendModel.StartQuantization(ctx, grpcReq)
	if err != nil {
		return nil, fmt.Errorf("failed to start quantization: %w", err)
	}
	if !result.Success {
		return nil, fmt.Errorf("quantization failed to start: %s", result.Message)
	}

	// Track the job
	job := &schema.QuantizationJob{
		ID:               jobID,
		UserID:           userID,
		Model:            req.Model,
		Backend:          backendName,
		ModelID:          modelID,
		QuantizationType: quantType,
		Status:           "queued",
		OutputDir:        outputDir,
		ExtraOptions:     req.ExtraOptions,
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
		Config:           &req,
	}
	// Set write-through persists to PostgreSQL (distributed) and broadcasts to
	// peer replicas; the disk state.json is written separately for restart
	// recovery / standalone hydrate.
	if err := s.jobs.Set(ctx, job); err != nil {
		return nil, fmt.Errorf("failed to persist job: %w", err)
	}
	s.saveJobState(job)

	// Consume the backend's progress stream for the lifetime of the job, not for
	// the lifetime of a client's SSE connection: a job that runs with nobody
	// attached must still reach "completed" in the store and in state.json. The
	// request ctx is done as soon as this HTTP handler returns, so the watcher
	// rides the application context instead.
	go s.watchProgress(s.appConfig.Context, jobID, backendName, modelID)

	return &schema.QuantizationJobResponse{
		ID:      jobID,
		Status:  "queued",
		Message: result.Message,
	}, nil
}

// GetJob returns a quantization job by ID.
func (s *QuantizationService) GetJob(userID, jobID string) (*schema.QuantizationJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.jobs.Get(jobID)
	if !ok {
		return nil, fmt.Errorf("job not found: %s", jobID)
	}
	if userID != "" && job.UserID != userID {
		return nil, fmt.Errorf("job not found: %s", jobID)
	}
	return job, nil
}

// ListJobs returns all jobs for a user, sorted by creation time (newest first).
func (s *QuantizationService) ListJobs(userID string) []*schema.QuantizationJob {
	s.mu.Lock()
	defer s.mu.Unlock()

	var result []*schema.QuantizationJob
	for _, job := range s.jobs.List() {
		if userID == "" || job.UserID == userID {
			result = append(result, job)
		}
	}

	slices.SortFunc(result, func(a, b *schema.QuantizationJob) int {
		return cmp.Compare(b.CreatedAt, a.CreatedAt)
	})

	return result
}

// StopJob stops a running quantization job.
func (s *QuantizationService) StopJob(ctx context.Context, userID, jobID string) error {
	s.mu.Lock()
	job, ok := s.jobs.Get(jobID)
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("job not found: %s", jobID)
	}
	if userID != "" && job.UserID != userID {
		s.mu.Unlock()
		return fmt.Errorf("job not found: %s", jobID)
	}
	s.mu.Unlock()

	// Kill the backend process directly
	stopModelID := job.ModelID
	if stopModelID == "" {
		stopModelID = job.Backend + "-quantize"
	}
	s.modelLoader.ShutdownModel(stopModelID)

	s.mu.Lock()
	job.Status = "stopped"
	job.Message = "Quantization stopped by user"
	if err := s.jobs.Set(ctx, job); err != nil {
		xlog.Warn("Failed to persist stopped job", "job_id", jobID, "error", err)
	}
	s.saveJobState(job)
	s.mu.Unlock()

	// Release clients attached to the progress stream: the backend process is gone,
	// so the watcher will not see a terminal update to forward.
	s.publishProgress(jobID, &schema.QuantizationProgressEvent{
		JobID:   jobID,
		Status:  "stopped",
		Message: "Quantization stopped by user",
	})

	return nil
}

// DeleteJob removes a quantization job and its associated data from disk.
func (s *QuantizationService) DeleteJob(userID, jobID string) error {
	s.mu.Lock()
	job, ok := s.jobs.Get(jobID)
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("job not found: %s", jobID)
	}
	if userID != "" && job.UserID != userID {
		s.mu.Unlock()
		return fmt.Errorf("job not found: %s", jobID)
	}

	// Reject deletion of actively running jobs
	activeStatuses := map[string]bool{
		"queued": true, "downloading": true, "converting": true, "quantizing": true,
	}
	if activeStatuses[job.Status] {
		s.mu.Unlock()
		return fmt.Errorf("cannot delete job %s: currently %s (stop it first)", jobID, job.Status)
	}
	if job.ImportStatus == "importing" {
		s.mu.Unlock()
		return fmt.Errorf("cannot delete job %s: import in progress", jobID)
	}

	importModelName := job.ImportModelName
	// Delete write-through removes the DB row (distributed) and broadcasts the
	// removal to peer replicas. DeleteJob has no ctx, so use Background.
	if err := s.jobs.Delete(context.Background(), jobID); err != nil {
		xlog.Warn("Failed to delete job from store", "job_id", jobID, "error", err)
	}
	s.mu.Unlock()

	// Remove job directory (state.json, output files)
	jobDir := s.jobDir(jobID)
	if err := os.RemoveAll(jobDir); err != nil {
		xlog.Warn("Failed to remove quantization job directory", "job_id", jobID, "path", jobDir, "error", err)
	}

	// If an imported model exists, clean it up too
	if importModelName != "" {
		modelsPath := s.appConfig.SystemState.Model.ModelsPath
		modelDir := filepath.Join(modelsPath, importModelName)
		configPath := filepath.Join(modelsPath, importModelName+".yaml")

		if err := os.RemoveAll(modelDir); err != nil {
			xlog.Warn("Failed to remove imported model directory", "path", modelDir, "error", err)
		}
		if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
			xlog.Warn("Failed to remove imported model config", "path", configPath, "error", err)
		}

		// Reload model configs
		if err := s.configLoader.LoadModelConfigsFromPath(modelsPath, s.appConfig.ToConfigLoaderOptions()...); err != nil {
			xlog.Warn("Failed to reload configs after delete", "error", err)
		}
	}

	xlog.Info("Deleted quantization job", "job_id", jobID)
	return nil
}

// watchProgress is the single reader of a job's backend progress stream. It
// records every transition on the job — in the cross-replica store and in
// state.json — and republishes it to the clients attached via StreamProgress.
//
// Recording here rather than in StreamProgress is the point: the backend hands
// each update to one consumer, so while StreamProgress was that consumer a job's
// state only advanced while somebody was watching it.
func (s *QuantizationService) watchProgress(ctx context.Context, jobID, backendName, modelID string) {
	backendModel, err := s.modelLoader.Load(
		model.WithBackendString(backendName),
		model.WithModel(backendName),
		model.WithModelID(modelID),
	)
	if err != nil {
		xlog.Warn("Failed to load backend for quantization progress", "job_id", jobID, "error", err)
		return
	}

	err = backendModel.QuantizationProgress(ctx, &pb.QuantizationProgressRequest{
		JobId: jobID,
	}, func(update *pb.QuantizationProgressUpdate) {
		s.publishProgress(jobID, s.applyProgressUpdate(ctx, jobID, update))
	})
	if err != nil {
		xlog.Warn("Quantization progress stream ended with an error", "job_id", jobID, "error", err)
	}

	// On shutdown leave the job alone: loadJobsFromDisk already reports jobs that
	// were running at exit as stopped.
	if ctx.Err() != nil {
		return
	}

	// A stream that ends without a terminal update means the backend is gone and
	// nothing further will arrive. Record that instead of leaving the job in a
	// running state forever — which is the failure this watcher exists to prevent —
	// and release any client still waiting on a terminal event.
	s.mu.Lock()
	j, ok := s.jobs.Get(jobID)
	stale := ok && !isTerminalStatus(j.Status)
	if stale {
		j.Status = "failed"
		if j.Message == "" {
			j.Message = "Backend progress stream ended before the job reported a result"
		}
		if err := s.jobs.Set(ctx, j); err != nil {
			xlog.Warn("Failed to persist orphaned job state", "job_id", jobID, "error", err)
		}
		s.saveJobState(j)
	}
	s.mu.Unlock()

	if stale {
		s.publishProgress(jobID, &schema.QuantizationProgressEvent{
			JobID:   jobID,
			Status:  "failed",
			Message: "Backend progress stream ended before the job reported a result",
		})
	}
}

// applyProgressUpdate records a backend progress update on the job and returns
// the event to hand to subscribers.
func (s *QuantizationService) applyProgressUpdate(ctx context.Context, jobID string, update *pb.QuantizationProgressUpdate) *schema.QuantizationProgressEvent {
	s.mu.Lock()
	if j, ok := s.jobs.Get(jobID); ok {
		// Don't let progress updates overwrite terminal states
		if !isTerminalStatus(j.Status) {
			j.Status = update.Status
		}
		if update.Message != "" {
			j.Message = update.Message
		}
		if update.OutputFile != "" {
			j.OutputFile = update.OutputFile
		}
		if err := s.jobs.Set(ctx, j); err != nil {
			xlog.Warn("Failed to persist progress update", "job_id", jobID, "error", err)
		}
		s.saveJobState(j)
	}
	s.mu.Unlock()

	// Convert extra metrics
	extraMetrics := make(map[string]float32, len(update.ExtraMetrics))
	for k, v := range update.ExtraMetrics {
		extraMetrics[k] = v
	}

	return &schema.QuantizationProgressEvent{
		JobID:           update.JobId,
		ProgressPercent: update.ProgressPercent,
		Status:          update.Status,
		Message:         update.Message,
		OutputFile:      update.OutputFile,
		ExtraMetrics:    extraMetrics,
	}
}

// subscribeProgress registers a channel to receive a job's progress events.
func (s *QuantizationService) subscribeProgress(jobID string) chan *schema.QuantizationProgressEvent {
	ch := make(chan *schema.QuantizationProgressEvent, progressSubBuffer)
	s.progressMu.Lock()
	s.progressSubs[jobID] = append(s.progressSubs[jobID], ch)
	s.progressMu.Unlock()
	return ch
}

// unsubscribeProgress removes a channel registered by subscribeProgress. The
// channel is never closed, so a publish racing with an unsubscribe cannot send
// on a closed channel.
func (s *QuantizationService) unsubscribeProgress(jobID string, ch chan *schema.QuantizationProgressEvent) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()

	subs := s.progressSubs[jobID]
	for i, c := range subs {
		if c == ch {
			s.progressSubs[jobID] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	if len(s.progressSubs[jobID]) == 0 {
		delete(s.progressSubs, jobID)
	}
}

// publishProgress fans an event out to a job's subscribers.
func (s *QuantizationService) publishProgress(jobID string, event *schema.QuantizationProgressEvent) {
	s.progressMu.Lock()
	subs := append([]chan *schema.QuantizationProgressEvent(nil), s.progressSubs[jobID]...)
	s.progressMu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- event:
		default:
			// A subscriber that cannot keep up must not stall the reader that is
			// recording job state for everyone else.
			xlog.Warn("Dropping quantization progress event for a slow subscriber", "job_id", jobID)
		}
	}
}

// StreamProgress calls the callback for each progress event of a job until it
// reaches a terminal status or ctx is done. It is a pure reader: the job's own
// watcher owns the backend stream and the state transitions.
func (s *QuantizationService) StreamProgress(ctx context.Context, userID, jobID string, callback func(event *schema.QuantizationProgressEvent)) error {
	s.mu.Lock()
	job, ok := s.jobs.Get(jobID)
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("job not found: %s", jobID)
	}
	if userID != "" && job.UserID != userID {
		s.mu.Unlock()
		return fmt.Errorf("job not found: %s", jobID)
	}
	s.mu.Unlock()

	ch := s.subscribeProgress(jobID)
	defer s.unsubscribeProgress(jobID, ch)

	// Re-read the job after subscribing: it may have finished between the lookup
	// above and the subscription, and no further event would ever arrive. Jobs
	// restored from disk after a restart are terminal too, and have no watcher.
	s.mu.Lock()
	current, ok := s.jobs.Get(jobID)
	terminal := ok && isTerminalStatus(current.Status)
	var final *schema.QuantizationProgressEvent
	if terminal {
		final = &schema.QuantizationProgressEvent{
			JobID:      current.ID,
			Status:     current.Status,
			Message:    current.Message,
			OutputFile: current.OutputFile,
		}
	}
	s.mu.Unlock()
	if terminal {
		callback(final)
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-ch:
			callback(event)
			if isTerminalStatus(event.Status) {
				return nil
			}
		}
	}
}

// sanitizeQuantModelName replaces non-alphanumeric characters with hyphens and lowercases.
func sanitizeQuantModelName(s string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9\-]`)
	s = re.ReplaceAllString(s, "-")
	s = regexp.MustCompile(`-+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return strings.ToLower(s)
}

// ImportModel imports a quantized model into LocalAI asynchronously.
func (s *QuantizationService) ImportModel(ctx context.Context, userID, jobID string, req schema.QuantizationImportRequest) (string, error) {
	s.mu.Lock()
	job, ok := s.jobs.Get(jobID)
	if !ok {
		s.mu.Unlock()
		return "", fmt.Errorf("job not found: %s", jobID)
	}
	if userID != "" && job.UserID != userID {
		s.mu.Unlock()
		return "", fmt.Errorf("job not found: %s", jobID)
	}
	if job.Status != "completed" {
		s.mu.Unlock()
		return "", fmt.Errorf("job %s is not completed (status: %s)", jobID, job.Status)
	}
	if job.ImportStatus == "importing" {
		s.mu.Unlock()
		return "", fmt.Errorf("import already in progress for job %s", jobID)
	}
	if job.OutputFile == "" {
		s.mu.Unlock()
		return "", fmt.Errorf("no output file for job %s", jobID)
	}
	s.mu.Unlock()

	// Compute model name
	modelName := req.Name
	if modelName == "" {
		base := sanitizeQuantModelName(job.Model)
		if base == "" {
			base = "model"
		}
		shortID := jobID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		modelName = base + "-" + job.QuantizationType + "-" + shortID
	}

	// Compute output path in models directory
	modelsPath := s.appConfig.SystemState.Model.ModelsPath
	outputPath := filepath.Join(modelsPath, modelName)

	// Check for name collision
	configPath := filepath.Join(modelsPath, modelName+".yaml")
	if err := utils.VerifyPath(modelName+".yaml", modelsPath); err != nil {
		return "", fmt.Errorf("invalid model name: %w", err)
	}
	if _, err := os.Stat(configPath); err == nil {
		return "", fmt.Errorf("model %q already exists, choose a different name", modelName)
	}

	// Create output directory
	if err := os.MkdirAll(outputPath, 0750); err != nil {
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}

	// Set import status
	s.mu.Lock()
	job.ImportStatus = "importing"
	job.ImportMessage = ""
	job.ImportModelName = ""
	if err := s.jobs.Set(ctx, job); err != nil {
		xlog.Warn("Failed to persist import start", "job_id", jobID, "error", err)
	}
	s.saveJobState(job)
	s.mu.Unlock()

	// Launch the import in a background goroutine
	go func() {
		s.setImportMessage(job, "Copying quantized model...")

		// Copy the output file to the models directory
		srcFile := job.OutputFile
		dstFile := filepath.Join(outputPath, filepath.Base(srcFile))

		srcData, err := os.ReadFile(srcFile)
		if err != nil {
			s.setImportFailed(job, fmt.Sprintf("failed to read output file: %v", err))
			return
		}
		if err := os.WriteFile(dstFile, srcData, 0644); err != nil {
			s.setImportFailed(job, fmt.Sprintf("failed to write model file: %v", err))
			return
		}

		s.setImportMessage(job, "Generating model configuration...")

		// Auto-import: detect format and generate config
		cfg, err := importers.ImportLocalPath(outputPath, modelName)
		if err != nil {
			s.setImportFailed(job, fmt.Sprintf("model copied to %s but config generation failed: %v", outputPath, err))
			return
		}

		cfg.Name = modelName

		// Write YAML config
		yamlData, err := yaml.Marshal(cfg)
		if err != nil {
			s.setImportFailed(job, fmt.Sprintf("failed to marshal config: %v", err))
			return
		}
		if err := os.WriteFile(configPath, yamlData, 0644); err != nil {
			s.setImportFailed(job, fmt.Sprintf("failed to write config file: %v", err))
			return
		}

		s.setImportMessage(job, "Registering model with LocalAI...")

		// Reload configs so the model is immediately available
		if err := s.configLoader.LoadModelConfigsFromPath(modelsPath, s.appConfig.ToConfigLoaderOptions()...); err != nil {
			xlog.Warn("Failed to reload configs after import", "error", err)
		}
		if err := s.configLoader.Preload(modelsPath); err != nil {
			xlog.Warn("Failed to preload after import", "error", err)
		}

		xlog.Info("Quantized model imported and registered", "job_id", jobID, "model_name", modelName)

		// Runs after the HTTP request returns, so use Background rather than the
		// (now likely cancelled) request ctx for the write-through.
		s.mu.Lock()
		job.ImportStatus = "completed"
		job.ImportModelName = modelName
		job.ImportMessage = ""
		if err := s.jobs.Set(context.Background(), job); err != nil {
			xlog.Warn("Failed to persist import completion", "job_id", jobID, "error", err)
		}
		s.saveJobState(job)
		s.mu.Unlock()
	}()

	return modelName, nil
}

// setImportMessage updates the import message and persists the job state. Called
// from the background import goroutine, so it uses Background for write-through.
func (s *QuantizationService) setImportMessage(job *schema.QuantizationJob, msg string) {
	s.mu.Lock()
	job.ImportMessage = msg
	if err := s.jobs.Set(context.Background(), job); err != nil {
		xlog.Warn("Failed to persist import message", "job_id", job.ID, "error", err)
	}
	s.saveJobState(job)
	s.mu.Unlock()
}

// setImportFailed sets the import status to failed with a message.
func (s *QuantizationService) setImportFailed(job *schema.QuantizationJob, message string) {
	xlog.Error("Quantization import failed", "job_id", job.ID, "error", message)
	s.mu.Lock()
	job.ImportStatus = "failed"
	job.ImportMessage = message
	if err := s.jobs.Set(context.Background(), job); err != nil {
		xlog.Warn("Failed to persist import failure", "job_id", job.ID, "error", err)
	}
	s.saveJobState(job)
	s.mu.Unlock()
}

// GetOutputPath returns the path to the quantized model file and a download name.
func (s *QuantizationService) GetOutputPath(userID, jobID string) (string, string, error) {
	s.mu.Lock()
	job, ok := s.jobs.Get(jobID)
	if !ok {
		s.mu.Unlock()
		return "", "", fmt.Errorf("job not found: %s", jobID)
	}
	if userID != "" && job.UserID != userID {
		s.mu.Unlock()
		return "", "", fmt.Errorf("job not found: %s", jobID)
	}
	if job.Status != "completed" {
		s.mu.Unlock()
		return "", "", fmt.Errorf("job not completed (status: %s)", job.Status)
	}
	if job.OutputFile == "" {
		s.mu.Unlock()
		return "", "", fmt.Errorf("no output file for job %s", jobID)
	}
	outputFile := job.OutputFile
	s.mu.Unlock()

	if _, err := os.Stat(outputFile); os.IsNotExist(err) {
		return "", "", fmt.Errorf("output file not found: %s", outputFile)
	}

	downloadName := filepath.Base(outputFile)
	return outputFile, downloadName, nil
}
