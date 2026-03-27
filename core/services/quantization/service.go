package quantization

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

// QuantizationService manages quantization jobs and their lifecycle.
type QuantizationService struct {
	appConfig    *config.ApplicationConfig
	modelLoader  *model.ModelLoader
	configLoader *config.ModelConfigLoader

	mu   sync.Mutex
	jobs map[string]*schema.QuantizationJob
}

// NewQuantizationService creates a new QuantizationService.
func NewQuantizationService(
	appConfig *config.ApplicationConfig,
	modelLoader *model.ModelLoader,
	configLoader *config.ModelConfigLoader,
) *QuantizationService {
	s := &QuantizationService{
		appConfig:    appConfig,
		modelLoader:  modelLoader,
		configLoader: configLoader,
		jobs:         make(map[string]*schema.QuantizationJob),
	}
	s.loadAllJobs()
	return s
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

// loadAllJobs scans the quantization directory for persisted jobs and loads them.
func (s *QuantizationService) loadAllJobs() {
	baseDir := s.quantizationBaseDir()
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

		s.jobs[job.ID] = &job
	}

	if len(s.jobs) > 0 {
		xlog.Info("Loaded persisted quantization jobs", "count", len(s.jobs))
	}
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
	s.jobs[jobID] = job
	s.saveJobState(job)

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

	job, ok := s.jobs[jobID]
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
	for _, job := range s.jobs {
		if userID == "" || job.UserID == userID {
			result = append(result, job)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt > result[j].CreatedAt
	})

	return result
}

// StopJob stops a running quantization job.
func (s *QuantizationService) StopJob(ctx context.Context, userID, jobID string) error {
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

	// Kill the backend process directly
	stopModelID := job.ModelID
	if stopModelID == "" {
		stopModelID = job.Backend + "-quantize"
	}
	s.modelLoader.ShutdownModel(stopModelID)

	s.mu.Lock()
	job.Status = "stopped"
	job.Message = "Quantization stopped by user"
	s.saveJobState(job)
	s.mu.Unlock()

	return nil
}

// DeleteJob removes a quantization job and its associated data from disk.
func (s *QuantizationService) DeleteJob(userID, jobID string) error {
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
	delete(s.jobs, jobID)
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

// StreamProgress opens a gRPC progress stream and calls the callback for each update.
func (s *QuantizationService) StreamProgress(ctx context.Context, userID, jobID string, callback func(event *schema.QuantizationProgressEvent)) error {
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

	streamModelID := job.ModelID
	if streamModelID == "" {
		streamModelID = job.Backend + "-quantize"
	}
	backendModel, err := s.modelLoader.Load(
		model.WithBackendString(job.Backend),
		model.WithModel(job.Backend),
		model.WithModelID(streamModelID),
	)
	if err != nil {
		return fmt.Errorf("failed to load backend: %w", err)
	}

	return backendModel.QuantizationProgress(ctx, &pb.QuantizationProgressRequest{
		JobId: jobID,
	}, func(update *pb.QuantizationProgressUpdate) {
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
			if update.OutputFile != "" {
				j.OutputFile = update.OutputFile
			}
			s.saveJobState(j)
		}
		s.mu.Unlock()

		// Convert extra metrics
		extraMetrics := make(map[string]float32)
		for k, v := range update.ExtraMetrics {
			extraMetrics[k] = v
		}

		event := &schema.QuantizationProgressEvent{
			JobID:           update.JobId,
			ProgressPercent: update.ProgressPercent,
			Status:          update.Status,
			Message:         update.Message,
			OutputFile:      update.OutputFile,
			ExtraMetrics:    extraMetrics,
		}
		callback(event)
	})
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
	job, ok := s.jobs[jobID]
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

		s.mu.Lock()
		job.ImportStatus = "completed"
		job.ImportModelName = modelName
		job.ImportMessage = ""
		s.saveJobState(job)
		s.mu.Unlock()
	}()

	return modelName, nil
}

// setImportMessage updates the import message and persists the job state.
func (s *QuantizationService) setImportMessage(job *schema.QuantizationJob, msg string) {
	s.mu.Lock()
	job.ImportMessage = msg
	s.saveJobState(job)
	s.mu.Unlock()
}

// setImportFailed sets the import status to failed with a message.
func (s *QuantizationService) setImportFailed(job *schema.QuantizationJob, message string) {
	xlog.Error("Quantization import failed", "job_id", job.ID, "error", message)
	s.mu.Lock()
	job.ImportStatus = "failed"
	job.ImportMessage = message
	s.saveJobState(job)
	s.mu.Unlock()
}

// GetOutputPath returns the path to the quantized model file and a download name.
func (s *QuantizationService) GetOutputPath(userID, jobID string) (string, string, error) {
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
