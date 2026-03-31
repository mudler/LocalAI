package agentpool

import "errors"

var (
	ErrAgentNotFound     = errors.New("agent not found")
	ErrTaskNotFound      = errors.New("task not found")
	ErrJobNotFound       = errors.New("job not found")
	ErrSkillNotFound     = errors.New("skill not found")
	ErrSkillsUnavailable = errors.New("skills service not available")
	ErrTaskDisabled      = errors.New("task is disabled")
	ErrJobQueueFull      = errors.New("job queue is full")
)
