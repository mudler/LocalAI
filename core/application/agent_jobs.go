package application

import (
	"time"

	"github.com/mudler/LocalAI/core/services/agentpool"
	"github.com/mudler/xlog"
)

// RestartAgentJobService restarts the agent job service with current ApplicationConfig settings
func (a *Application) RestartAgentJobService() error {
	a.agentJobMutex.Lock()
	defer a.agentJobMutex.Unlock()

	// Stop existing service if running
	if a.agentJobService != nil {
		if err := a.agentJobService.Stop(); err != nil {
			xlog.Warn("Error stopping agent job service", "error", err)
		}
		// Wait a bit for shutdown to complete
		time.Sleep(200 * time.Millisecond)
	}

	// Create new service instance
	agentJobService := agentpool.NewAgentJobService(
		a.ApplicationConfig(),
		a.ModelLoader(),
		a.ModelConfigLoader(),
		a.TemplatesEvaluator(),
	)

	// Re-apply distributed wiring if available (matches startup.go logic)
	if a.jobDispatcher != nil {
		agentJobService.SetDistributedBackends(a.jobDispatcher)
	}
	if a.jobStore != nil {
		agentJobService.SetDistributedJobStore(a.jobStore)
	}

	// Start the service
	err := agentJobService.Start(a.ApplicationConfig().Context)
	if err != nil {
		xlog.Error("Failed to start agent job service", "error", err)
		return err
	}

	a.agentJobService = agentJobService
	xlog.Info("Agent job service restarted")
	return nil
}
