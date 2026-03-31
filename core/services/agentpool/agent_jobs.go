package agentpool

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/Masterminds/sprig/v3"
	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/xsync"
	"github.com/mudler/cogito"
	"github.com/mudler/cogito/clients"
	"github.com/mudler/xlog"
	"github.com/robfig/cron/v3"
)

// AgentJobService manages agent tasks and job execution
type AgentJobService struct {
	appConfig    *config.ApplicationConfig
	modelLoader  *model.ModelLoader
	configLoader *config.ModelConfigLoader
	evaluator    *templates.Evaluator

	// Storage (in-memory primary, persister for secondary persistence)
	tasks     *xsync.SyncedMap[string, schema.Task]
	jobs      *xsync.SyncedMap[string, schema.Job]
	persister JobPersister
	tasksFile string // Path to agent_tasks.json (kept for backward compat)
	jobsFile  string // Path to agent_jobs.json (kept for backward compat)
	userID    string // Scoping: empty for global (main service), set for per-user instances

	// Job execution channel
	jobQueue chan JobExecution

	// Cancellation support
	cancellations *xsync.SyncedMap[string, context.CancelFunc]

	// Cron scheduler
	cronScheduler *cron.Cron
	cronEntries   *xsync.SyncedMap[string, cron.EntryID]

	// Job retention
	retentionDays int // From runtime settings, default: 30

	// Distributed mode (nil when not in distributed mode)
	dispatcher DistributedDispatcher
	rawDBStore *jobs.JobStore // kept for DBStore() accessor

	// Service lifecycle
	ctx    context.Context
	cancel context.CancelFunc

	// Mutex for file operations
	fileMutex sync.Mutex
}

// DistributedDispatcher is the interface for distributed job dispatching via NATS.
// Satisfied by *jobs.Dispatcher.
type DistributedDispatcher interface {
	Enqueue(jobID, taskID, userID string) error
	Cancel(jobID string) error
}

// SetDistributedBackends sets the dispatcher for distributed mode.
// When set, ExecuteJob/CancelJob delegate to NATS and Start() skips local workers.
func (s *AgentJobService) SetDistributedBackends(dispatcher DistributedDispatcher) {
	s.dispatcher = dispatcher
}

// SetUserID sets the user ID for per-user scoping of DB queries.
func (s *AgentJobService) SetUserID(id string) {
	s.userID = id
}

// SetDistributedJobStore sets the database-backed job store for persisting tasks/jobs.
// When set, the DB is the source of truth instead of JSON files.
func (s *AgentJobService) SetDistributedJobStore(store *jobs.JobStore) {
	s.rawDBStore = store
	s.persister = &dbJobPersister{store: store}
}

// Dispatcher returns the distributed dispatcher (nil if not in distributed mode).
func (s *AgentJobService) Dispatcher() DistributedDispatcher {
	return s.dispatcher
}

// DBStore returns the database-backed job store (nil if not configured).
func (s *AgentJobService) DBStore() *jobs.JobStore {
	return s.rawDBStore
}

// saveTasks persists tasks via the configured persister (file or DB).
func (s *AgentJobService) saveTasks(task schema.Task) {
	if err := s.persister.SaveTask(s.userID, task); err != nil {
		xlog.Warn("Failed to persist task", "error", err, "task_id", task.ID)
	}
}

// saveJobs persists jobs via the configured persister (file or DB).
func (s *AgentJobService) saveJobs(job schema.Job) {
	if err := s.persister.SaveJob(s.userID, job); err != nil {
		xlog.Warn("Failed to persist job", "error", err, "job_id", job.ID)
	}
}

// LoadFromDB populates the in-memory maps from the database.
// LoadFromDB loads tasks and jobs from the configured persister.
// Kept for backward compatibility with user_services.go.
func (s *AgentJobService) LoadFromDB() {
	s.loadFromPersister()
}

// loadFromPersister loads tasks and jobs from the configured persister into memory.
func (s *AgentJobService) loadFromPersister() {
	if tasks, err := s.persister.LoadTasks(s.userID); err != nil {
		xlog.Warn("Failed to load tasks from persister", "error", err)
	} else {
		for _, task := range tasks {
			s.tasks.Set(task.ID, task)
			if task.Enabled && task.Cron != "" {
				if err := s.ScheduleCronTask(task); err != nil {
					xlog.Warn("Failed to schedule cron task on load", "error", err, "task_id", task.ID)
				}
			}
		}
		xlog.Info("Loaded tasks from persister", "count", len(tasks))
	}

	if loadedJobs, err := s.persister.LoadJobs(s.userID); err != nil {
		xlog.Warn("Failed to load jobs from persister", "error", err)
	} else {
		for _, job := range loadedJobs {
			s.jobs.Set(job.ID, job)
		}
		xlog.Info("Loaded jobs from persister", "count", len(loadedJobs))
	}
}

// JobExecution represents a job to be executed
type JobExecution struct {
	Job    schema.Job
	Task   schema.Task
	Ctx    context.Context
	Cancel context.CancelFunc
}

const (
	JobImageType = "image"
	JobVideoType = "video"
	JobAudioType = "audio"
	JobFileType  = "file"
)

// NewAgentJobService creates a new AgentJobService instance
func NewAgentJobService(
	appConfig *config.ApplicationConfig,
	modelLoader *model.ModelLoader,
	configLoader *config.ModelConfigLoader,
	evaluator *templates.Evaluator,
) *AgentJobService {
	// Determine storage directory: DataPath > DynamicConfigsDir
	tasksFile := ""
	jobsFile := ""
	dataDir := appConfig.DataPath
	if dataDir == "" {
		dataDir = appConfig.DynamicConfigsDir
	}
	if dataDir != "" {
		tasksFile = filepath.Join(dataDir, "agent_tasks.json")
		jobsFile = filepath.Join(dataDir, "agent_jobs.json")
	}

	return NewAgentJobServiceWithPaths(appConfig, modelLoader, configLoader, evaluator, tasksFile, jobsFile)
}

// NewAgentJobServiceWithPaths creates a new AgentJobService with explicit file paths.
func NewAgentJobServiceWithPaths(
	appConfig *config.ApplicationConfig,
	modelLoader *model.ModelLoader,
	configLoader *config.ModelConfigLoader,
	evaluator *templates.Evaluator,
	tasksFile, jobsFile string,
) *AgentJobService {
	retentionDays := cmp.Or(appConfig.AgentJobRetentionDays, 30)

	tasks := xsync.NewSyncedMap[string, schema.Task]()
	jobsMap := xsync.NewSyncedMap[string, schema.Job]()

	return &AgentJobService{
		appConfig:    appConfig,
		modelLoader:  modelLoader,
		configLoader: configLoader,
		evaluator:    evaluator,
		tasks:        tasks,
		jobs:         jobsMap,
		persister: &fileJobPersister{
			tasks:     tasks,
			jobs:      jobsMap,
			tasksFile: tasksFile,
			jobsFile:  jobsFile,
		},
		tasksFile:     tasksFile,
		jobsFile:      jobsFile,
		jobQueue:      make(chan JobExecution, 100), // Buffer for 100 jobs
		cancellations: xsync.NewSyncedMap[string, context.CancelFunc](),
		cronScheduler: cron.New(), // Support seconds in cron
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
		xlog.Debug("agent_tasks.json not found, starting with empty tasks")
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

	for _, task := range tasksFile.Tasks {
		s.tasks.Set(task.ID, task)
		// Schedule cron if enabled and has cron expression
		if task.Enabled && task.Cron != "" {
			if err := s.ScheduleCronTask(task); err != nil {
				xlog.Warn("Failed to schedule cron task on load", "error", err, "task_id", task.ID)
			}
		}
	}

	xlog.Info("Loaded tasks from file", "count", len(tasksFile.Tasks))

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
		xlog.Debug("agent_jobs.json not found, starting with empty jobs")
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

	xlog.Info("Loaded jobs from file", "count", len(jobsFile.Jobs))
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
			xlog.Warn("Failed to schedule cron task", "error", err, "task_id", id)
		}
	}

	s.saveTasks(task)
	return id, nil
}

// UpdateTask updates an existing task
func (s *AgentJobService) UpdateTask(id string, task schema.Task) error {
	if !s.tasks.Exists(id) {
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
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
			xlog.Warn("Failed to schedule cron task", "error", err, "task_id", id)
		}
	}

	s.saveTasks(task)
	return nil
}

// DeleteTask deletes a task
func (s *AgentJobService) DeleteTask(id string) error {
	if !s.tasks.Exists(id) {
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}

	// Unschedule cron
	s.UnscheduleCronTask(id)

	// Remove from memory
	s.tasks.Delete(id)

	if err := s.persister.DeleteTask(id); err != nil {
		xlog.Warn("Failed to delete task from persister", "error", err, "task_id", id)
	}

	return nil
}

// GetTask retrieves a task by ID
func (s *AgentJobService) GetTask(id string) (*schema.Task, error) {
	task := s.tasks.Get(id)
	if task.ID == "" {
		return nil, fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	return &task, nil
}

// ListTasks returns all tasks, sorted by creation date (newest first)
func (s *AgentJobService) ListTasks() []schema.Task {
	tasks := s.tasks.Values()
	// Sort by CreatedAt descending (newest first), then by Name for stability
	slices.SortFunc(tasks, func(a, b schema.Task) int {
		if a.CreatedAt.Equal(b.CreatedAt) {
			return cmp.Compare(a.Name, b.Name)
		}
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return tasks
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
// multimedia can be nil for backward compatibility
func (s *AgentJobService) ExecuteJob(taskID string, params map[string]string, triggeredBy string, multimedia *schema.MultimediaAttachment) (string, error) {
	task := s.tasks.Get(taskID)
	if task.ID == "" {
		return "", fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
	}

	if !task.Enabled {
		return "", fmt.Errorf("%w: %s", ErrTaskDisabled, taskID)
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

	// Handle multimedia: merge task-level (for cron) and job-level (for manual execution)
	if triggeredBy == "cron" && len(task.MultimediaSources) > 0 {
		// Fetch multimedia from task sources
		job.Images = []string{}
		job.Videos = []string{}
		job.Audios = []string{}
		job.Files = []string{}

		for _, source := range task.MultimediaSources {
			// Fetch content from URL with custom headers
			dataURI, err := s.fetchMultimediaFromURL(source.URL, source.Headers, source.Type)
			if err != nil {
				xlog.Warn("Failed to fetch multimedia from task source", "error", err, "url", source.URL, "type", source.Type)
				continue
			}

			// Add to appropriate slice based on type
			switch source.Type {
			case JobImageType:
				job.Images = append(job.Images, dataURI)
			case JobVideoType:
				job.Videos = append(job.Videos, dataURI)
			case JobAudioType:
				job.Audios = append(job.Audios, dataURI)
			case JobFileType:
				job.Files = append(job.Files, dataURI)
			}
		}
	}

	// Override with job-level multimedia if provided (manual execution takes precedence)
	if multimedia != nil {
		if len(multimedia.Images) > 0 {
			job.Images = multimedia.Images
		}
		if len(multimedia.Videos) > 0 {
			job.Videos = multimedia.Videos
		}
		if len(multimedia.Audios) > 0 {
			job.Audios = multimedia.Audios
		}
		if len(multimedia.Files) > 0 {
			job.Files = multimedia.Files
		}
	}

	// Store job
	s.jobs.Set(jobID, job)

	// Distributed mode: delegate to NATS dispatcher
	if s.dispatcher != nil {
		go func() { s.saveJobs(job) }()
		xlog.Info("Enqueuing job via distributed dispatcher", "job_id", jobID, "task_id", taskID)
		return jobID, s.dispatcher.Enqueue(jobID, taskID, "")
	}

	go func() { s.saveJobs(job) }()

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
		return "", ErrJobQueueFull
	}

	return jobID, nil
}

// GetJob retrieves a job by ID
func (s *AgentJobService) GetJob(id string) (*schema.Job, error) {
	// Try authoritative read from persister (DB returns fresh data; file returns nil)
	if job, err := s.persister.GetJob(id); err == nil && job != nil {
		s.jobs.Set(job.ID, *job) // sync back to in-memory
		return job, nil
	}
	// Fall back to in-memory
	job := s.jobs.Get(id)
	if job.ID == "" {
		return nil, fmt.Errorf("%w: %s", ErrJobNotFound, id)
	}
	return &job, nil
}

// ListJobs returns jobs, optionally filtered by task_id and status
func (s *AgentJobService) ListJobs(taskID *string, status *schema.JobStatus, limit int) []schema.Job {
	// Try authoritative list from persister (DB returns fresh data; file returns nil)
	taskFilter := ""
	if taskID != nil {
		taskFilter = *taskID
	}
	statusFilter := ""
	if status != nil {
		statusFilter = string(*status)
	}
	if result, err := s.persister.ListJobs(s.userID, taskFilter, statusFilter, limit); err == nil && result != nil {
		for i := range result {
			s.jobs.Set(result[i].ID, result[i]) // sync back
		}
		return result
	}
	// Fall back to in-memory filtering
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
	slices.SortFunc(filtered, func(a, b schema.Job) int { return b.CreatedAt.Compare(a.CreatedAt) })

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
		return fmt.Errorf("%w: %s", ErrJobNotFound, id)
	}

	if job.Status != schema.JobStatusPending && job.Status != schema.JobStatusRunning {
		return fmt.Errorf("job cannot be cancelled: status is %s", job.Status)
	}

	// Distributed mode: delegate cancel via NATS
	if s.dispatcher != nil {
		return s.dispatcher.Cancel(id)
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

	go func() { s.saveJobs(job) }()

	return nil
}

// DeleteJob deletes a job
func (s *AgentJobService) DeleteJob(id string) error {
	if !s.jobs.Exists(id) {
		return fmt.Errorf("%w: %s", ErrJobNotFound, id)
	}

	s.jobs.Delete(id)

	if err := s.persister.DeleteJob(id); err != nil {
		xlog.Warn("Failed to delete job from persister", "error", err, "job_id", id)
	}

	return nil
}

// traceCollector synchronises concurrent cogito callback writes to job traces.
type traceCollector struct {
	mu     sync.Mutex
	jobID  string
	traces []schema.JobTrace
	jobs   *xsync.SyncedMap[string, schema.Job]
}

func (tc *traceCollector) add(trace schema.JobTrace) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.traces = append(tc.traces, trace)
	if tc.jobs.Exists(tc.jobID) {
		job := tc.jobs.Get(tc.jobID)
		job.Traces = make([]schema.JobTrace, len(tc.traces))
		copy(job.Traces, tc.traces)
		tc.jobs.Set(tc.jobID, job)
	}
}

type multimediaContent struct {
	url       string
	mediaType string
}

func (mu multimediaContent) URL() string {
	return mu.url
}

// fetchMultimediaFromURL fetches multimedia content from a URL with custom headers
// and converts it to a data URI string
func (s *AgentJobService) fetchMultimediaFromURL(url string, headers map[string]string, mediaType string) (string, error) {
	// Create HTTP request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set custom headers
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	// Execute request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	// Read content
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	// Encode to base64
	encoded := base64.StdEncoding.EncodeToString(data)

	// Determine MIME type
	mimeType := s.getMimeTypeForMediaType(mediaType)
	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		mimeType = contentType
	}

	// Return as data URI
	return fmt.Sprintf("data:%s;base64,%s", mimeType, encoded), nil
}

// getMimeTypeForMediaType returns the default MIME type for a media type
func (s *AgentJobService) getMimeTypeForMediaType(mediaType string) string {
	switch mediaType {
	case JobImageType:
		return "image/png"
	case JobVideoType:
		return "video/mp4"
	case JobAudioType:
		return "audio/mpeg"
	case JobFileType:
		return "application/octet-stream"
	default:
		return "application/octet-stream"
	}
}

// convertToMultimediaContent converts a slice of strings (URLs or base64) to multimediaContent objects
func (s *AgentJobService) convertToMultimediaContent(items []string, mediaType string) ([]cogito.Multimedia, error) {
	result := make([]cogito.Multimedia, 0, len(items))

	for _, item := range items {
		if item == "" {
			continue
		}

		// Check if it's already a data URI
		if strings.HasPrefix(item, "data:") {
			result = append(result, multimediaContent{url: item, mediaType: mediaType})
			continue
		}

		// Check if it's a URL
		if strings.HasPrefix(item, "http://") || strings.HasPrefix(item, "https://") {
			// Pass URL directly to cogito (it handles fetching)
			result = append(result, multimediaContent{url: item, mediaType: mediaType})
			continue
		}

		// Assume it's base64 without data URI prefix
		// Add appropriate prefix based on media type
		mimeType := s.getMimeTypeForMediaType(mediaType)
		dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, item)
		result = append(result, multimediaContent{url: dataURI, mediaType: mediaType})
	}

	return result, nil
}

// ExecuteJobInternal executes a job using cogito.
// Exported so the distributed Dispatcher can bridge NATS-dequeued jobs back into this service.
func (s *AgentJobService) ExecuteJobInternal(job schema.Job, task schema.Task, ctx context.Context) error {
	// Update job status to running
	now := time.Now()
	job.Status = schema.JobStatusRunning
	job.StartedAt = &now
	s.jobs.Set(job.ID, job)
	xlog.Info("Job started", "job_id", job.ID, "task_id", job.TaskID)

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

	// Collect all multimedia content
	multimediaItems := []cogito.Multimedia{}

	// Convert images
	if len(job.Images) > 0 {
		images, err := s.convertToMultimediaContent(job.Images, JobImageType)
		if err != nil {
			xlog.Warn("Failed to convert images", "error", err, "job_id", job.ID)
		} else {
			multimediaItems = append(multimediaItems, images...)
		}
	}

	// Convert videos
	if len(job.Videos) > 0 {
		videos, err := s.convertToMultimediaContent(job.Videos, JobVideoType)
		if err != nil {
			xlog.Warn("Failed to convert videos", "error", err, "job_id", job.ID)
		} else {
			multimediaItems = append(multimediaItems, videos...)
		}
	}

	// Convert audios
	if len(job.Audios) > 0 {
		audios, err := s.convertToMultimediaContent(job.Audios, JobAudioType)
		if err != nil {
			xlog.Warn("Failed to convert audios", "error", err, "job_id", job.ID)
		} else {
			multimediaItems = append(multimediaItems, audios...)
		}
	}

	// Convert files
	if len(job.Files) > 0 {
		files, err := s.convertToMultimediaContent(job.Files, JobFileType)
		if err != nil {
			xlog.Warn("Failed to convert files", "error", err, "job_id", job.ID)
		} else {
			multimediaItems = append(multimediaItems, files...)
		}
	}

	fragment = fragment.AddMessage("user", prompt, multimediaItems...)

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
	defaultLLM := clients.NewLocalAILLM(modelConfig.Name, apiKey, "http://127.0.0.1:"+port)

	// Initialize traces slice
	job.Traces = []schema.JobTrace{}

	tc := &traceCollector{
		jobID:  job.ID,
		traces: job.Traces,
		jobs:   s.jobs,
	}

	// Build cogito options
	cogitoOpts := modelConfig.BuildCogitoOptions()
	cogitoOpts = append(
		cogitoOpts,
		cogito.WithContext(ctx),
		cogito.WithMCPs(sessions...),
		cogito.WithStatusCallback(func(status string) {
			xlog.Debug("Status", "job_id", job.ID, "model", modelConfig.Name, "status", status)
			// Store trace
			trace := schema.JobTrace{
				Type:      "status",
				Content:   status,
				Timestamp: time.Now(),
			}
			tc.add(trace)
		}),
		cogito.WithReasoningCallback(func(reasoning string) {
			xlog.Debug("Reasoning", "job_id", job.ID, "model", modelConfig.Name, "reasoning", reasoning)
			// Store trace
			trace := schema.JobTrace{
				Type:      "reasoning",
				Content:   reasoning,
				Timestamp: time.Now(),
			}
			tc.add(trace)
		}),
		cogito.WithToolCallBack(func(t *cogito.ToolChoice, state *cogito.SessionState) cogito.ToolCallDecision {
			xlog.Debug("Tool call", "job_id", job.ID, "model", modelConfig.Name, "tool", t.Name, "reasoning", t.Reasoning, "arguments", t.Arguments)
			// Store trace
			arguments := make(map[string]any)
			if t.Arguments != nil {
				arguments = t.Arguments
			}
			trace := schema.JobTrace{
				Type:      "tool_call",
				Content:   t.Reasoning,
				Timestamp: time.Now(),
				ToolName:  t.Name,
				Arguments: arguments,
			}
			tc.add(trace)
			return cogito.ToolCallDecision{
				Approved: true,
			}
		}),
		cogito.WithToolCallResultCallback(func(t cogito.ToolStatus) {
			xlog.Debug("Tool call result", "job_id", job.ID, "model", modelConfig.Name, "tool", t.Name, "result", t.Result, "tool_arguments", t.ToolArguments)
			// Store trace
			arguments := make(map[string]any)
			// Convert ToolArguments to map via JSON marshaling
			if toolArgsBytes, err := json.Marshal(t.ToolArguments); err == nil {
				var toolArgsMap map[string]any
				if err := json.Unmarshal(toolArgsBytes, &toolArgsMap); err == nil {
					arguments = toolArgsMap
				}
			}
			arguments["result"] = t.Result
			trace := schema.JobTrace{
				Type:      "tool_result",
				Content:   t.Result,
				Timestamp: time.Now(),
				ToolName:  t.Name,
				Arguments: arguments,
			}
			tc.add(trace)
		}),
		cogito.WithStreamCallback(func(ev cogito.StreamEvent) {
			switch ev.Type {
			case cogito.StreamEventReasoning:
				trace := schema.JobTrace{
					Type:      "stream_reasoning",
					Content:   ev.Content,
					Timestamp: time.Now(),
				}
				tc.add(trace)
			case cogito.StreamEventContent:
				trace := schema.JobTrace{
					Type:      "stream_content",
					Content:   ev.Content,
					Timestamp: time.Now(),
				}
				tc.add(trace)
			case cogito.StreamEventToolCall:
				trace := schema.JobTrace{
					Type:      "stream_tool_call",
					Content:   ev.ToolArgs,
					ToolName:  ev.ToolName,
					Timestamp: time.Now(),
				}
				tc.add(trace)
			}
		}),
	)

	// Execute tools
	f, err := cogito.ExecuteTools(defaultLLM, fragment, cogitoOpts...)

	// Re-read job from the store to pick up traces written by callbacks
	if updated := s.jobs.Get(job.ID); updated.ID != "" {
		job = updated
	}

	if err != nil && !errors.Is(err, cogito.ErrNoToolSelected) {
		job.Status = schema.JobStatusFailed
		job.Error = fmt.Sprintf("failed to execute tools: %v", err)
		completedAt := time.Now()
		job.CompletedAt = &completedAt
		s.jobs.Set(job.ID, job)
		return fmt.Errorf("failed to execute tools: %w", err)
	}

	// Extract traces from fragment.Status after execution
	// This provides complete information about tool calls and results
	// We use Status data to supplement/replace callback data for completeness
	if f.Status != nil {
		// Clear existing tool_call and tool_result traces (from callbacks) and replace with Status data
		// Keep status and reasoning traces from callbacks
		filteredTraces := []schema.JobTrace{}
		for _, trace := range job.Traces {
			if trace.Type != "tool_call" && trace.Type != "tool_result" {
				filteredTraces = append(filteredTraces, trace)
			}
		}
		job.Traces = filteredTraces

		// Extract tool calls from Status.ToolsCalled
		if len(f.Status.ToolsCalled) > 0 {
			for _, toolCallInterface := range f.Status.ToolsCalled {
				// Marshal to JSON and unmarshal to extract fields
				if toolCallBytes, err := json.Marshal(toolCallInterface); err == nil {
					var toolCallData map[string]any
					if err := json.Unmarshal(toolCallBytes, &toolCallData); err == nil {
						arguments := make(map[string]any)
						if args, ok := toolCallData["arguments"].(map[string]any); ok {
							arguments = args
						}
						reasoning := ""
						if r, ok := toolCallData["reasoning"].(string); ok {
							reasoning = r
						}
						name := ""
						if n, ok := toolCallData["name"].(string); ok {
							name = n
						}
						trace := schema.JobTrace{
							Type:      "tool_call",
							Content:   reasoning,
							Timestamp: time.Now(),
							ToolName:  name,
							Arguments: arguments,
						}
						job.Traces = append(job.Traces, trace)
					}
				}
			}
		}

		// Extract tool results from Status.ToolResults
		if len(f.Status.ToolResults) > 0 {
			for _, toolResult := range f.Status.ToolResults {
				arguments := make(map[string]any)
				// Convert ToolArguments to map via JSON marshaling
				if toolArgsBytes, err := json.Marshal(toolResult.ToolArguments); err == nil {
					var toolArgsMap map[string]any
					if err := json.Unmarshal(toolArgsBytes, &toolArgsMap); err == nil {
						arguments = toolArgsMap
					}
				}
				arguments["result"] = toolResult.Result
				trace := schema.JobTrace{
					Type:      "tool_result",
					Content:   toolResult.Result,
					Timestamp: time.Now(),
					ToolName:  toolResult.Name,
					Arguments: arguments,
				}
				job.Traces = append(job.Traces, trace)
			}
		}
	}

	// Update job with result
	completedAt := time.Now()
	job.Status = schema.JobStatusCompleted
	job.Result = f.LastMessage().Content
	job.CompletedAt = &completedAt
	s.jobs.Set(job.ID, job)
	xlog.Info("Job completed", "job_id", job.ID, "status", job.Status)

	go func() { s.saveJobs(job) }()

	// Send webhooks (non-blocking)
	go func() {
		s.sendWebhooks(job, task)
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
			err := s.ExecuteJobInternal(exec.Job, exec.Task, exec.Ctx)
			if err != nil {
				xlog.Error("Job execution failed", "error", err, "job_id", exec.Job.ID)
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
	// Use cron parameters if provided, otherwise use empty map
	cronParams := task.CronParameters
	if cronParams == nil {
		cronParams = map[string]string{}
	}
	entryID, err := s.cronScheduler.AddFunc(cronExpr, func() {
		// Create job for cron execution with configured parameters
		// Multimedia will be fetched from task sources in ExecuteJob
		_, err := s.ExecuteJob(task.ID, cronParams, "cron", nil)
		if err != nil {
			xlog.Error("Failed to execute cron job", "error", err, "task_id", task.ID)
		}
	})
	if err != nil {
		return fmt.Errorf("failed to parse cron expression: %w", err)
	}

	s.cronEntries.Set(task.ID, entryID)
	xlog.Info("Scheduled cron task", "task_id", task.ID, "cron", cronExpr)
	return nil
}

// UnscheduleCronTask removes a task from the cron scheduler
func (s *AgentJobService) UnscheduleCronTask(taskID string) {
	if s.cronEntries.Exists(taskID) {
		entryID := s.cronEntries.Get(taskID)
		s.cronScheduler.Remove(entryID)
		s.cronEntries.Delete(taskID)
		xlog.Info("Unscheduled cron task", "task_id", taskID)
	}
}

// sendWebhooks sends webhook notifications to all configured webhooks
func (s *AgentJobService) sendWebhooks(job schema.Job, task schema.Task) {
	// Collect all webhook configs from new format
	webhookConfigs := task.Webhooks

	if len(webhookConfigs) == 0 {
		return // No webhooks configured
	}

	xlog.Info("Sending webhooks", "job_id", job.ID, "webhook_count", len(webhookConfigs))

	// Send all webhooks concurrently and track results
	var wg sync.WaitGroup
	errors := make(chan webhookError, len(webhookConfigs))
	var successCount atomic.Int32

	for _, webhookConfig := range webhookConfigs {
		wg.Go(func() {
			if err := s.sendWebhook(job, task, webhookConfig); err != nil {
				errors <- webhookError{
					URL:   webhookConfig.URL,
					Error: err.Error(),
				}
			} else {
				successCount.Add(1)
			}
		})
	}
	wg.Wait()
	close(errors)

	// Collect errors
	var webhookErrors []string
	for err := range errors {
		webhookErrors = append(webhookErrors, fmt.Sprintf("%s: %s", err.URL, err.Error))
	}

	// Update job with webhook status
	job = s.jobs.Get(job.ID)
	if job.ID == "" {
		return
	}

	now := time.Now()
	if len(webhookErrors) == 0 {
		// All webhooks succeeded
		job.WebhookSent = true
		job.WebhookSentAt = &now
		job.WebhookError = ""
	} else if successCount.Load() > 0 {
		// Some succeeded, some failed
		job.WebhookSent = true
		job.WebhookSentAt = &now
		job.WebhookError = fmt.Sprintf("Some webhooks failed (%d/%d succeeded): %s", int(successCount.Load()), len(webhookConfigs), strings.Join(webhookErrors, "; "))
	} else {
		// All failed
		job.WebhookSent = false
		job.WebhookError = fmt.Sprintf("All webhooks failed: %s", strings.Join(webhookErrors, "; "))
	}

	s.jobs.Set(job.ID, job)

	go func() { s.saveJobs(job) }()
}

// webhookError represents a webhook delivery error
type webhookError struct {
	URL   string
	Error string
}

// sendWebhook sends a single webhook notification
// Returns an error if the webhook delivery failed
func (s *AgentJobService) sendWebhook(job schema.Job, task schema.Task, webhookConfig schema.WebhookConfig) error {
	// Build payload
	payload, err := s.buildWebhookPayload(job, task, webhookConfig)
	if err != nil {
		xlog.Error("Failed to build webhook payload", "error", err, "job_id", job.ID, "webhook_url", webhookConfig.URL)
		return fmt.Errorf("failed to build payload: %w", err)
	}

	xlog.Debug("Sending webhook", "job_id", job.ID, "webhook_url", webhookConfig.URL, "payload", string(payload))

	// Determine HTTP method (default to POST)
	method := webhookConfig.Method
	if method == "" {
		method = "POST"
	}

	// Create HTTP request
	req, err := http.NewRequest(method, webhookConfig.URL, bytes.NewBuffer(payload))
	if err != nil {
		xlog.Error("Failed to create webhook request", "error", err, "job_id", job.ID, "webhook_url", webhookConfig.URL)
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	for key, value := range webhookConfig.Headers {
		req.Header.Set(key, value)
	}

	// Execute with retry
	client := &http.Client{Timeout: 30 * time.Second}
	err = s.executeWithRetry(client, req)
	if err != nil {
		xlog.Error("Webhook delivery failed", "error", err, "job_id", job.ID, "webhook_url", webhookConfig.URL)
		return fmt.Errorf("webhook delivery failed: %w", err)
	}

	xlog.Info("Webhook delivered successfully", "job_id", job.ID, "webhook_url", webhookConfig.URL)
	return nil
}

// buildWebhookPayload builds webhook payload (default or template)
func (s *AgentJobService) buildWebhookPayload(job schema.Job, task schema.Task, webhookConfig schema.WebhookConfig) ([]byte, error) {
	if webhookConfig.PayloadTemplate != "" {
		// Use custom template
		return s.buildPayloadFromTemplate(job, task, webhookConfig.PayloadTemplate)
	}

	// Use default format
	// Include Error field (empty string if no error)
	payload := map[string]any{
		"job_id":       job.ID,
		"task_id":      job.TaskID,
		"task_name":    task.Name,
		"status":       string(job.Status),
		"result":       job.Result,
		"error":        job.Error, // Empty string if no error
		"parameters":   job.Parameters,
		"started_at":   job.StartedAt,
		"completed_at": job.CompletedAt,
	}

	return json.Marshal(payload)
}

// buildPayloadFromTemplate builds payload from template
func (s *AgentJobService) buildPayloadFromTemplate(job schema.Job, task schema.Task, templateStr string) ([]byte, error) {
	// Create template context
	// Available variables:
	// - .Job - Job object with all fields
	// - .Task - Task object
	// - .Result - Job result (if successful)
	// - .Error - Error message (if failed, empty string if successful)
	// - .Status - Job status string
	ctx := map[string]any{
		"Job":        job,
		"Task":       task,
		"Result":     job.Result,
		"Error":      job.Error,
		"Parameters": job.Parameters,
		"Status":     string(job.Status),
	}

	// Add json function for template
	funcMap := template.FuncMap{
		"json": func(v any) string {
			b, _ := json.Marshal(v)
			return string(b)
		},
	}

	tmpl, err := template.New("payload").Funcs(funcMap).Funcs(sprig.FuncMap()).Parse(templateStr)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// executeWithRetry executes HTTP request with retry logic
func (s *AgentJobService) executeWithRetry(client *http.Client, req *http.Request) error {
	maxRetries := 3
	backoff := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

	// Save body bytes once before the loop so retries can re-use them
	var savedBody []byte
	if req.Body != nil {
		var err error
		savedBody, err = io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return fmt.Errorf("reading request body: %w", err)
		}
	}

	var lastErr error
	for i := range maxRetries {
		// Reset body for each attempt
		if savedBody != nil {
			req.Body = io.NopCloser(bytes.NewReader(savedBody))
			req.ContentLength = int64(len(savedBody))
		}

		var resp *http.Response
		resp, lastErr = client.Do(req)
		if lastErr == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			resp.Body.Close()
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		if i < maxRetries-1 {
			time.Sleep(backoff[i])
		}
	}

	return fmt.Errorf("failed after %d retries: %w", maxRetries, lastErr)
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

	// Also clean up from the persister (DB cleans via SQL, file re-serializes)
	retention := time.Duration(s.retentionDays) * 24 * time.Hour
	if dbRemoved, err := s.persister.CleanupOldJobs(retention); err != nil {
		xlog.Warn("Failed to cleanup old jobs from persister", "error", err)
	} else if dbRemoved > 0 {
		removed += int(dbRemoved)
	}

	if removed > 0 {
		xlog.Info("Cleaned up old jobs", "removed", removed, "retention_days", s.retentionDays)
		// For file persister, re-serialize the map after deletions
		s.saveJobs(schema.Job{})
	}

	return nil
}

// Start starts the background service
func (s *AgentJobService) Start(ctx context.Context) error {
	// Create service context
	s.ctx, s.cancel = context.WithCancel(ctx)

	// Update retention days from config
	s.retentionDays = cmp.Or(s.appConfig.AgentJobRetentionDays, 30)

	// Load tasks and jobs from persister (DB or files)
	s.loadFromPersister()

	// In distributed mode, the Dispatcher handles worker pool + cron leader election.
	// Skip local worker pool and cron scheduler.
	if s.dispatcher != nil {
		xlog.Info("AgentJobService started (distributed mode — local workers/cron disabled)", "retention_days", s.retentionDays)
		return nil
	}

	// Start cron scheduler
	s.cronScheduler.Start()

	// Start worker pool (5 workers)
	workerCount := 5
	for range workerCount {
		go s.worker(s.ctx)
	}

	// Schedule daily cleanup at midnight
	_, err := s.cronScheduler.AddFunc("0 0 * * *", func() {
		if err := s.CleanupOldJobs(); err != nil {
			xlog.Error("Failed to cleanup old jobs", "error", err)
		}
	})
	if err != nil {
		xlog.Warn("Failed to schedule daily cleanup", "error", err)
	}

	// Run initial cleanup
	if err := s.CleanupOldJobs(); err != nil {
		xlog.Warn("Failed to run initial cleanup", "error", err)
	}

	xlog.Info("AgentJobService started", "retention_days", s.retentionDays)
	return nil
}

// Stop stops the agent job service
func (s *AgentJobService) Stop() error {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.cronScheduler != nil {
		s.cronScheduler.Stop()
	}
	xlog.Info("AgentJobService stopped")
	return nil
}

// UpdateRetentionDays updates the retention days setting
func (s *AgentJobService) UpdateRetentionDays(days int) {
	s.retentionDays = cmp.Or(days, 30)
	xlog.Info("Updated agent job retention days", "retention_days", s.retentionDays)
}
