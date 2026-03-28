package jobs

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// TaskRecord is the GORM model for persisting tasks in PostgreSQL.
type TaskRecord struct {
	ID                string    `gorm:"primaryKey;size:36" json:"id"`
	UserID            string    `gorm:"index;size:36" json:"user_id"`
	Name              string    `gorm:"index;size:255" json:"name"`
	Description       string    `gorm:"type:text" json:"description"`
	Model             string    `gorm:"size:255" json:"model"`
	Prompt            string    `gorm:"type:text" json:"prompt"`
	Enabled           bool      `gorm:"default:true" json:"enabled"`
	Cron              string    `gorm:"size:64" json:"cron,omitempty"`
	CronParametersJSON string  `gorm:"column:cron_parameters;type:text" json:"-"`
	WebhooksJSON      string   `gorm:"column:webhooks;type:text" json:"-"`
	MultimediaJSON    string   `gorm:"column:multimedia_sources;type:text" json:"-"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func (TaskRecord) TableName() string { return "tasks" }

// JobRecord is the GORM model for persisting jobs in PostgreSQL.
type JobRecord struct {
	ID              string     `gorm:"primaryKey;size:36" json:"id"`
	TaskID          string     `gorm:"index;size:36" json:"task_id"`
	UserID          string     `gorm:"index;size:36" json:"user_id"`
	Status          string     `gorm:"index;size:32;default:pending" json:"status"`
	ParametersJSON  string     `gorm:"column:parameters;type:text" json:"-"`
	Result          string     `gorm:"type:text" json:"result,omitempty"`
	Error           string     `gorm:"type:text" json:"error,omitempty"`
	TriggeredBy     string     `gorm:"size:32" json:"triggered_by"`
	FrontendID      string     `gorm:"size:36" json:"frontend_id,omitempty"`
	TracesJSON      string     `gorm:"column:traces;type:text" json:"-"`
	WebhookSent     bool       `json:"webhook_sent"`
	WebhookSentAt   *time.Time `json:"webhook_sent_at,omitempty"`
	WebhookError    string     `gorm:"type:text" json:"webhook_error,omitempty"`
	ImagesJSON      string     `gorm:"column:images;type:text" json:"-"`
	VideosJSON      string     `gorm:"column:videos;type:text" json:"-"`
	AudiosJSON      string     `gorm:"column:audios;type:text" json:"-"`
	FilesJSON       string     `gorm:"column:files;type:text" json:"-"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (JobRecord) TableName() string { return "jobs" }

// JobStore provides PostgreSQL-backed persistence for tasks and jobs.
type JobStore struct {
	db *gorm.DB
}

// NewJobStore creates a new JobStore and auto-migrates the schema.
func NewJobStore(db *gorm.DB) (*JobStore, error) {
	if err := db.AutoMigrate(&TaskRecord{}, &JobRecord{}); err != nil {
		return nil, fmt.Errorf("migrating job tables: %w", err)
	}
	return &JobStore{db: db}, nil
}

// --- Task CRUD ---

// CreateTask stores a new task.
func (s *JobStore) CreateTask(t *TaskRecord) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	t.CreatedAt = time.Now()
	t.UpdatedAt = t.CreatedAt
	return s.db.Create(t).Error
}

// UpdateTask updates an existing task.
func (s *JobStore) UpdateTask(t *TaskRecord) error {
	t.UpdatedAt = time.Now()
	return s.db.Save(t).Error
}

// SaveTask creates or updates a task (upsert).
func (s *JobStore) SaveTask(t *TaskRecord) error {
	t.UpdatedAt = time.Now()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = t.UpdatedAt
	}
	return s.db.Save(t).Error
}

// GetTask retrieves a task by ID.
func (s *JobStore) GetTask(id string) (*TaskRecord, error) {
	var t TaskRecord
	if err := s.db.First(&t, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// GetTaskByName retrieves a task by name and user.
func (s *JobStore) GetTaskByName(userID, name string) (*TaskRecord, error) {
	var t TaskRecord
	q := s.db.Where("name = ?", name)
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	if err := q.First(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// ListTasks returns all tasks for a user, or all tasks if userID is empty.
func (s *JobStore) ListTasks(userID string) ([]TaskRecord, error) {
	var tasks []TaskRecord
	q := s.db.Order("created_at DESC")
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	if err := q.Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

// DeleteTask removes a task by ID.
func (s *JobStore) DeleteTask(id string) error {
	return s.db.Where("id = ?", id).Delete(&TaskRecord{}).Error
}

// ListCronTasks returns all tasks that have a cron schedule and are enabled.
func (s *JobStore) ListCronTasks() ([]TaskRecord, error) {
	var tasks []TaskRecord
	if err := s.db.Where("cron != '' AND enabled = true").Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

// --- Job CRUD ---

// CreateJob stores a new job.
func (s *JobStore) CreateJob(j *JobRecord) error {
	if j.ID == "" {
		j.ID = uuid.New().String()
	}
	j.CreatedAt = time.Now()
	j.UpdatedAt = j.CreatedAt
	return s.db.Create(j).Error
}

// UpdateJob updates an existing job.
func (s *JobStore) UpdateJob(j *JobRecord) error {
	j.UpdatedAt = time.Now()
	return s.db.Save(j).Error
}

// SaveJob creates or updates a job (upsert).
func (s *JobStore) SaveJob(j *JobRecord) error {
	j.UpdatedAt = time.Now()
	if j.CreatedAt.IsZero() {
		j.CreatedAt = j.UpdatedAt
	}
	return s.db.Save(j).Error
}

// GetJob retrieves a job by ID.
func (s *JobStore) GetJob(id string) (*JobRecord, error) {
	var j JobRecord
	if err := s.db.First(&j, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &j, nil
}

// ListJobs returns jobs, with optional filters.
func (s *JobStore) ListJobs(userID, taskID, status string, limit int) ([]JobRecord, error) {
	var jobs []JobRecord
	q := s.db.Order("created_at DESC")
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	if taskID != "" {
		q = q.Where("task_id = ?", taskID)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

// DeleteJob removes a job by ID.
func (s *JobStore) DeleteJob(id string) error {
	return s.db.Where("id = ?", id).Delete(&JobRecord{}).Error
}

// UpdateJobStatus updates just the status (and optionally result/error) of a job.
func (s *JobStore) UpdateJobStatus(id, status, result, errMsg string) error {
	updates := map[string]any{
		"status":     status,
		"updated_at": time.Now(),
	}
	if result != "" {
		updates["result"] = result
	}
	if errMsg != "" {
		updates["error"] = errMsg
	}
	now := time.Now()
	if status == "running" {
		updates["started_at"] = &now
	}
	if status == "completed" || status == "failed" || status == "cancelled" {
		updates["completed_at"] = &now
	}
	return s.db.Model(&JobRecord{}).Where("id = ?", id).Updates(updates).Error
}

// UpdateJobTraces updates the traces JSON for a job.
func (s *JobStore) UpdateJobTraces(id string, traces []byte) error {
	return s.db.Model(&JobRecord{}).Where("id = ?", id).
		Update("traces", string(traces)).Error
}

// AppendJobTrace atomically appends a single trace entry to the job's traces JSON array.
// Uses PostgreSQL jsonb concatenation to avoid read-modify-write races.
func (s *JobStore) AppendJobTrace(jobID, traceType, traceContent string) error {
	entry := map[string]string{
		"type":    traceType,
		"content": traceContent,
	}
	entryJSON, err := json.Marshal([]map[string]string{entry})
	if err != nil {
		return err
	}
	return s.db.Exec(
		`UPDATE jobs SET traces = COALESCE(NULLIF(traces, '')::jsonb, '[]'::jsonb) || ?::jsonb, updated_at = NOW() WHERE id = ?`,
		string(entryJSON), jobID,
	).Error
}

// CleanupOldJobs removes jobs older than the given duration.
func (s *JobStore) CleanupOldJobs(retention time.Duration) (int64, error) {
	cutoff := time.Now().Add(-retention)
	result := s.db.Where("created_at < ?", cutoff).Delete(&JobRecord{})
	return result.RowsAffected, result.Error
}

// --- JSON helpers ---

// MarshalJSON marshals a value to a JSON string for storage.
func MarshalJSON(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	s := string(b)
	if s == "null" || s == "[]" || s == "{}" {
		return ""
	}
	return s
}

// UnmarshalJSON unmarshals a JSON string into the target.
func UnmarshalJSON(s string, v any) error {
	if s == "" {
		return nil
	}
	return json.Unmarshal([]byte(s), v)
}
