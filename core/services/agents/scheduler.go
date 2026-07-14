package agents

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// SchedulerStore is the interface for the scheduler's database needs.
type SchedulerStore interface {
	ListConfigs(userID string) ([]AgentConfigRecord, error)
	UpdateLastRun(userID, name string) error
}

// AgentScheduler periodically checks for agents with standalone_job=true
// and publishes background run events to the NATS agent execution queue.
// Uses a PostgreSQL advisory lock so only one instance fires the cron.
// Same pattern as notetaker's runAgentScheduler and LocalAI's cronLeaderLoop.
type AgentScheduler struct {
	db            *gorm.DB
	nats          messaging.Publisher
	store         SchedulerStore
	skillProvider SkillContentProvider // optional: loads full skill info for enriching events
	subject       string               // NATS subject for agent execution
	pollInterval  time.Duration        // how often to check for due agents
}

// AgentSchedulerOpt is a functional option for AgentScheduler.
type AgentSchedulerOpt func(*AgentScheduler)

// WithSchedulerSkillProvider sets the skill provider for enriching events with per-user skills.
func WithSchedulerSkillProvider(provider SkillContentProvider) AgentSchedulerOpt {
	return func(s *AgentScheduler) {
		s.skillProvider = provider
	}
}

// NewAgentScheduler creates a new background agent scheduler.
func NewAgentScheduler(db *gorm.DB, nats messaging.Publisher, store SchedulerStore, subject string, opts ...AgentSchedulerOpt) *AgentScheduler {
	s := &AgentScheduler{
		db:           db,
		nats:         nats,
		store:        store,
		subject:      subject,
		pollInterval: 15 * time.Second,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Start begins the scheduler loop. Blocks until ctx is cancelled.
func (s *AgentScheduler) Start(ctx context.Context) {
	xlog.Info("Agent scheduler started", "pollInterval", s.pollInterval, "subject", s.subject)
	advisorylock.RunLeaderLoop(ctx, s.db, advisorylock.KeyAgentScheduler, s.pollInterval, s.runDueAgents)
	xlog.Info("Agent scheduler stopped")
}

// runDueAgents finds all agents with standalone_job=true that are due for a run
// and publishes background execution events to the NATS queue.
func (s *AgentScheduler) runDueAgents() {
	configs, err := s.store.ListConfigs("") // all users
	if err != nil {
		xlog.Error("Agent scheduler: failed to list configs", "error", err)
		return
	}

	for _, rec := range configs {
		if rec.Status != StatusActive {
			continue
		}

		var cfg AgentConfig
		if err := ParseConfigJSON(rec.ConfigJSON, &cfg); err != nil {
			continue
		}

		if !cfg.StandaloneJob {
			continue
		}

		// Parse the periodic run interval
		interval := parseInterval(cfg.PeriodicRuns)

		// Check if the agent is due
		if !isDue(rec.LastRunAt, interval) {
			continue
		}

		xlog.Info("Scheduling background agent run", "agent", rec.Name, "user", rec.UserID, "interval", interval)

		// Enrich the event with config and skills so the worker needs no DB access
		var skills []SkillInfo
		if cfg.EnableSkills && s.skillProvider != nil {
			if loaded, err := s.skillProvider(rec.UserID); err == nil {
				skills = loaded
			}
		}

		// Publish background run event
		evt := AgentChatEvent{
			AgentName: rec.Name,
			UserID:    rec.UserID,
			MessageID: fmt.Sprintf("bg-%d", time.Now().UnixNano()),
			Role:      RoleSystem,
			Config:    &cfg,
			Skills:    skills,
		}
		if err := s.nats.Publish(s.subject, evt); err != nil {
			xlog.Error("Agent scheduler: failed to publish event", "agent", rec.Name, "error", err)
			continue
		}

		// Update last run timestamp
		if err := s.store.UpdateLastRun(rec.UserID, rec.Name); err != nil {
			xlog.Warn("Agent scheduler: failed to update last run", "agent", rec.Name, "error", err)
		}
	}
}

// parseInterval parses a duration string like "10m", "1h", "30s".
// Returns a default of 10 minutes if empty or invalid.
func parseInterval(s string) time.Duration {
	if s == "" {
		return 10 * time.Minute
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 10 * time.Minute
	}
	return d
}

// IsDueExported is the exported version of isDue for testing.
func IsDueExported(lastRun *time.Time, interval time.Duration) bool {
	return isDue(lastRun, interval)
}

// isDue checks if enough time has elapsed since lastRun for the given interval.
func isDue(lastRun *time.Time, interval time.Duration) bool {
	if lastRun == nil {
		return true // never run before — due now
	}
	return time.Since(*lastRun) >= interval
}
