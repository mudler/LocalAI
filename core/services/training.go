package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/xsync"
	"github.com/mudler/xlog"
)

// TrainingJobService manages fine-tuning job lifecycle
type TrainingJobService struct {
	appConfig    *config.ApplicationConfig
	modelLoader  *model.ModelLoader
	configLoader *config.ModelConfigLoader

	jobs          *xsync.SyncedMap[string, schema.TrainingJob]
	cancellations *xsync.SyncedMap[string, context.CancelFunc]
	jobsFile      string

	ctx    context.Context
	cancel context.CancelFunc

	fileMutex sync.Mutex
}

// NewTrainingJobService creates a new TrainingJobService
func NewTrainingJobService(
	appConfig *config.ApplicationConfig,
	modelLoader *model.ModelLoader,
	configLoader *config.ModelConfigLoader,
) *TrainingJobService {
	dataDir := appConfig.DataPath
	if dataDir == "" {
		dataDir = appConfig.DynamicConfigsDir
	}

	jobsFile := ""
	if dataDir != "" {
		jobsFile = filepath.Join(dataDir, "training_jobs.json")
	}

	return &TrainingJobService{
		appConfig:     appConfig,
		modelLoader:   modelLoader,
		configLoader:  configLoader,
		jobs:          xsync.NewSyncedMap[string, schema.TrainingJob](),
		cancellations: xsync.NewSyncedMap[string, context.CancelFunc](),
		jobsFile:      jobsFile,
	}
}

// Start initializes the service and loads persisted jobs
func (s *TrainingJobService) Start(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)

	if err := s.loadJobs(); err != nil {
		xlog.Warn("Failed to load training jobs from disk", "error", err)
	}

	return nil
}

// CreateJob creates a new training job and starts it asynchronously
func (s *TrainingJobService) CreateJob(req schema.TrainingJobRequest) (schema.TrainingJob, error) {
	if req.Model == "" {
		return schema.TrainingJob{}, fmt.Errorf("model is required")
	}
	if req.Dataset == "" {
		return schema.TrainingJob{}, fmt.Errorf("dataset is required")
	}

	id := uuid.New().String()
	now := time.Now()

	outputDir := req.OutputDir
	if outputDir == "" {
		outputDir = filepath.Join(s.appConfig.GeneratedContentDir, "training", id)
	}

	job := schema.TrainingJob{
		ID:        id,
		Model:     req.Model,
		Backend:   req.Backend,
		Dataset:   req.Dataset,
		OutputDir: outputDir,
		Status:    schema.TrainingJobStatusPending,
		CreatedAt: now,
		Parameters: schema.TrainingParams{
			Epochs:       req.Epochs,
			BatchSize:    req.BatchSize,
			LearningRate: req.LearningRate,
			MaxSeqLength: req.MaxSeqLength,
			LoraRank:     req.LoraRank,
			LoraAlpha:    req.LoraAlpha,
			LoraDropout:  req.LoraDropout,
			Quantization: req.Quantization,
			Options:      req.Options,
		},
	}

	s.jobs.Set(id, job)
	s.saveJobs()

	// Start training asynchronously
	go s.executeJob(id)

	return job, nil
}

// GetJob returns a training job by ID
func (s *TrainingJobService) GetJob(id string) (schema.TrainingJob, bool) {
	return s.jobs.Get(id)
}

// ListJobs returns all training jobs, sorted by creation time (newest first)
func (s *TrainingJobService) ListJobs() []schema.TrainingJob {
	var jobs []schema.TrainingJob
	s.jobs.Iterate(func(_ string, job schema.TrainingJob) bool {
		jobs = append(jobs, job)
		return true
	})
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})
	return jobs
}

// CancelJob cancels a running training job
func (s *TrainingJobService) CancelJob(id string) error {
	job, ok := s.jobs.Get(id)
	if !ok {
		return fmt.Errorf("job not found: %s", id)
	}

	if job.Status != schema.TrainingJobStatusPending && job.Status != schema.TrainingJobStatusRunning {
		return fmt.Errorf("job %s is not cancellable (status: %s)", id, job.Status)
	}

	cancelFn, ok := s.cancellations.Get(id)
	if ok {
		cancelFn()
	}

	now := time.Now()
	job.Status = schema.TrainingJobStatusCancelled
	job.CompletedAt = &now
	job.Message = "Job cancelled by user"
	s.jobs.Set(id, job)
	s.saveJobs()

	return nil
}

// DeleteJob removes a job from the store
func (s *TrainingJobService) DeleteJob(id string) error {
	job, ok := s.jobs.Get(id)
	if !ok {
		return fmt.Errorf("job not found: %s", id)
	}

	// Cancel if still running
	if job.Status == schema.TrainingJobStatusRunning || job.Status == schema.TrainingJobStatusPending {
		if cancelFn, ok := s.cancellations.Get(id); ok {
			cancelFn()
		}
	}

	s.jobs.Delete(id)
	s.cancellations.Delete(id)
	s.saveJobs()
	return nil
}

// executeJob runs the training job via the backend gRPC call
func (s *TrainingJobService) executeJob(id string) {
	job, ok := s.jobs.Get(id)
	if !ok {
		return
	}

	ctx, cancel := context.WithCancel(s.ctx)
	s.cancellations.Set(id, cancel)
	defer func() {
		cancel()
		s.cancellations.Delete(id)
	}()

	now := time.Now()
	job.Status = schema.TrainingJobStatusRunning
	job.StartedAt = &now
	job.Message = "Starting training..."
	s.jobs.Set(id, job)
	s.saveJobs()

	// Build gRPC TrainRequest
	trainReq := &proto.TrainRequest{
		Model:         job.Model,
		Dataset:       job.Dataset,
		OutputDir:     job.OutputDir,
		Epochs:        job.Parameters.Epochs,
		BatchSize:     job.Parameters.BatchSize,
		LearningRate:  job.Parameters.LearningRate,
		MaxSeqLength:  job.Parameters.MaxSeqLength,
		LoraRank:      job.Parameters.LoraRank,
		LoraAlpha:     job.Parameters.LoraAlpha,
		LoraDropout:   job.Parameters.LoraDropout,
		TargetModules: job.Parameters.TargetModules,
		Quantization:  job.Parameters.Quantization,
		Options:       job.Parameters.Options,
	}

	// Resolve model config — use the backend field from the request, defaulting to "unsloth"
	backendName := job.Backend
	if backendName == "" {
		backendName = "unsloth"
	}

	modelConfig := config.ModelConfig{
		Name:    job.Model,
		Model:   job.Model,
		Backend: backendName,
	}

	// Attempt to load from existing model configs
	if cfgs := s.configLoader.ListConfigs(); len(cfgs) > 0 {
		for _, name := range cfgs {
			if name == job.Model {
				if cfg, exists := s.configLoader.GetConfig(name); exists {
					modelConfig = cfg
					if backendName != "" {
						modelConfig.Backend = backendName
					}
				}
				break
			}
		}
	}

	// Load backend and call TrainStream directly (avoids import cycle with core/backend)
	opts := []model.Option{
		model.WithBackendString(modelConfig.Backend),
		model.WithModel(modelConfig.Model),
		model.WithContext(s.appConfig.Context),
		model.WithModelID(modelConfig.Name),
	}
	grpcBackend, err := s.modelLoader.Load(opts...)
	if err != nil {
		err = fmt.Errorf("loading backend for training: %w", err)
	} else if grpcBackend == nil {
		err = fmt.Errorf("could not load backend %q for training", modelConfig.Backend)
	} else {
		err = grpcBackend.TrainStream(ctx, trainReq, func(resp *proto.TrainResponse) {
			s.updateJobFromResponse(id, resp)
		})
	}

	// Final status update
	job, _ = s.jobs.Get(id)
	completedAt := time.Now()

	if err != nil {
		if ctx.Err() != nil {
			// Context was cancelled
			job.Status = schema.TrainingJobStatusCancelled
			job.Message = "Job cancelled"
		} else {
			job.Status = schema.TrainingJobStatusFailed
			job.Error = err.Error()
			job.Message = fmt.Sprintf("Training failed: %s", err.Error())
		}
	} else if job.Status != schema.TrainingJobStatusCompleted {
		// Mark completed if the backend didn't already
		job.Status = schema.TrainingJobStatusCompleted
		job.Progress = 100.0
		job.Message = "Training completed"
	}

	job.CompletedAt = &completedAt
	s.jobs.Set(id, job)
	s.saveJobs()

	xlog.Info("Training job finished", "id", id, "status", job.Status, "output", job.OutputPath)
}

// updateJobFromResponse updates job state from a streaming TrainResponse
func (s *TrainingJobService) updateJobFromResponse(id string, resp *proto.TrainResponse) {
	job, ok := s.jobs.Get(id)
	if !ok {
		return
	}

	job.Progress = resp.Progress
	job.CurrentEpoch = resp.CurrentEpoch
	job.TotalEpochs = resp.TotalEpochs
	job.CurrentStep = resp.CurrentStep
	job.TotalSteps = resp.TotalSteps
	job.Loss = resp.Loss
	job.LearningRate = resp.LearningRateCurrent
	job.Message = resp.Message

	if resp.OutputPath != "" {
		job.OutputPath = resp.OutputPath
	}
	if resp.Error != "" {
		job.Error = resp.Error
	}

	switch resp.Status {
	case proto.TrainResponse_COMPLETED:
		job.Status = schema.TrainingJobStatusCompleted
		now := time.Now()
		job.CompletedAt = &now
	case proto.TrainResponse_FAILED:
		job.Status = schema.TrainingJobStatusFailed
		now := time.Now()
		job.CompletedAt = &now
	case proto.TrainResponse_RUNNING:
		job.Status = schema.TrainingJobStatusRunning
	}

	s.jobs.Set(id, job)
}

// loadJobs reads persisted jobs from disk
func (s *TrainingJobService) loadJobs() error {
	if s.jobsFile == "" {
		return nil
	}

	data, err := os.ReadFile(s.jobsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var jobs []schema.TrainingJob
	if err := json.Unmarshal(data, &jobs); err != nil {
		return err
	}

	for _, job := range jobs {
		// Mark any previously-running jobs as failed (server restarted)
		if job.Status == schema.TrainingJobStatusRunning || job.Status == schema.TrainingJobStatusPending {
			job.Status = schema.TrainingJobStatusFailed
			job.Error = "server restarted during training"
			job.Message = "Job interrupted by server restart"
			now := time.Now()
			job.CompletedAt = &now
		}
		s.jobs.Set(job.ID, job)
	}

	return nil
}

// saveJobs persists all jobs to disk
func (s *TrainingJobService) saveJobs() {
	if s.jobsFile == "" {
		return
	}

	s.fileMutex.Lock()
	defer s.fileMutex.Unlock()

	jobs := s.ListJobs()
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		xlog.Error("Failed to marshal training jobs", "error", err)
		return
	}

	dir := filepath.Dir(s.jobsFile)
	if err := os.MkdirAll(dir, 0750); err != nil {
		xlog.Error("Failed to create training jobs directory", "error", err)
		return
	}

	if err := os.WriteFile(s.jobsFile, data, 0600); err != nil {
		xlog.Error("Failed to save training jobs", "error", err)
	}
}
