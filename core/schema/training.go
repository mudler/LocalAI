package schema

import "time"

// TrainingJobStatus represents the status of a training job
type TrainingJobStatus string

const (
	TrainingJobStatusPending   TrainingJobStatus = "pending"
	TrainingJobStatusRunning   TrainingJobStatus = "running"
	TrainingJobStatusCompleted TrainingJobStatus = "completed"
	TrainingJobStatusFailed    TrainingJobStatus = "failed"
	TrainingJobStatusCancelled TrainingJobStatus = "cancelled"
)

// TrainingJob represents a fine-tuning job
type TrainingJob struct {
	ID           string            `json:"id"`
	Model        string            `json:"model"`
	Backend      string            `json:"backend,omitempty"`
	Dataset      string            `json:"dataset"`
	OutputDir    string            `json:"output_dir"`
	Status       TrainingJobStatus `json:"status"`
	Progress     float32           `json:"progress"`
	CurrentEpoch int32             `json:"current_epoch"`
	TotalEpochs  int32             `json:"total_epochs"`
	CurrentStep  int32             `json:"current_step"`
	TotalSteps   int32             `json:"total_steps"`
	Loss         float32           `json:"loss"`
	LearningRate float32           `json:"learning_rate_current"`
	Message      string            `json:"message"`
	Error        string            `json:"error,omitempty"`
	OutputPath   string            `json:"output_path,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	StartedAt    *time.Time        `json:"started_at,omitempty"`
	CompletedAt  *time.Time        `json:"completed_at,omitempty"`
	Parameters   TrainingParams    `json:"parameters"`
}

// TrainingParams holds the training hyperparameters
type TrainingParams struct {
	Epochs        int32    `json:"epochs,omitempty"`
	BatchSize     int32    `json:"batch_size,omitempty"`
	LearningRate  float32  `json:"learning_rate,omitempty"`
	MaxSeqLength  int32    `json:"max_seq_length,omitempty"`
	LoraRank      int32    `json:"lora_rank,omitempty"`
	LoraAlpha     int32    `json:"lora_alpha,omitempty"`
	LoraDropout   float32  `json:"lora_dropout,omitempty"`
	TargetModules []string `json:"target_modules,omitempty"`
	Quantization  string   `json:"quantization,omitempty"`

	// Extra options passed through to the backend
	Options map[string]string `json:"options,omitempty"`
}

// TrainingJobRequest is the HTTP request body for creating a training job
type TrainingJobRequest struct {
	Model        string            `json:"model"`
	Backend      string            `json:"backend,omitempty"`
	Dataset      string            `json:"dataset"`
	OutputDir    string            `json:"output_dir,omitempty"`
	Epochs       int32             `json:"epochs,omitempty"`
	BatchSize    int32             `json:"batch_size,omitempty"`
	LearningRate float32           `json:"learning_rate,omitempty"`
	MaxSeqLength int32             `json:"max_seq_length,omitempty"`
	LoraRank     int32             `json:"lora_rank,omitempty"`
	LoraAlpha    int32             `json:"lora_alpha,omitempty"`
	LoraDropout  float32           `json:"lora_dropout,omitempty"`
	Quantization string            `json:"quantization,omitempty"`
	Options      map[string]string `json:"options,omitempty"`
}
