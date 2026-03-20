package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery/importers"
	"github.com/mudler/LocalAI/core/schema"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
	"gopkg.in/yaml.v3"
)

// FineTuneService manages fine-tuning jobs and their lifecycle.
type FineTuneService struct {
	appConfig    *config.ApplicationConfig
	modelLoader  *model.ModelLoader
	configLoader *config.ModelConfigLoader

	mu   sync.Mutex
	jobs map[string]*schema.FineTuneJob
}

// NewFineTuneService creates a new FineTuneService.
func NewFineTuneService(
	appConfig *config.ApplicationConfig,
	modelLoader *model.ModelLoader,
	configLoader *config.ModelConfigLoader,
) *FineTuneService {
	s := &FineTuneService{
		appConfig:    appConfig,
		modelLoader:  modelLoader,
		configLoader: configLoader,
		jobs:         make(map[string]*schema.FineTuneJob),
	}
	s.loadAllJobs()
	return s
}

// fineTuneBaseDir returns the base directory for fine-tune job data.
func (s *FineTuneService) fineTuneBaseDir() string {
	return filepath.Join(s.appConfig.DataPath, "fine-tune")
}

// jobDir returns the directory for a specific job.
func (s *FineTuneService) jobDir(jobID string) string {
	return filepath.Join(s.fineTuneBaseDir(), jobID)
}

// saveJobState persists a job's state to disk as state.json.
func (s *FineTuneService) saveJobState(job *schema.FineTuneJob) {
	dir := s.jobDir(job.ID)
	if err := os.MkdirAll(dir, 0750); err != nil {
		xlog.Error("Failed to create job directory", "job_id", job.ID, "error", err)
		return
	}

	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		xlog.Error("Failed to marshal job state", "job_id", job.ID, "error", err)
		return
	}

	statePath := filepath.Join(dir, "state.json")
	if err := os.WriteFile(statePath, data, 0640); err != nil {
		xlog.Error("Failed to write job state", "job_id", job.ID, "error", err)
	}
}

// loadAllJobs scans the fine-tune directory for persisted jobs and loads them.
func (s *FineTuneService) loadAllJobs() {
	baseDir := s.fineTuneBaseDir()
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		// Directory doesn't exist yet — that's fine
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		statePath := filepath.Join(baseDir, entry.Name(), "state.json")
		data, err := os.ReadFile(statePath)
		if err != nil {
			continue
		}

		var job schema.FineTuneJob
		if err := json.Unmarshal(data, &job); err != nil {
			xlog.Warn("Failed to parse job state", "path", statePath, "error", err)
			continue
		}

		// Jobs that were running when we shut down are now stale
		if job.Status == "queued" || job.Status == "loading_model" || job.Status == "loading_dataset" || job.Status == "training" || job.Status == "saving" {
			job.Status = "stopped"
			job.Message = "Server restarted while job was running"
		}

		// Exports that were in progress are now stale
		if job.ExportStatus == "exporting" {
			job.ExportStatus = "failed"
			job.ExportMessage = "Server restarted while export was running"
		}

		s.jobs[job.ID] = &job
	}

	if len(s.jobs) > 0 {
		xlog.Info("Loaded persisted fine-tune jobs", "count", len(s.jobs))
	}
}

// StartJob starts a new fine-tuning job.
func (s *FineTuneService) StartJob(ctx context.Context, userID string, req schema.FineTuneJobRequest) (*schema.FineTuneJobResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobID := uuid.New().String()

	backendName := req.Backend
	if backendName == "" {
		backendName = "trl"
	}

	// Always use DataPath for output — not user-configurable
	outputDir := filepath.Join(s.fineTuneBaseDir(), jobID)

	// Build gRPC request
	grpcReq := &pb.FineTuneRequest{
		Model:                     req.Model,
		TrainingType:              req.TrainingType,
		TrainingMethod:            req.TrainingMethod,
		AdapterRank:               req.AdapterRank,
		AdapterAlpha:              req.AdapterAlpha,
		AdapterDropout:            req.AdapterDropout,
		TargetModules:             req.TargetModules,
		LearningRate:              req.LearningRate,
		NumEpochs:                 req.NumEpochs,
		BatchSize:                 req.BatchSize,
		GradientAccumulationSteps: req.GradientAccumulationSteps,
		WarmupSteps:               req.WarmupSteps,
		MaxSteps:                  req.MaxSteps,
		SaveSteps:                 req.SaveSteps,
		WeightDecay:               req.WeightDecay,
		GradientCheckpointing:     req.GradientCheckpointing,
		Optimizer:                 req.Optimizer,
		Seed:                      req.Seed,
		MixedPrecision:            req.MixedPrecision,
		DatasetSource:             req.DatasetSource,
		DatasetSplit:              req.DatasetSplit,
		OutputDir:                 outputDir,
		JobId:                     jobID,
		ResumeFromCheckpoint:      req.ResumeFromCheckpoint,
		ExtraOptions:              req.ExtraOptions,
	}

	// Serialize reward functions into extra_options for the backend
	if len(req.RewardFunctions) > 0 {
		rfJSON, err := json.Marshal(req.RewardFunctions)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize reward functions: %w", err)
		}
		if grpcReq.ExtraOptions == nil {
			grpcReq.ExtraOptions = make(map[string]string)
		}
		grpcReq.ExtraOptions["reward_funcs"] = string(rfJSON)
	}

	// Load the fine-tuning backend
	backendModel, err := s.modelLoader.Load(
		model.WithBackendString(backendName),
		model.WithModel(backendName),
		model.WithModelID(backendName+"-finetune"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load backend %s: %w", backendName, err)
	}

	// Start fine-tuning via gRPC
	result, err := backendModel.StartFineTune(ctx, grpcReq)
	if err != nil {
		return nil, fmt.Errorf("failed to start fine-tuning: %w", err)
	}
	if !result.Success {
		return nil, fmt.Errorf("fine-tuning failed to start: %s", result.Message)
	}

	// Track the job
	job := &schema.FineTuneJob{
		ID:             jobID,
		UserID:         userID,
		Model:          req.Model,
		Backend:        backendName,
		TrainingType:   req.TrainingType,
		TrainingMethod: req.TrainingMethod,
		Status:         "queued",
		OutputDir:      outputDir,
		ExtraOptions:   req.ExtraOptions,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		Config:         &req,
	}
	s.jobs[jobID] = job
	s.saveJobState(job)

	return &schema.FineTuneJobResponse{
		ID:      jobID,
		Status:  "queued",
		Message: result.Message,
	}, nil
}

// GetJob returns a fine-tuning job by ID.
func (s *FineTuneService) GetJob(userID, jobID string) (*schema.FineTuneJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.jobs[jobID]
	if !ok {
		return nil, fmt.Errorf("job not found: %s", jobID)
	}
	if userID != "" && job.UserID != userID {
		return nil, fmt.Errorf("job not found: %s", jobID)
	}
	return job, nil
}

// ListJobs returns all jobs for a user.
func (s *FineTuneService) ListJobs(userID string) []*schema.FineTuneJob {
	s.mu.Lock()
	defer s.mu.Unlock()

	var result []*schema.FineTuneJob
	for _, job := range s.jobs {
		if userID == "" || job.UserID == userID {
			result = append(result, job)
		}
	}
	return result
}

// StopJob stops a running fine-tuning job.
func (s *FineTuneService) StopJob(ctx context.Context, userID, jobID string, saveCheckpoint bool) error {
	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("job not found: %s", jobID)
	}
	if userID != "" && job.UserID != userID {
		s.mu.Unlock()
		return fmt.Errorf("job not found: %s", jobID)
	}
	s.mu.Unlock()

	backendModel, err := s.modelLoader.Load(
		model.WithBackendString(job.Backend),
		model.WithModel(job.Backend),
		model.WithModelID(job.Backend+"-finetune"),
	)
	if err != nil {
		return fmt.Errorf("failed to load backend: %w", err)
	}

	_, err = backendModel.StopFineTune(ctx, &pb.FineTuneStopRequest{
		JobId:          jobID,
		SaveCheckpoint: saveCheckpoint,
	})
	if err != nil {
		return fmt.Errorf("failed to stop job: %w", err)
	}

	s.mu.Lock()
	job.Message = "Stop requested, waiting for training to halt..."
	s.saveJobState(job)
	s.mu.Unlock()

	return nil
}

// DeleteJob removes a fine-tuning job and its associated data from disk.
func (s *FineTuneService) DeleteJob(userID, jobID string) error {
	s.mu.Lock()
	job, ok := s.jobs[jobID]
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
		"queued": true, "loading_model": true, "loading_dataset": true,
		"training": true, "saving": true,
	}
	if activeStatuses[job.Status] {
		s.mu.Unlock()
		return fmt.Errorf("cannot delete job %s: currently %s (stop it first)", jobID, job.Status)
	}
	if job.ExportStatus == "exporting" {
		s.mu.Unlock()
		return fmt.Errorf("cannot delete job %s: export in progress", jobID)
	}

	exportModelName := job.ExportModelName
	delete(s.jobs, jobID)
	s.mu.Unlock()

	// Remove job directory (state.json, checkpoints, output)
	jobDir := s.jobDir(jobID)
	if err := os.RemoveAll(jobDir); err != nil {
		xlog.Warn("Failed to remove job directory", "job_id", jobID, "path", jobDir, "error", err)
	}

	// If an exported model exists, clean it up too
	if exportModelName != "" {
		modelsPath := s.appConfig.SystemState.Model.ModelsPath
		modelDir := filepath.Join(modelsPath, exportModelName)
		configPath := filepath.Join(modelsPath, exportModelName+".yaml")

		if err := os.RemoveAll(modelDir); err != nil {
			xlog.Warn("Failed to remove exported model directory", "path", modelDir, "error", err)
		}
		if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
			xlog.Warn("Failed to remove exported model config", "path", configPath, "error", err)
		}

		// Reload model configs
		if err := s.configLoader.LoadModelConfigsFromPath(modelsPath, s.appConfig.ToConfigLoaderOptions()...); err != nil {
			xlog.Warn("Failed to reload configs after delete", "error", err)
		}
	}

	xlog.Info("Deleted fine-tune job", "job_id", jobID)
	return nil
}

// StreamProgress opens a gRPC progress stream and calls the callback for each update.
func (s *FineTuneService) StreamProgress(ctx context.Context, userID, jobID string, callback func(event *schema.FineTuneProgressEvent)) error {
	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("job not found: %s", jobID)
	}
	if userID != "" && job.UserID != userID {
		s.mu.Unlock()
		return fmt.Errorf("job not found: %s", jobID)
	}
	s.mu.Unlock()

	backendModel, err := s.modelLoader.Load(
		model.WithBackendString(job.Backend),
		model.WithModel(job.Backend),
		model.WithModelID(job.Backend+"-finetune"),
	)
	if err != nil {
		return fmt.Errorf("failed to load backend: %w", err)
	}

	return backendModel.FineTuneProgress(ctx, &pb.FineTuneProgressRequest{
		JobId: jobID,
	}, func(update *pb.FineTuneProgressUpdate) {
		// Update job status and persist
		s.mu.Lock()
		if j, ok := s.jobs[jobID]; ok {
			// Don't let progress updates overwrite terminal states
			isTerminal := j.Status == "stopped" || j.Status == "completed" || j.Status == "failed"
			if !isTerminal {
				j.Status = update.Status
			}
			if update.Message != "" {
				j.Message = update.Message
			}
			s.saveJobState(j)
		}
		s.mu.Unlock()

		// Convert extra metrics
		extraMetrics := make(map[string]float32)
		for k, v := range update.ExtraMetrics {
			extraMetrics[k] = v
		}

		event := &schema.FineTuneProgressEvent{
			JobID:           update.JobId,
			CurrentStep:     update.CurrentStep,
			TotalSteps:      update.TotalSteps,
			CurrentEpoch:    update.CurrentEpoch,
			TotalEpochs:     update.TotalEpochs,
			Loss:            update.Loss,
			LearningRate:    update.LearningRate,
			GradNorm:        update.GradNorm,
			EvalLoss:        update.EvalLoss,
			EtaSeconds:      update.EtaSeconds,
			ProgressPercent: update.ProgressPercent,
			Status:          update.Status,
			Message:         update.Message,
			CheckpointPath:  update.CheckpointPath,
			SamplePath:      update.SamplePath,
			ExtraMetrics:    extraMetrics,
		}
		callback(event)
	})
}

// ListCheckpoints lists checkpoints for a job.
func (s *FineTuneService) ListCheckpoints(ctx context.Context, userID, jobID string) ([]*pb.CheckpointInfo, error) {
	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("job not found: %s", jobID)
	}
	if userID != "" && job.UserID != userID {
		s.mu.Unlock()
		return nil, fmt.Errorf("job not found: %s", jobID)
	}
	s.mu.Unlock()

	backendModel, err := s.modelLoader.Load(
		model.WithBackendString(job.Backend),
		model.WithModel(job.Backend),
		model.WithModelID(job.Backend+"-finetune"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load backend: %w", err)
	}

	resp, err := backendModel.ListCheckpoints(ctx, &pb.ListCheckpointsRequest{
		OutputDir: job.OutputDir,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list checkpoints: %w", err)
	}

	return resp.Checkpoints, nil
}

// sanitizeModelName replaces non-alphanumeric characters with hyphens and lowercases.
func sanitizeModelName(s string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9\-]`)
	s = re.ReplaceAllString(s, "-")
	s = regexp.MustCompile(`-+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return strings.ToLower(s)
}

// ExportModel starts an async model export from a checkpoint and returns the intended model name immediately.
func (s *FineTuneService) ExportModel(ctx context.Context, userID, jobID string, req schema.ExportRequest) (string, error) {
	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if !ok {
		s.mu.Unlock()
		return "", fmt.Errorf("job not found: %s", jobID)
	}
	if userID != "" && job.UserID != userID {
		s.mu.Unlock()
		return "", fmt.Errorf("job not found: %s", jobID)
	}
	if job.ExportStatus == "exporting" {
		s.mu.Unlock()
		return "", fmt.Errorf("export already in progress for job %s", jobID)
	}
	s.mu.Unlock()

	// Compute model name
	modelName := req.Name
	if modelName == "" {
		base := sanitizeModelName(job.Model)
		if base == "" {
			base = "model"
		}
		shortID := jobID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		modelName = base + "-ft-" + shortID
	}

	// Compute output path in models directory
	modelsPath := s.appConfig.SystemState.Model.ModelsPath
	outputPath := filepath.Join(modelsPath, modelName)

	// Check for name collision (synchronous — fast validation)
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

	// Set export status to "exporting" and persist
	s.mu.Lock()
	job.ExportStatus = "exporting"
	job.ExportMessage = ""
	job.ExportModelName = ""
	s.saveJobState(job)
	s.mu.Unlock()

	// Launch the export in a background goroutine
	go func() {
		s.setExportMessage(job, "Loading export backend...")

		backendModel, err := s.modelLoader.Load(
			model.WithBackendString(job.Backend),
			model.WithModel(job.Backend),
			model.WithModelID(job.Backend+"-finetune"),
		)
		if err != nil {
			s.setExportFailed(job, fmt.Sprintf("failed to load backend: %v", err))
			return
		}

		// Merge job's extra_options (contains hf_token from training) with request's
		mergedOpts := make(map[string]string)
		for k, v := range job.ExtraOptions {
			mergedOpts[k] = v
		}
		for k, v := range req.ExtraOptions {
			mergedOpts[k] = v // request overrides job
		}

		grpcReq := &pb.ExportModelRequest{
			CheckpointPath:     req.CheckpointPath,
			OutputPath:         outputPath,
			ExportFormat:       req.ExportFormat,
			QuantizationMethod: req.QuantizationMethod,
			Model:              req.Model,
			ExtraOptions:       mergedOpts,
		}

		s.setExportMessage(job, "Running model export (merging and converting — this may take a while)...")

		result, err := backendModel.ExportModel(context.Background(), grpcReq)
		if err != nil {
			s.setExportFailed(job, fmt.Sprintf("export failed: %v", err))
			return
		}
		if !result.Success {
			s.setExportFailed(job, fmt.Sprintf("export failed: %s", result.Message))
			return
		}

		s.setExportMessage(job, "Export complete, generating model configuration...")

		// Auto-import: detect format and generate config
		cfg, err := importers.ImportLocalPath(outputPath, modelName)
		if err != nil {
			s.setExportFailed(job, fmt.Sprintf("model exported to %s but config generation failed: %v", outputPath, err))
			return
		}

		cfg.Name = modelName

		// If base model not detected from files, use the job's model field
		if cfg.Model == "" && job.Model != "" {
			cfg.Model = job.Model
		}

		// Write YAML config
		yamlData, err := yaml.Marshal(cfg)
		if err != nil {
			s.setExportFailed(job, fmt.Sprintf("failed to marshal config: %v", err))
			return
		}
		if err := os.WriteFile(configPath, yamlData, 0644); err != nil {
			s.setExportFailed(job, fmt.Sprintf("failed to write config file: %v", err))
			return
		}

		s.setExportMessage(job, "Registering model with LocalAI...")

		// Reload configs so the model is immediately available
		if err := s.configLoader.LoadModelConfigsFromPath(modelsPath, s.appConfig.ToConfigLoaderOptions()...); err != nil {
			xlog.Warn("Failed to reload configs after export", "error", err)
		}
		if err := s.configLoader.Preload(modelsPath); err != nil {
			xlog.Warn("Failed to preload after export", "error", err)
		}

		xlog.Info("Model exported and registered", "job_id", jobID, "model_name", modelName, "format", req.ExportFormat)

		s.mu.Lock()
		job.ExportStatus = "completed"
		job.ExportModelName = modelName
		job.ExportMessage = ""
		s.saveJobState(job)
		s.mu.Unlock()
	}()

	return modelName, nil
}

// setExportMessage updates the export message and persists the job state.
func (s *FineTuneService) setExportMessage(job *schema.FineTuneJob, msg string) {
	s.mu.Lock()
	job.ExportMessage = msg
	s.saveJobState(job)
	s.mu.Unlock()
}

// GetExportedModelPath returns the path to the exported model directory and its name.
func (s *FineTuneService) GetExportedModelPath(userID, jobID string) (string, string, error) {
	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if !ok {
		s.mu.Unlock()
		return "", "", fmt.Errorf("job not found: %s", jobID)
	}
	if userID != "" && job.UserID != userID {
		s.mu.Unlock()
		return "", "", fmt.Errorf("job not found: %s", jobID)
	}
	if job.ExportStatus != "completed" {
		s.mu.Unlock()
		return "", "", fmt.Errorf("export not completed for job %s (status: %s)", jobID, job.ExportStatus)
	}
	exportModelName := job.ExportModelName
	s.mu.Unlock()

	if exportModelName == "" {
		return "", "", fmt.Errorf("no exported model name for job %s", jobID)
	}

	modelsPath := s.appConfig.SystemState.Model.ModelsPath
	modelDir := filepath.Join(modelsPath, exportModelName)

	if _, err := os.Stat(modelDir); os.IsNotExist(err) {
		return "", "", fmt.Errorf("exported model directory not found: %s", modelDir)
	}

	return modelDir, exportModelName, nil
}

// setExportFailed sets the export status to failed with a message.
func (s *FineTuneService) setExportFailed(job *schema.FineTuneJob, message string) {
	xlog.Error("Export failed", "job_id", job.ID, "error", message)
	s.mu.Lock()
	job.ExportStatus = "failed"
	job.ExportMessage = message
	s.saveJobState(job)
	s.mu.Unlock()
}

// UploadDataset handles dataset file upload and returns the local path.
func (s *FineTuneService) UploadDataset(filename string, data []byte) (string, error) {
	uploadDir := filepath.Join(s.fineTuneBaseDir(), "datasets")
	if err := os.MkdirAll(uploadDir, 0750); err != nil {
		return "", fmt.Errorf("failed to create dataset directory: %w", err)
	}

	filePath := filepath.Join(uploadDir, uuid.New().String()[:8]+"-"+filename)
	if err := os.WriteFile(filePath, data, 0640); err != nil {
		return "", fmt.Errorf("failed to write dataset: %w", err)
	}

	return filePath, nil
}

// MarshalProgressEvent converts a progress event to JSON for SSE.
func MarshalProgressEvent(event *schema.FineTuneProgressEvent) (string, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
