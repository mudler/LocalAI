package application

import (
	"context"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	corebackend "github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/agentpool"
	"github.com/mudler/LocalAI/core/services/facerecognition"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/voicerecognition"
	"github.com/mudler/LocalAI/core/templates"
	pkggrpc "github.com/mudler/LocalAI/pkg/grpc"
	localaitools "github.com/mudler/LocalAI/pkg/mcp/localaitools"
	localaiInproc "github.com/mudler/LocalAI/pkg/mcp/localaitools/inproc"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/signals"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// faceEmbeddingDim is the expected dimension for face embeddings.
// Set to 0 so the Registry accepts whatever dim the loaded recognizer
// produces — ArcFace R50 is 512-d, MBF is 512-d, SFace is 128-d, and
// the insightface backend can load any of them via LoadModel options.
// Locking this to a specific value would force a single recognizer
// family per deployment; we keep the door open instead.
const faceEmbeddingDim = 0

// voiceEmbeddingDim is the expected dimension for speaker embeddings.
// 0 so the Registry accepts whatever dim the loaded recognizer
// produces — ECAPA-TDNN is 192, WeSpeaker ResNet34 is 256, 3D-Speaker
// ERes2Net is 192, CAM++ is 512.
const voiceEmbeddingDim = 0

type Application struct {
	backendLoader      *config.ModelConfigLoader
	modelLoader        *model.ModelLoader
	applicationConfig  *config.ApplicationConfig
	startupConfig      *config.ApplicationConfig // Stores original config from env vars (before file loading)
	templatesEvaluator *templates.Evaluator
	galleryService     *galleryop.GalleryService
	agentJobService    *agentpool.AgentJobService
	agentPoolService   atomic.Pointer[agentpool.AgentPoolService]
	faceRegistry       facerecognition.Registry
	voiceRegistry      voicerecognition.Registry
	authDB             *gorm.DB
	watchdogMutex      sync.Mutex
	watchdogStop       chan bool
	p2pMutex           sync.Mutex
	p2pCtx             context.Context
	p2pCancel          context.CancelFunc
	agentJobMutex      sync.Mutex

	// Distributed mode services (nil when not in distributed mode)
	distributed *DistributedServices

	// Upgrade checker (background service for detecting backend upgrades)
	upgradeChecker *UpgradeChecker

	// LocalAI Assistant in-process MCP server. nil when DisableLocalAIAssistant
	// is set; otherwise initialised in start() after galleryService.
	localAIAssistant *mcpTools.LocalAIAssistantHolder
}

func newApplication(appConfig *config.ApplicationConfig) *Application {
	ml := model.NewModelLoader(appConfig.SystemState)

	// Close MCP sessions when a model is unloaded (watchdog eviction, manual shutdown, etc.)
	ml.OnModelUnload(func(modelName string) {
		mcpTools.CloseMCPSessions(modelName)
	})

	app := &Application{
		backendLoader:      config.NewModelConfigLoader(appConfig.SystemState.Model.ModelsPath),
		modelLoader:        ml,
		applicationConfig:  appConfig,
		templatesEvaluator: templates.NewEvaluator(appConfig.SystemState.Model.ModelsPath),
	}

	// Face-recognition registry backed by LocalAI's built-in vector store.
	// The resolver closes over the ModelLoader so the Registry stays
	// decoupled from loader plumbing; swapping in a postgres-backed
	// implementation later is a single construction change here.
	//
	// `faceStoreName` is the default namespace passed to StoreBackend when
	// the request doesn't override it. Face and voice MUST use distinct
	// namespaces — the local-store gRPC surface rejects mixed dimensions
	// inside one namespace ("Try to add key with length N when existing
	// length is M"). ArcFace buffalo_l produces 512-dim embeddings while
	// ECAPA-TDNN produces 192-dim; enrolling one after the other into a
	// shared namespace is exactly how we hit that error.
	const (
		faceStoreName  = "localai-face-biometrics"
		voiceStoreName = "localai-voice-biometrics"
	)
	faceStoreResolver := func(_ context.Context, storeName string) (pkggrpc.Backend, error) {
		return corebackend.StoreBackend(ml, appConfig, storeName, "")
	}
	app.faceRegistry = facerecognition.NewStoreRegistry(faceStoreResolver, faceStoreName, faceEmbeddingDim)

	// Voice (speaker) recognition registry — same plumbing, separate
	// namespace so embedding spaces stay isolated (a face vector and a
	// speaker vector are not comparable and differ in dimensionality).
	voiceStoreResolver := func(_ context.Context, storeName string) (pkggrpc.Backend, error) {
		return corebackend.StoreBackend(ml, appConfig, storeName, "")
	}
	app.voiceRegistry = voicerecognition.NewStoreRegistry(voiceStoreResolver, voiceStoreName, voiceEmbeddingDim)

	return app
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

func (a *Application) UpgradeChecker() *UpgradeChecker {
	return a.upgradeChecker
}

// LocalAIAssistant returns the in-process MCP holder used by the chat handler
// when an admin opts into the assistant modality. Returns nil when the feature
// is disabled at startup.
func (a *Application) LocalAIAssistant() *mcpTools.LocalAIAssistantHolder {
	return a.localAIAssistant
}

// distributedDB returns the PostgreSQL database for distributed coordination,
// or nil in standalone mode.
func (a *Application) distributedDB() *gorm.DB {
	if a.distributed != nil {
		return a.authDB
	}
	return nil
}

func (a *Application) AgentPoolService() *agentpool.AgentPoolService {
	return a.agentPoolService.Load()
}

// FaceRegistry returns the face-recognition registry used for 1:N
// identification. The current implementation is backed by the
// in-memory local-store backend; see core/services/facerecognition
// for the interface and the postgres TODO.
func (a *Application) FaceRegistry() facerecognition.Registry {
	return a.faceRegistry
}

// VoiceRegistry returns the voice (speaker) recognition registry used
// for 1:N identification. Same in-memory local-store backing as
// FaceRegistry but a separate instance — voice embeddings live in
// their own vector space.
func (a *Application) VoiceRegistry() voicerecognition.Registry {
	return a.voiceRegistry
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

	// LocalAI Assistant: in-process MCP server exposing admin tools. Initialised
	// once at startup and reused across chat sessions that opt in via metadata.
	if !a.applicationConfig.DisableLocalAIAssistant {
		holder := mcpTools.NewLocalAIAssistantHolder()
		assistantClient := localaiInproc.New(
			a.applicationConfig,
			a.applicationConfig.SystemState,
			a.backendLoader,
			a.modelLoader,
			a.galleryService,
		)
		if err := holder.Initialize(a.applicationConfig.Context, assistantClient, localaitools.Options{}); err != nil {
			// Why log+continue instead of fail: the assistant is an optional
			// feature; a failure here must not take down the whole server.
			xlog.Warn("LocalAI Assistant initialisation failed; feature unavailable", "error", err)
		} else {
			a.localAIAssistant = holder
			// Tear the in-memory transport pair down on SIGINT/SIGTERM so the
			// goroutine ends cleanly. Mirrors how core/http/endpoints/mcp/tools.go
			// closes its per-model MCP sessions on graceful termination.
			signals.RegisterGracefulTerminationHandler(func() {
				_ = holder.Close()
			})
		}
	}

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
	// Build options struct from available dependencies
	opts := agentpool.AgentPoolOptions{
		AuthDB: a.authDB,
	}
	if d := a.Distributed(); d != nil {
		if d.DistStores != nil && d.DistStores.Skills != nil {
			opts.SkillStore = d.DistStores.Skills
		}
		opts.NATSClient = d.Nats
		opts.EventBridge = d.AgentBridge
		opts.AgentStore = d.AgentStore
	}

	aps, err := agentpool.NewAgentPoolService(a.applicationConfig, opts)
	if err != nil {
		xlog.Error("Failed to create agent pool service", "error", err)
		return
	}

	// Wire distributed mode components
	if d := a.Distributed(); d != nil {
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
