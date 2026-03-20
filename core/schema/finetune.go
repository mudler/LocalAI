package schema

// RewardFunctionSpec defines a reward function for GRPO training.
type RewardFunctionSpec struct {
	Type   string            `json:"type"`             // "builtin" or "inline"
	Name   string            `json:"name"`
	Code   string            `json:"code,omitempty"`   // inline only
	Params map[string]string `json:"params,omitempty"`
}

// FineTuneJobRequest is the REST API request to start a fine-tuning job.
type FineTuneJobRequest struct {
	Model          string            `json:"model"`
	Backend        string            `json:"backend"`                    // "trl"
	TrainingType   string            `json:"training_type,omitempty"`    // lora, loha, lokr, full
	TrainingMethod string            `json:"training_method,omitempty"`  // sft, dpo, grpo, rloo, reward, kto, orpo

	// Adapter config
	AdapterRank    int32    `json:"adapter_rank,omitempty"`
	AdapterAlpha   int32    `json:"adapter_alpha,omitempty"`
	AdapterDropout float32  `json:"adapter_dropout,omitempty"`
	TargetModules  []string `json:"target_modules,omitempty"`

	// Training hyperparameters
	LearningRate              float32 `json:"learning_rate,omitempty"`
	NumEpochs                 int32   `json:"num_epochs,omitempty"`
	BatchSize                 int32   `json:"batch_size,omitempty"`
	GradientAccumulationSteps int32   `json:"gradient_accumulation_steps,omitempty"`
	WarmupSteps               int32   `json:"warmup_steps,omitempty"`
	MaxSteps                  int32   `json:"max_steps,omitempty"`
	SaveSteps                 int32   `json:"save_steps,omitempty"`
	WeightDecay               float32 `json:"weight_decay,omitempty"`
	GradientCheckpointing     bool    `json:"gradient_checkpointing,omitempty"`
	Optimizer                 string  `json:"optimizer,omitempty"`
	Seed                      int32   `json:"seed,omitempty"`
	MixedPrecision            string  `json:"mixed_precision,omitempty"`

	// Dataset
	DatasetSource string `json:"dataset_source"`
	DatasetSplit  string `json:"dataset_split,omitempty"`

	// Resume from a checkpoint
	ResumeFromCheckpoint string `json:"resume_from_checkpoint,omitempty"`

	// GRPO reward functions
	RewardFunctions []RewardFunctionSpec `json:"reward_functions,omitempty"`

	// Backend-specific and method-specific options
	ExtraOptions map[string]string `json:"extra_options,omitempty"`
}

// FineTuneJob represents a fine-tuning job with its current state.
type FineTuneJob struct {
	ID             string            `json:"id"`
	UserID         string            `json:"user_id,omitempty"`
	Model          string            `json:"model"`
	Backend        string            `json:"backend"`
	TrainingType   string            `json:"training_type"`
	TrainingMethod string            `json:"training_method"`
	Status         string            `json:"status"` // queued, loading_model, loading_dataset, training, saving, completed, failed, stopped
	Message        string            `json:"message,omitempty"`
	OutputDir      string            `json:"output_dir"`
	ExtraOptions   map[string]string `json:"extra_options,omitempty"`
	CreatedAt      string            `json:"created_at"`

	// Export state (tracked separately from training status)
	ExportStatus    string `json:"export_status,omitempty"`     // "", "exporting", "completed", "failed"
	ExportMessage   string `json:"export_message,omitempty"`
	ExportModelName string `json:"export_model_name,omitempty"` // registered model name after export

	// Full config for resume/reuse
	Config *FineTuneJobRequest `json:"config,omitempty"`
}

// FineTuneJobResponse is the REST API response when creating a job.
type FineTuneJobResponse struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// FineTuneProgressEvent is an SSE event for training progress.
type FineTuneProgressEvent struct {
	JobID           string             `json:"job_id"`
	CurrentStep     int32              `json:"current_step"`
	TotalSteps      int32              `json:"total_steps"`
	CurrentEpoch    float32            `json:"current_epoch"`
	TotalEpochs     float32            `json:"total_epochs"`
	Loss            float32            `json:"loss"`
	LearningRate    float32            `json:"learning_rate"`
	GradNorm        float32            `json:"grad_norm"`
	EvalLoss        float32            `json:"eval_loss"`
	EtaSeconds      float32            `json:"eta_seconds"`
	ProgressPercent float32            `json:"progress_percent"`
	Status          string             `json:"status"`
	Message         string             `json:"message,omitempty"`
	CheckpointPath  string             `json:"checkpoint_path,omitempty"`
	SamplePath      string             `json:"sample_path,omitempty"`
	ExtraMetrics    map[string]float32 `json:"extra_metrics,omitempty"`
}

// ExportRequest is the REST API request to export a model.
type ExportRequest struct {
	Name               string            `json:"name,omitempty"`              // model name for LocalAI (auto-generated if empty)
	CheckpointPath     string            `json:"checkpoint_path"`
	ExportFormat       string            `json:"export_format"`               // lora, merged_16bit, merged_4bit, gguf
	QuantizationMethod string            `json:"quantization_method"`         // for GGUF: q4_k_m, q5_k_m, q8_0, f16
	Model              string            `json:"model,omitempty"`             // base model name for merge
	ExtraOptions       map[string]string `json:"extra_options,omitempty"`
}
