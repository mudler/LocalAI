package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"text/template"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/xsync"
	"github.com/mudler/cogito"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog/log"
)

// AgentJobService manages agent tasks and job execution
type AgentJobService struct {
	appConfig    *config.ApplicationConfig
	modelLoader  *model.ModelLoader
	configLoader *config.ModelConfigLoader
	evaluator    *templates.Evaluator

	// Storage (file-based with in-memory cache)
	tasks     *xsync.SyncedMap[string, schema.Task]
	jobs      *xsync.SyncedMap[string, schema.Job]
	tasksFile string // Path to agent_tasks.json
	jobsFile  string // Path to agent_jobs.json

	// Job execution channel
	jobQueue chan JobExecution

	// Cancellation support
	cancellations *xsync.SyncedMap[string, context.CancelFunc]

	// Cron scheduler
	cronScheduler *cron.Cron
	cronEntries   *xsync.SyncedMap[string, cron.EntryID]

	// Job retention
	retentionDays int // From runtime settings, default: 30

	// Mutex for file operations
	fileMutex sync.Mutex
}

// JobExecution represents a job to be executed
type JobExecution struct {
	Job    schema.Job
	Task   schema.Task
	Ctx    context.Context
	Cancel context.CancelFunc
}

// NewAgentJobService creates a new AgentJobService instance
func NewAgentJobService(
	appConfig *config.ApplicationConfig,
	modelLoader *model.ModelLoader,
	configLoader *config.ModelConfigLoader,
	evaluator *templates.Evaluator,
) *AgentJobService {
	retentionDays := appConfig.AgentJobRetentionDays
	if retentionDays == 0 {
		retentionDays = 30 // Default
	}

	tasksFile := ""
	jobsFile := ""
	if appConfig.DynamicConfigsDir != "" {
		tasksFile = filepath.Join(appConfig.DynamicConfigsDir, "agent_tasks.json")
		jobsFile = filepath.Join(appConfig.DynamicConfigsDir, "agent_jobs.json")
	}

	return &AgentJobService{
		appConfig:     appConfig,
		modelLoader:   modelLoader,
		configLoader:  configLoader,
		evaluator:     evaluator,
		tasks:         xsync.NewSyncedMap[string, schema.Task](),
		jobs:          xsync.NewSyncedMap[string, schema.Job](),
		tasksFile:     tasksFile,
		jobsFile:      jobsFile,
		jobQueue:      make(chan JobExecution, 100), // Buffer for 100 jobs
		cancellations: xsync.NewSyncedMap[string, context.CancelFunc](),
		cronScheduler: cron.New(cron.WithSeconds()), // Support seconds in cron
		cronEntries:   xsync.NewSyncedMap[string, cron.EntryID](),
		retentionDays: retentionDays,
	}
}

// LoadTasksFromFile loads tasks from agent_tasks.json
func (s *AgentJobService) LoadTasksFromFile() error {
	if s.tasksFile == "" {
		return nil // No file path configured
	}

	s.fileMutex.Lock()
	defer s.fileMutex.Unlock()

	if _, err := os.Stat(s.tasksFile); os.IsNotExist(err) {
		log.Debug().Msg("agent_tasks.json not found, starting with empty tasks")
		return nil
	}

	fileContent, err := os.ReadFile(s.tasksFile)
	if err != nil {
		return fmt.Errorf("failed to read tasks file: %w", err)
	}

	var tasksFile schema.TasksFile
	if err := json.Unmarshal(fileContent, &tasksFile); err != nil {
		return fmt.Errorf("failed to parse tasks file: %w", err)
	}

	// Load tasks into memory
	for _, task := range tasksFile.Tasks {
		s.tasks.Set(task.ID, task)
		// Schedule cron if enabled and has cron expression
		if task.Enabled && task.Cron != "" {
			if err := s.ScheduleCronTask(task); err != nil {
				log.Warn().Err(err).Str("task_id", task.ID).Msg("Failed to schedule cron task on load")
			}
		}
	}

	log.Info().Int("count", len(tasksFile.Tasks)).Msg("Loaded tasks from file")
	return nil
}

// SaveTasksToFile saves tasks to agent_tasks.json
func (s *AgentJobService) SaveTasksToFile() error {
	if s.tasksFile == "" {
		return nil // No file path configured
	}

	s.fileMutex.Lock()
	defer s.fileMutex.Unlock()

	tasksFile := schema.TasksFile{
		Tasks: s.tasks.Values(),
	}

	fileContent, err := json.MarshalIndent(tasksFile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal tasks: %w", err)
	}

	if err := os.WriteFile(s.tasksFile, fileContent, 0600); err != nil {
		return fmt.Errorf("failed to write tasks file: %w", err)
	}

	return nil
}

// LoadJobsFromFile loads jobs from agent_jobs.json
func (s *AgentJobService) LoadJobsFromFile() error {
	if s.jobsFile == "" {
		return nil // No file path configured
	}

	s.fileMutex.Lock()
	defer s.fileMutex.Unlock()

	if _, err := os.Stat(s.jobsFile); os.IsNotExist(err) {
		log.Debug().Msg("agent_jobs.json not found, starting with empty jobs")
		return nil
	}

	fileContent, err := os.ReadFile(s.jobsFile)
	if err != nil {
		return fmt.Errorf("failed to read jobs file: %w", err)
	}

	var jobsFile schema.JobsFile
	if err := json.Unmarshal(fileContent, &jobsFile); err != nil {
		return fmt.Errorf("failed to parse jobs file: %w", err)
	}

	// Load jobs into memory
	for _, job := range jobsFile.Jobs {
		s.jobs.Set(job.ID, job)
	}

	log.Info().Int("count", len(jobsFile.Jobs)).Msg("Loaded jobs from file")
	return nil
}

// SaveJobsToFile saves jobs to agent_jobs.json
func (s *AgentJobService) SaveJobsToFile() error {
	if s.jobsFile == "" {
		return nil // No file path configured
	}

	s.fileMutex.Lock()
	defer s.fileMutex.Unlock()

	jobsFile := schema.JobsFile{
		Jobs:        s.jobs.Values(),
		LastCleanup: time.Now(),
	}

	fileContent, err := json.MarshalIndent(jobsFile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal jobs: %w", err)
	}

	if err := os.WriteFile(s.jobsFile, fileContent, 0600); err != nil {
		return fmt.Errorf("failed to write jobs file: %w", err)
	}

	return nil
}

// CreateTask creates a new task
func (s *AgentJobService) CreateTask(task schema.Task) (string, error) {
	if task.Name == "" {
		return "", fmt.Errorf("task name is required")
	}
	if task.Model == "" {
		return "", fmt.Errorf("task model is required")
	}
	if task.Prompt == "" {
		return "", fmt.Errorf("task prompt is required")
	}

	// Generate UUID
	id := uuid.New().String()
	task.ID = id
	now := time.Now()
	task.CreatedAt = now
	task.UpdatedAt = now
	if !task.Enabled {
		task.Enabled = true // Default to enabled
	}

	// Store task
	s.tasks.Set(id, task)

	// Schedule cron if enabled and has cron expression
	if task.Enabled && task.Cron != "" {
		if err := s.ScheduleCronTask(task); err != nil {
			log.Warn().Err(err).Str("task_id", id).Msg("Failed to schedule cron task")
			// Don't fail task creation if cron scheduling fails
		}
	}

	// Save to file
	if err := s.SaveTasksToFile(); err != nil {
		log.Error().Err(err).Msg("Failed to save tasks to file")
		// Don't fail task creation if file save fails
	}

	return id, nil
}

// UpdateTask updates an existing task
func (s *AgentJobService) UpdateTask(id string, task schema.Task) error {
	if !s.tasks.Exists(id) {
		return fmt.Errorf("task not found: %s", id)
	}
	existing := s.tasks.Get(id)

	// Preserve ID and CreatedAt
	task.ID = id
	task.CreatedAt = existing.CreatedAt
	task.UpdatedAt = time.Now()

	// Unschedule old cron if it had one
	if existing.Cron != "" {
		s.UnscheduleCronTask(id)
	}

	// Store updated task
	s.tasks.Set(id, task)

	// Schedule new cron if enabled and has cron expression
	if task.Enabled && task.Cron != "" {
		if err := s.ScheduleCronTask(task); err != nil {
			log.Warn().Err(err).Str("task_id", id).Msg("Failed to schedule cron task")
		}
	}

	// Save to file
	if err := s.SaveTasksToFile(); err != nil {
		log.Error().Err(err).Msg("Failed to save tasks to file")
	}

	return nil
}

// DeleteTask deletes a task
func (s *AgentJobService) DeleteTask(id string) error {
	if !s.tasks.Exists(id) {
		return fmt.Errorf("task not found: %s", id)
	}

	// Unschedule cron
	s.UnscheduleCronTask(id)

	// Remove from memory
	s.tasks.Delete(id)

	// Save to file
	if err := s.SaveTasksToFile(); err != nil {
		log.Error().Err(err).Msg("Failed to save tasks to file")
	}

	return nil
}

// GetTask retrieves a task by ID
func (s *AgentJobService) GetTask(id string) (*schema.Task, error) {
	task := s.tasks.Get(id)
	if task.ID == "" {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	return &task, nil
}

// ListTasks returns all tasks
func (s *AgentJobService) ListTasks() []schema.Task {
	return s.tasks.Values()
}

// buildPrompt builds a prompt from a template with parameters
func (s *AgentJobService) buildPrompt(templateStr string, params map[string]string) (string, error) {
	tmpl, err := template.New("prompt").Parse(templateStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse prompt template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("failed to execute prompt template: %w", err)
	}

	return buf.String(), nil
}

// ExecuteJob creates and queues a job for execution
func (s *AgentJobService) ExecuteJob(taskID string, params map[string]string, triggeredBy string) (string, error) {
	task := s.tasks.Get(taskID)
	if task.ID == "" {
		return "", fmt.Errorf("task not found: %s", taskID)
	}

	if !task.Enabled {
		return "", fmt.Errorf("task is disabled: %s", taskID)
	}

	// Create job
	jobID := uuid.New().String()
	now := time.Now()
	job := schema.Job{
		ID:          jobID,
		TaskID:      taskID,
		Status:      schema.JobStatusPending,
		Parameters:  params,
		CreatedAt:   now,
		TriggeredBy: triggeredBy,
	}

	// Store job
	s.jobs.Set(jobID, job)

	// Save to file (async, don't block)
	go func() {
		if err := s.SaveJobsToFile(); err != nil {
			log.Error().Err(err).Msg("Failed to save jobs to file")
		}
	}()

	// Create context for cancellation
	ctx, cancel := context.WithCancel(context.Background())
	s.cancellations.Set(jobID, cancel)

	// Queue job
	select {
	case s.jobQueue <- JobExecution{
		Job:    job,
		Task:   task,
		Ctx:    ctx,
		Cancel: cancel,
	}:
	default:
		// Queue is full, update job status
		job.Status = schema.JobStatusFailed
		job.Error = "job queue is full"
		s.jobs.Set(jobID, job)
		return "", fmt.Errorf("job queue is full")
	}

	return jobID, nil
}

// GetJob retrieves a job by ID
func (s *AgentJobService) GetJob(id string) (*schema.Job, error) {
	job := s.jobs.Get(id)
	if job.ID == "" {
		return nil, fmt.Errorf("job not found: %s", id)
	}
	return &job, nil
}

// ListJobs returns jobs, optionally filtered by task_id and status
func (s *AgentJobService) ListJobs(taskID *string, status *schema.JobStatus, limit int) []schema.Job {
	allJobs := s.jobs.Values()
	filtered := []schema.Job{}

	for _, job := range allJobs {
		if taskID != nil && job.TaskID != *taskID {
			continue
		}
		if status != nil && job.Status != *status {
			continue
		}
		filtered = append(filtered, job)
	}

	// Sort by CreatedAt descending (newest first)
	for i := 0; i < len(filtered)-1; i++ {
		for j := i + 1; j < len(filtered); j++ {
			if filtered[i].CreatedAt.Before(filtered[j].CreatedAt) {
				filtered[i], filtered[j] = filtered[j], filtered[i]
			}
		}
	}

	// Apply limit
	if limit > 0 && limit < len(filtered) {
		filtered = filtered[:limit]
	}

	return filtered
}

// CancelJob cancels a running job
func (s *AgentJobService) CancelJob(id string) error {
	job := s.jobs.Get(id)
	if job.ID == "" {
		return fmt.Errorf("job not found: %s", id)
	}

	if job.Status != schema.JobStatusPending && job.Status != schema.JobStatusRunning {
		return fmt.Errorf("job cannot be cancelled: status is %s", job.Status)
	}

	// Cancel context
	if s.cancellations.Exists(id) {
		cancel := s.cancellations.Get(id)
		cancel()
		s.cancellations.Delete(id)
	}

	// Update job status
	now := time.Now()
	job.Status = schema.JobStatusCancelled
	job.CompletedAt = &now
	s.jobs.Set(id, job)

	// Save to file (async)
	go func() {
		if err := s.SaveJobsToFile(); err != nil {
			log.Error().Err(err).Msg("Failed to save jobs to file")
		}
	}()

	return nil
}

// DeleteJob deletes a job
func (s *AgentJobService) DeleteJob(id string) error {
	if !s.jobs.Exists(id) {
		return fmt.Errorf("job not found: %s", id)
	}

	s.jobs.Delete(id)

	// Save to file
	if err := s.SaveJobsToFile(); err != nil {
		log.Error().Err(err).Msg("Failed to save jobs to file")
	}

	return nil
}

// executeJobInternal executes a job using cogito
func (s *AgentJobService) executeJobInternal(job schema.Job, task schema.Task, ctx context.Context) error {
	// Update job status to running
	now := time.Now()
	job.Status = schema.JobStatusRunning
	job.StartedAt = &now
	s.jobs.Set(job.ID, job)

	// Load model config
	modelConfig, err := s.configLoader.LoadModelConfigFileByNameDefaultOptions(task.Model, s.appConfig)
	if err != nil {
		job.Status = schema.JobStatusFailed
		job.Error = fmt.Sprintf("failed to load model config: %v", err)
		completedAt := time.Now()
		job.CompletedAt = &completedAt
		s.jobs.Set(job.ID, job)
		return fmt.Errorf("failed to load model config: %w", err)
	}

	// Validate MCP configuration
	if modelConfig.MCP.Servers == "" && modelConfig.MCP.Stdio == "" {
		job.Status = schema.JobStatusFailed
		job.Error = "no MCP servers configured for model"
		completedAt := time.Now()
		job.CompletedAt = &completedAt
		s.jobs.Set(job.ID, job)
		return fmt.Errorf("no MCP servers configured for model: %s", task.Model)
	}

	// Get MCP config from model config
	remote, stdio, err := modelConfig.MCP.MCPConfigFromYAML()
	if err != nil {
		job.Status = schema.JobStatusFailed
		job.Error = fmt.Sprintf("failed to get MCP config: %v", err)
		completedAt := time.Now()
		job.CompletedAt = &completedAt
		s.jobs.Set(job.ID, job)
		return fmt.Errorf("failed to get MCP config: %w", err)
	}

	// Get MCP sessions
	sessions, err := mcpTools.SessionsFromMCPConfig(modelConfig.Name, remote, stdio)
	if err != nil {
		job.Status = schema.JobStatusFailed
		job.Error = fmt.Sprintf("failed to get MCP sessions: %v", err)
		completedAt := time.Now()
		job.CompletedAt = &completedAt
		s.jobs.Set(job.ID, job)
		return fmt.Errorf("failed to get MCP sessions: %w", err)
	}

	if len(sessions) == 0 {
		job.Status = schema.JobStatusFailed
		job.Error = "no working MCP servers found"
		completedAt := time.Now()
		job.CompletedAt = &completedAt
		s.jobs.Set(job.ID, job)
		return fmt.Errorf("no working MCP servers found")
	}

	// Build prompt from template
	prompt, err := s.buildPrompt(task.Prompt, job.Parameters)
	if err != nil {
		job.Status = schema.JobStatusFailed
		job.Error = fmt.Sprintf("failed to build prompt: %v", err)
		completedAt := time.Now()
		job.CompletedAt = &completedAt
		s.jobs.Set(job.ID, job)
		return fmt.Errorf("failed to build prompt: %w", err)
	}

	// Create cogito fragment
	fragment := cogito.NewEmptyFragment()
	fragment = fragment.AddMessage("user", prompt)

	// Get API address and key
	_, port, err := net.SplitHostPort(s.appConfig.APIAddress)
	if err != nil {
		job.Status = schema.JobStatusFailed
		job.Error = fmt.Sprintf("failed to parse API address: %v", err)
		completedAt := time.Now()
		job.CompletedAt = &completedAt
		s.jobs.Set(job.ID, job)
		return fmt.Errorf("failed to parse API address: %w", err)
	}

	apiKey := ""
	if len(s.appConfig.ApiKeys) > 0 {
		apiKey = s.appConfig.ApiKeys[0]
	}

	// Create LLM client
	defaultLLM := cogito.NewOpenAILLM(modelConfig.Name, apiKey, "http://127.0.0.1:"+port)

	// Build cogito options
	cogitoOpts := modelConfig.BuildCogitoOptions()
	cogitoOpts = append(
		cogitoOpts,
		cogito.WithContext(ctx),
		cogito.WithMCPs(sessions...),
		cogito.WithStatusCallback(func(status string) {
			log.Debug().Str("job_id", job.ID).Str("model", modelConfig.Name).Msgf("Status: %s", status)
		}),
		cogito.WithReasoningCallback(func(reasoning string) {
			log.Debug().Str("job_id", job.ID).Str("model", modelConfig.Name).Msgf("Reasoning: %s", reasoning)
		}),
		cogito.WithToolCallBack(func(t *cogito.ToolChoice) bool {
			log.Debug().Str("job_id", job.ID).Str("model", modelConfig.Name).
				Str("tool", t.Name).Str("reasoning", t.Reasoning).Interface("arguments", t.Arguments).
				Msg("Tool call")
			return true
		}),
		cogito.WithToolCallResultCallback(func(t cogito.ToolStatus) {
			log.Debug().Str("job_id", job.ID).Str("model", modelConfig.Name).
				Str("tool", t.Name).Str("result", t.Result).Interface("tool_arguments", t.ToolArguments).
				Msg("Tool call result")
		}),
	)

	// Execute tools
	f, err := cogito.ExecuteTools(defaultLLM, fragment, cogitoOpts...)
	if err != nil && !errors.Is(err, cogito.ErrNoToolSelected) {
		job.Status = schema.JobStatusFailed
		job.Error = fmt.Sprintf("failed to execute tools: %v", err)
		completedAt := time.Now()
		job.CompletedAt = &completedAt
		s.jobs.Set(job.ID, job)
		return fmt.Errorf("failed to execute tools: %w", err)
	}

	// Get final response
	f, err = defaultLLM.Ask(ctx, f)
	if err != nil {
		job.Status = schema.JobStatusFailed
		job.Error = fmt.Sprintf("failed to get response: %v", err)
		completedAt := time.Now()
		job.CompletedAt = &completedAt
		s.jobs.Set(job.ID, job)
		return fmt.Errorf("failed to get response: %w", err)
	}

	// Update job with result
	completedAt := time.Now()
	job.Status = schema.JobStatusCompleted
	job.Result = f.LastMessage().Content
	job.CompletedAt = &completedAt
	s.jobs.Set(job.ID, job)

	// Save to file (async)
	go func() {
		if err := s.SaveJobsToFile(); err != nil {
			log.Error().Err(err).Msg("Failed to save jobs to file")
		}
	}()

	// Send webhook and push results (non-blocking)
	go func() {
		if task.WebhookURL != "" {
			s.sendWebhook(job, task)
		}
		s.pushResult(job, task)
	}()

	return nil
}

// worker processes jobs from the queue
func (s *AgentJobService) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case exec := <-s.jobQueue:
			// Check if job was cancelled before execution
			select {
			case <-exec.Ctx.Done():
				job := exec.Job
				now := time.Now()
				job.Status = schema.JobStatusCancelled
				job.CompletedAt = &now
				s.jobs.Set(job.ID, job)
				s.cancellations.Delete(job.ID)
				continue
			default:
			}

			// Execute job
			err := s.executeJobInternal(exec.Job, exec.Task, exec.Ctx)
			if err != nil {
				log.Error().Err(err).Str("job_id", exec.Job.ID).Msg("Job execution failed")
			}

			// Clean up cancellation
			s.cancellations.Delete(exec.Job.ID)
		}
	}
}

// ScheduleCronTask schedules a task to run on a cron schedule
func (s *AgentJobService) ScheduleCronTask(task schema.Task) error {
	if task.Cron == "" {
		return nil // No cron expression
	}

	// Parse cron expression (support standard 5-field format)
	// Convert to 6-field format if needed (with seconds)
	cronExpr := task.Cron
	entryID, err := s.cronScheduler.AddFunc(cronExpr, func() {
		// Create job for cron execution
		_, err := s.ExecuteJob(task.ID, map[string]string{}, "cron")
		if err != nil {
			log.Error().Err(err).Str("task_id", task.ID).Msg("Failed to execute cron job")
		}
	})
	if err != nil {
		return fmt.Errorf("failed to parse cron expression: %w", err)
	}

	s.cronEntries.Set(task.ID, entryID)
	log.Info().Str("task_id", task.ID).Str("cron", cronExpr).Msg("Scheduled cron task")
	return nil
}

// UnscheduleCronTask removes a task from the cron scheduler
func (s *AgentJobService) UnscheduleCronTask(taskID string) {
	if s.cronEntries.Exists(taskID) {
		entryID := s.cronEntries.Get(taskID)
		s.cronScheduler.Remove(entryID)
		s.cronEntries.Delete(taskID)
		log.Info().Str("task_id", taskID).Msg("Unscheduled cron task")
	}
}

// sendWebhook sends a webhook notification
func (s *AgentJobService) sendWebhook(job schema.Job, task schema.Task) {
	// Build payload
	payload, err := s.buildWebhookPayload(job, task)
	if err != nil {
		log.Error().Err(err).Str("job_id", job.ID).Msg("Failed to build webhook payload")
		s.updateJobWebhookStatus(job.ID, false, err)
		return
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", task.WebhookURL, bytes.NewBuffer(payload))
	if err != nil {
		log.Error().Err(err).Str("job_id", job.ID).Msg("Failed to create webhook request")
		s.updateJobWebhookStatus(job.ID, false, err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	if task.WebhookAuth != "" {
		req.Header.Set("Authorization", task.WebhookAuth)
	}

	// Execute with retry
	client := &http.Client{Timeout: 30 * time.Second}
	err = s.executeWithRetry(client, req, job.ID, "webhook")
	if err != nil {
		s.updateJobWebhookStatus(job.ID, false, err)
		return
	}

	s.updateJobWebhookStatus(job.ID, true, nil)
}

// buildWebhookPayload builds webhook payload (default or template)
func (s *AgentJobService) buildWebhookPayload(job schema.Job, task schema.Task) ([]byte, error) {
	if task.WebhookTemplate != "" {
		// Use custom template
		return s.buildPayloadFromTemplate(job, task, task.WebhookTemplate)
	}

	// Use default format
	payload := map[string]interface{}{
		"job_id":       job.ID,
		"task_id":      job.TaskID,
		"task_name":    task.Name,
		"status":       string(job.Status),
		"result":       job.Result,
		"parameters":   job.Parameters,
		"started_at":   job.StartedAt,
		"completed_at": job.CompletedAt,
	}

	if job.Error != "" {
		payload["error"] = job.Error
	}

	return json.Marshal(payload)
}

// pushResult pushes job results to external APIs
func (s *AgentJobService) pushResult(job schema.Job, task schema.Task) {
	// Determine which result push configs to use based on job status
	var pushConfigs []schema.ResultPushConfig

	switch job.Status {
	case schema.JobStatusCompleted:
		pushConfigs = task.ResultPush
	case schema.JobStatusFailed:
		pushConfigs = task.ResultPushFailure
	default:
		return // Only push on completed or failed
	}

	if len(pushConfigs) == 0 {
		return // No result push configured for this status
	}

	// Push to all configured endpoints concurrently
	var wg sync.WaitGroup
	errors := make(chan error, len(pushConfigs))

	for _, config := range pushConfigs {
		wg.Add(1)
		go func(cfg schema.ResultPushConfig) {
			defer wg.Done()
			if err := s.pushToEndpoint(job, task, cfg); err != nil {
				errors <- err
			}
		}(config)
	}

	wg.Wait()
	close(errors)

	// Collect errors (non-blocking - job status not affected)
	var pushErrors []string
	for err := range errors {
		pushErrors = append(pushErrors, err.Error())
	}

	if len(pushErrors) > 0 {
		// Store errors but don't fail the job
		s.updateJobResultPushStatus(job.ID, false, fmt.Errorf("some endpoints failed: %v", pushErrors))
	} else {
		s.updateJobResultPushStatus(job.ID, true, nil)
	}
}

// pushToEndpoint pushes to a single endpoint
func (s *AgentJobService) pushToEndpoint(job schema.Job, task schema.Task, config schema.ResultPushConfig) error {
	// Build payload
	var payload []byte
	var err error

	if config.PayloadTemplate != "" {
		// Use custom template
		payload, err = s.buildPayloadFromTemplate(job, task, config.PayloadTemplate)
	} else {
		// Use default format
		payload, err = s.buildDefaultPayload(job, task)
	}

	if err != nil {
		return fmt.Errorf("failed to build payload: %w", err)
	}

	// Create HTTP request
	method := config.Method
	if method == "" {
		method = "POST" // Default
	}

	req, err := http.NewRequest(method, config.URL, bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	for k, v := range config.Headers {
		req.Header.Set(k, v)
	}

	// Execute with retry
	client := &http.Client{Timeout: 30 * time.Second}
	return s.executeWithRetry(client, req, job.ID, "result_push")
}

// buildPayloadFromTemplate builds payload from template
func (s *AgentJobService) buildPayloadFromTemplate(job schema.Job, task schema.Task, templateStr string) ([]byte, error) {
	// Create template context
	ctx := map[string]interface{}{
		"Job":        job,
		"Task":       task,
		"Result":     job.Result,
		"Parameters": job.Parameters,
		"Status":     string(job.Status),
	}

	// Add json function for template
	funcMap := template.FuncMap{
		"json": func(v interface{}) string {
			b, _ := json.Marshal(v)
			return string(b)
		},
	}

	tmpl, err := template.New("payload").Funcs(funcMap).Parse(templateStr)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// buildDefaultPayload builds default JSON payload
func (s *AgentJobService) buildDefaultPayload(job schema.Job, task schema.Task) ([]byte, error) {
	payload := map[string]interface{}{
		"job_id":            job.ID,
		"task_id":            job.TaskID,
		"task_name":         task.Name,
		"status":            string(job.Status),
		"result":            job.Result,
		"parameters":        job.Parameters,
		"started_at":        job.StartedAt,
		"completed_at":      job.CompletedAt,
		"completed_at_unix": int64(0),
	}

	if job.CompletedAt != nil {
		payload["completed_at_unix"] = job.CompletedAt.Unix()
	}

	if job.Error != "" {
		payload["error"] = job.Error
	}

	return json.Marshal(payload)
}

// executeWithRetry executes HTTP request with retry logic
func (s *AgentJobService) executeWithRetry(client *http.Client, req *http.Request, jobID, operation string) error {
	maxRetries := 3
	backoff := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

	for i := 0; i < maxRetries; i++ {
		// Recreate request body if needed (it may have been consumed)
		if req.Body != nil {
			bodyBytes, _ := io.ReadAll(req.Body)
			req.Body.Close()
			req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}

		resp, err := client.Do(req)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			resp.Body.Close()
			return nil // Success
		}

		if resp != nil {
			resp.Body.Close()
		}

		if i < maxRetries-1 {
			time.Sleep(backoff[i])
		}
	}

	return fmt.Errorf("failed after %d retries", maxRetries)
}

// updateJobWebhookStatus updates webhook delivery status
func (s *AgentJobService) updateJobWebhookStatus(jobID string, sent bool, err error) {
	job := s.jobs.Get(jobID)
	if job.ID == "" {
		return
	}

	job.WebhookSent = sent
	if sent {
		now := time.Now()
		job.WebhookSentAt = &now
		job.WebhookError = ""
	} else if err != nil {
		job.WebhookError = err.Error()
	}

	s.jobs.Set(jobID, job)
}

// updateJobResultPushStatus updates result push delivery status
func (s *AgentJobService) updateJobResultPushStatus(jobID string, pushed bool, err error) {
	job := s.jobs.Get(jobID)
	if job.ID == "" {
		return
	}

	job.ResultPushed = pushed
	if pushed {
		now := time.Now()
		job.ResultPushedAt = &now
		job.ResultPushError = ""
	} else if err != nil {
		job.ResultPushError = err.Error()
	}

	s.jobs.Set(jobID, job)

	// Save to file (async)
	go func() {
		if err := s.SaveJobsToFile(); err != nil {
			log.Error().Err(err).Msg("Failed to save jobs to file")
		}
	}()
}

// CleanupOldJobs removes jobs older than retention period
func (s *AgentJobService) CleanupOldJobs() error {
	cutoff := time.Now().AddDate(0, 0, -s.retentionDays)
	allJobs := s.jobs.Values()
	removed := 0

	for _, job := range allJobs {
		if job.CreatedAt.Before(cutoff) {
			s.jobs.Delete(job.ID)
			removed++
		}
	}

	if removed > 0 {
		log.Info().Int("removed", removed).Int("retention_days", s.retentionDays).Msg("Cleaned up old jobs")
		// Save to file
		if err := s.SaveJobsToFile(); err != nil {
			log.Error().Err(err).Msg("Failed to save jobs to file after cleanup")
		}
	}

	return nil
}

// Start starts the background service
func (s *AgentJobService) Start(ctx context.Context) error {
	// Load tasks and jobs from files
	if err := s.LoadTasksFromFile(); err != nil {
		log.Warn().Err(err).Msg("Failed to load tasks from file")
	}
	if err := s.LoadJobsFromFile(); err != nil {
		log.Warn().Err(err).Msg("Failed to load jobs from file")
	}

	// Start cron scheduler
	s.cronScheduler.Start()

	// Start worker pool (5 workers)
	workerCount := 5
	for i := 0; i < workerCount; i++ {
		go s.worker(ctx)
	}

	// Schedule daily cleanup at midnight
	_, err := s.cronScheduler.AddFunc("0 0 * * *", func() {
		if err := s.CleanupOldJobs(); err != nil {
			log.Error().Err(err).Msg("Failed to cleanup old jobs")
		}
	})
	if err != nil {
		log.Warn().Err(err).Msg("Failed to schedule daily cleanup")
	}

	// Run initial cleanup
	if err := s.CleanupOldJobs(); err != nil {
		log.Warn().Err(err).Msg("Failed to run initial cleanup")
	}

	log.Info().Msg("AgentJobService started")
	return nil
}

