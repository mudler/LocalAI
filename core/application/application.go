package application

import (
	"context"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/agentpool"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

type Application struct {
	backendLoader      *config.ModelConfigLoader
	modelLoader        *model.ModelLoader
	applicationConfig  *config.ApplicationConfig
	startupConfig      *config.ApplicationConfig // Stores original config from env vars (before file loading)
	templatesEvaluator *templates.Evaluator
	galleryService     *galleryop.GalleryService
	agentJobService    *agentpool.AgentJobService
	agentPoolService   atomic.Pointer[agentpool.AgentPoolService]
	authDB             *gorm.DB
	watchdogMutex      sync.Mutex
	watchdogStop       chan bool
	p2pMutex           sync.Mutex
	p2pCtx             context.Context
	p2pCancel          context.CancelFunc
	agentJobMutex      sync.Mutex

	// Distributed mode services (nil when not in distributed mode)
	distributed *DistributedServices
}

func newApplication(appConfig *config.ApplicationConfig) *Application {
	ml := model.NewModelLoader(appConfig.SystemState)

	// Close MCP sessions when a model is unloaded (watchdog eviction, manual shutdown, etc.)
	ml.OnModelUnload(func(modelName string) {
		mcpTools.CloseMCPSessions(modelName)
	})

	return &Application{
		backendLoader:      config.NewModelConfigLoader(appConfig.SystemState.Model.ModelsPath),
		modelLoader:        ml,
		applicationConfig:  appConfig,
		templatesEvaluator: templates.NewEvaluator(appConfig.SystemState.Model.ModelsPath),
	}
}

func (a *Application) ModelConfigLoader() *config.ModelConfigLoader {
	return a.backendLoader
}

func (a *Application) ModelLoader() *model.ModelLoader {
	return a.modelLoader
}

func (a *Application) ApplicationConfig() *config.ApplicationConfig {
	return a.applicationConfig
}

func (a *Application) TemplatesEvaluator() *templates.Evaluator {
	return a.templatesEvaluator
}

func (a *Application) GalleryService() *galleryop.GalleryService {
	return a.galleryService
}

func (a *Application) AgentJobService() *agentpool.AgentJobService {
	return a.agentJobService
}

func (a *Application) AgentPoolService() *agentpool.AgentPoolService {
	return a.agentPoolService.Load()
}

// AuthDB returns the auth database connection, or nil if auth is not enabled.
func (a *Application) AuthDB() *gorm.DB {
	return a.authDB
}

// StartupConfig returns the original startup configuration (from env vars, before file loading)
func (a *Application) StartupConfig() *config.ApplicationConfig {
	return a.startupConfig
}

// Distributed returns the distributed services, or nil if not in distributed mode.
func (a *Application) Distributed() *DistributedServices {
	return a.distributed
}

// IsDistributed returns true if the application is running in distributed mode.
func (a *Application) IsDistributed() bool {
	return a.distributed != nil
}

// waitForHealthyWorker blocks until at least one healthy backend worker is registered.
// This prevents the agent pool from failing during startup when workers haven't connected yet.
func (a *Application) waitForHealthyWorker() {
	maxWait := a.applicationConfig.Distributed.WorkerWaitTimeoutOrDefault()
	const basePoll = 2 * time.Second

	xlog.Info("Waiting for at least one healthy backend worker before starting agent pool")
	deadline := time.Now().Add(maxWait)

	for time.Now().Before(deadline) {
		registered, err := a.distributed.Registry.List(context.Background())
		if err == nil {
			for _, n := range registered {
				if n.NodeType == nodes.NodeTypeBackend && n.Status == nodes.StatusHealthy {
					xlog.Info("Healthy backend worker found", "node", n.Name)
					return
				}
			}
		}
		// Add 0-1s jitter to prevent thundering-herd on the node registry
		jitter := time.Duration(rand.Int64N(int64(time.Second)))
		select {
		case <-a.applicationConfig.Context.Done():
			return
		case <-time.After(basePoll + jitter):
		}
	}
	xlog.Warn("No healthy backend worker found after waiting, proceeding anyway")
}

// InstanceID returns the unique identifier for this frontend instance.
func (a *Application) InstanceID() string {
	return a.applicationConfig.Distributed.InstanceID
}

func (a *Application) start() error {
	galleryService := galleryop.NewGalleryService(a.ApplicationConfig(), a.ModelLoader())
	err := galleryService.Start(a.ApplicationConfig().Context, a.ModelConfigLoader(), a.ApplicationConfig().SystemState)
	if err != nil {
		return err
	}

	a.galleryService = galleryService

	// Initialize agent job service (Start() is deferred to after distributed wiring)
	agentJobService := agentpool.NewAgentJobService(
		a.ApplicationConfig(),
		a.ModelLoader(),
		a.ModelConfigLoader(),
		a.TemplatesEvaluator(),
	)

	a.agentJobService = agentJobService

	return nil
}

// StartAgentPool initializes and starts the agent pool service (LocalAGI integration).
// This must be called after the HTTP server is listening, because backends like
// PostgreSQL need to call the embeddings API during collection initialization.
func (a *Application) StartAgentPool() {
	if !a.applicationConfig.AgentPool.Enabled {
		return
	}
	aps, err := agentpool.NewAgentPoolService(a.applicationConfig)
	if err != nil {
		xlog.Error("Failed to create agent pool service", "error", err)
		return
	}
	if a.authDB != nil {
		aps.SetAuthDB(a.authDB)
	}
	// Wire distributed mode components
	if d := a.Distributed(); d != nil {
		if d.DistStores != nil && d.DistStores.Skills != nil {
			aps.SetSkillStore(d.DistStores.Skills)
		}
		aps.SetNATSClient(d.Nats)
		if d.AgentBridge != nil {
			aps.SetEventBridge(d.AgentBridge)
		}
		if d.AgentStore != nil {
			aps.SetAgentStore(d.AgentStore)
		}
		// Wait for at least one healthy backend worker before starting the agent pool.
		// Collections initialization calls embeddings which require a worker.
		if d.Registry != nil {
			a.waitForHealthyWorker()
		}
	}

	if err := aps.Start(a.applicationConfig.Context); err != nil {
		xlog.Error("Failed to start agent pool", "error", err)
		return
	}

	// Wire per-user scoped services so collections, skills, and jobs are isolated per user
	usm := agentpool.NewUserServicesManager(
		aps.UserStorage(),
		a.applicationConfig,
		a.modelLoader,
		a.backendLoader,
		a.templatesEvaluator,
	)
	// Wire distributed backends to per-user job services
	if a.agentJobService != nil {
		if d := a.agentJobService.Dispatcher(); d != nil {
			usm.SetJobDispatcher(d)
		}
		if s := a.agentJobService.DBStore(); s != nil {
			usm.SetJobDBStore(s)
		}
	}
	aps.SetUserServicesManager(usm)

	a.agentPoolService.Store(aps)
}
