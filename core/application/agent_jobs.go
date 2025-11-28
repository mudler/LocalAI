package application

import (
	"time"

	"github.com/mudler/LocalAI/core/services"
	"github.com/rs/zerolog/log"
)

// RestartAgentJobService restarts the agent job service with current ApplicationConfig settings
func (a *Application) RestartAgentJobService() error {
	a.agentJobMutex.Lock()
	defer a.agentJobMutex.Unlock()

	// Stop existing service if running
	if a.agentJobService != nil {
		if err := a.agentJobService.Stop(); err != nil {
			log.Warn().Err(err).Msg("Error stopping agent job service")
		}
		// Wait a bit for shutdown to complete
		time.Sleep(200 * time.Millisecond)
	}

	// Create new service instance
	agentJobService := services.NewAgentJobService(
		a.ApplicationConfig(),
		a.ModelLoader(),
		a.ModelConfigLoader(),
		a.TemplatesEvaluator(),
	)

	// Start the service
	err := agentJobService.Start(a.ApplicationConfig().Context)
	if err != nil {
		log.Error().Err(err).Msg("Failed to start agent job service")
		return err
	}

	a.agentJobService = agentJobService
	log.Info().Msg("Agent job service restarted")
	return nil
}

