// Queue Status Implementation for LocalAI
// This file contains the implementation for queue position tracking

package services

import (
	"fmt"
	"sort"
	"time"

	"github.com/mudler/LocalAI/core/schema"
)

// QueueStatus represents the current status of a job in the queue
type QueueStatus struct {
	JobID           string    `json:"job_id"`
	Position        int       `json:"position"`         // Position in queue (1-indexed)
	TotalPendingJobs int      `json:"total_pending"`    // Total jobs pending
	AheadOfJob      int       `json:"ahead_of_job"`     // Jobs ahead of this one
	EstimatedWait   time.Duration `json:"estimated_wait,omitempty"` // Estimated wait time
	QueueSize       int       `json:"queue_size"`       // Total queue capacity
}

// GetQueueStatusForJob returns the queue status for a specific job
func (s *AgentJobService) GetQueueStatusForJob(jobID string) (*QueueStatus, error) {
	job := s.jobs.Get(jobID)
	if job.ID == "" {
		return nil, fmt.Errorf("job not found: %s", jobID)
	}

	// Get all pending jobs
	pendingJobs := s.ListJobs(nil, &schema.JobStatusPending, 0)
	
	// Sort by CreatedAt ascending (oldest first) to determine queue order
	sort.Slice(pendingJobs, func(i, j int) bool {
		return pendingJobs[i].CreatedAt.Before(pendingJobs[j].CreatedAt)
	})

	// Find the position of the target job
	position := 0
	for i, pj := range pendingJobs {
		if pj.ID == jobID {
			position = i + 1 // 1-indexed
			break
		}
	}

	// If job is not in pending queue, return appropriate status
	if position == 0 {
		return &QueueStatus{
			JobID:           jobID,
			Position:        0,
			TotalPendingJobs: len(pendingJobs),
			AheadOfJob:      0,
			EstimatedWait:   0,
			QueueSize:       cap(s.jobQueue),
		}, nil
	}

	// Calculate estimated wait time based on average job processing time
	// This is a simple heuristic - in production, you might want more sophisticated metrics
	var estimatedWait time.Duration
	if len(pendingJobs) > position {
		// Estimate based on average processing time of completed jobs
		// For now, use a simple 30 second per job estimate
		estimatedWait = time.Duration(len(pendingJobs)-position+1) * 30 * time.Second
	}

	return &QueueStatus{
		JobID:           jobID,
		Position:        position,
		TotalPendingJobs: len(pendingJobs),
		AheadOfJob:      position - 1,
		EstimatedWait:   estimatedWait,
		QueueSize:       cap(s.jobQueue),
	}, nil
}

// GetAllQueueStatus returns the status of all jobs in the queue
func (s *AgentJobService) GetAllQueueStatus() []QueueStatus {
	pendingJobs := s.ListJobs(nil, &schema.JobStatusPending, 0)
	
	// Sort by CreatedAt ascending (oldest first)
	sort.Slice(pendingJobs, func(i, j int) bool {
		return pendingJobs[i].CreatedAt.Before(pendingJobs[j].CreatedAt)
	})

	statuses := make([]QueueStatus, 0, len(pendingJobs))
	for i, job := range pendingJobs {
		estimatedWait := time.Duration(len(pendingJobs)-i-1) * 30 * time.Second
		statuses = append(statuses, QueueStatus{
			JobID:           job.ID,
			Position:        i + 1,
			TotalPendingJobs: len(pendingJobs),
			AheadOfJob:      i,
			EstimatedWait:   estimatedWait,
			QueueSize:       cap(s.jobQueue),
		})
	}

	return statuses
}

// UpdateJobStatusWithQueueInfo updates a job's status with queue information
// This is useful for connectors to push updates to users
func (s *AgentJobService) UpdateJobStatusWithQueueInfo(jobID string, message string) error {
	job := s.jobs.Get(jobID)
	if job.ID == "" {
		return fmt.Errorf("job not found: %s", jobID)
	}

	// Get current queue status
	queueStatus, err := s.GetQueueStatusForJob(jobID)
	if err != nil {
		return err
	}

	// Add a trace with queue information
	trace := schema.JobTrace{
		Type:      "queue_status",
		Content:   message,
		Timestamp: time.Now(),
		Arguments: map[string]interface{}{
			"position":         queueStatus.Position,
			"total_pending":    queueStatus.TotalPendingJobs,
			"ahead_of_job":     queueStatus.AheadOfJob,
			"estimated_wait":   queueStatus.EstimatedWait.String(),
			"queue_size":       queueStatus.QueueSize,
		},
	}

	job.Traces = append(job.Traces, trace)
	s.jobs.Set(jobID, job)

	return nil
}

// GetQueueMetrics returns overall queue metrics
func (s *AgentJobService) GetQueueMetrics() map[string]interface{} {
	pendingJobs := s.ListJobs(nil, &schema.JobStatusPending, 0)
	runningJobs := s.ListJobs(nil, &schema.JobStatusRunning, 0)

	return map[string]interface{}{
		"pending_count":    len(pendingJobs),
		"running_count":    len(runningJobs),
		"total_in_system":  len(pendingJobs) + len(runningJobs),
		"queue_capacity":   cap(s.jobQueue),
		"queue_utilization": float64(len(pendingJobs)) / float64(cap(s.jobQueue)),
		"oldest_pending":   getOldestPendingTime(pendingJobs),
	}
}

func getOldestPendingTime(pendingJobs []schema.Job) *time.Time {
	if len(pendingJobs) == 0 {
		return nil
	}
	
	oldest := pendingJobs[0].CreatedAt
	for _, job := range pendingJobs[1:] {
		if job.CreatedAt.Before(oldest) {
			oldest = job.CreatedAt
		}
	}
	return &oldest
}
