package application

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// DistributedServices holds all services initialized for distributed mode.
type DistributedServices struct {
	Nats         *messaging.Client
	Store        storage.ObjectStore
	Registry     *nodes.NodeRegistry
	Router       *nodes.SmartRouter
	Health       *nodes.HealthMonitor
	JobStore     *jobs.JobStore
	Dispatcher   *jobs.Dispatcher
	AgentStore   *agents.AgentStore
	AgentBridge  *agents.EventBridge
	DistStores   *distributed.Stores
	FileMgr      *storage.FileManager
	FileStager   nodes.FileStager
	ModelAdapter *nodes.ModelRouterAdapter
	Unloader     *nodes.RemoteUnloaderAdapter
}

// initDistributed validates distributed mode prerequisites and initializes
// NATS, object storage, node registry, and instance identity.
// Returns nil if distributed mode is not enabled.
func initDistributed(cfg *config.ApplicationConfig, authDB *gorm.DB) (*DistributedServices, error) {
	if !cfg.Distributed.Enabled {
		return nil, nil
	}

	xlog.Info("Distributed mode enabled — validating prerequisites")

	// Validate PostgreSQL is configured (auth DB must be PostgreSQL for distributed mode)
	if !cfg.Auth.Enabled {
		return nil, fmt.Errorf("distributed mode requires authentication to be enabled (--auth / LOCALAI_AUTH=true)")
	}
	if !isPostgresURL(cfg.Auth.DatabaseURL) {
		return nil, fmt.Errorf("distributed mode requires PostgreSQL for auth database (got %q)", cfg.Auth.DatabaseURL)
	}

	// Validate NATS
	if cfg.Distributed.NatsURL == "" {
		return nil, fmt.Errorf("distributed mode requires --nats-url / LOCALAI_NATS_URL")
	}

	// Generate instance ID if not set
	if cfg.Distributed.InstanceID == "" {
		cfg.Distributed.InstanceID = uuid.New().String()
	}
	xlog.Info("Distributed instance", "id", cfg.Distributed.InstanceID)

	// Connect to NATS
	natsClient, err := messaging.New(cfg.Distributed.NatsURL)
	if err != nil {
		return nil, fmt.Errorf("connecting to NATS: %w", err)
	}
	xlog.Info("Connected to NATS", "url", cfg.Distributed.NatsURL)

	// Ensure NATS is closed if any subsequent initialization step fails.
	success := false
	defer func() {
		if !success {
			natsClient.Close()
		}
	}()

	// Initialize object storage
	var store storage.ObjectStore
	if cfg.Distributed.StorageURL != "" {
		if cfg.Distributed.StorageBucket == "" {
			return nil, fmt.Errorf("distributed storage bucket must be set when storage URL is configured")
		}
		s3Store, err := storage.NewS3Store(storage.S3Config{
			Endpoint:        cfg.Distributed.StorageURL,
			Region:          cfg.Distributed.StorageRegion,
			Bucket:          cfg.Distributed.StorageBucket,
			AccessKeyID:     cfg.Distributed.StorageAccessKey,
			SecretAccessKey: cfg.Distributed.StorageSecretKey,
			ForcePathStyle:  true, // required for MinIO
		})
		if err != nil {
			return nil, fmt.Errorf("initializing S3 storage: %w", err)
		}
		xlog.Info("Object storage initialized (S3)", "endpoint", cfg.Distributed.StorageURL, "bucket", cfg.Distributed.StorageBucket)
		store = s3Store
	} else {
		// Fallback to filesystem storage in distributed mode (useful for single-node testing)
		fsStore, err := storage.NewFilesystemStore(cfg.DataPath + "/objectstore")
		if err != nil {
			return nil, fmt.Errorf("initializing filesystem storage: %w", err)
		}
		xlog.Info("Object storage initialized (filesystem fallback)", "path", cfg.DataPath+"/objectstore")
		store = fsStore
	}

	// Initialize node registry (requires the auth DB which is PostgreSQL)
	if authDB == nil {
		return nil, fmt.Errorf("distributed mode requires auth database to be initialized first")
	}

	registry, err := nodes.NewNodeRegistry(authDB)
	if err != nil {
		return nil, fmt.Errorf("initializing node registry: %w", err)
	}
	xlog.Info("Node registry initialized")

	// Collect SmartRouter option values; the router itself is created after all
	// dependencies (including FileStager and Unloader) are ready.
	var routerAuthToken string
	if cfg.Distributed.RegistrationToken != "" {
		routerAuthToken = cfg.Distributed.RegistrationToken
	}
	var routerGalleriesJSON string
	if galleriesJSON, err := json.Marshal(cfg.BackendGalleries); err == nil {
		routerGalleriesJSON = string(galleriesJSON)
	}

	healthMon := nodes.NewHealthMonitor(registry, authDB,
		cfg.Distributed.HealthCheckIntervalOrDefault(),
		cfg.Distributed.StaleNodeThresholdOrDefault(),
	)

	// Initialize job store
	jobStore, err := jobs.NewJobStore(authDB)
	if err != nil {
		return nil, fmt.Errorf("initializing job store: %w", err)
	}
	xlog.Info("Distributed job store initialized")

	// Initialize job dispatcher
	dispatcher := jobs.NewDispatcher(jobStore, natsClient, authDB, cfg.Distributed.InstanceID)

	// Initialize agent store
	agentStore, err := agents.NewAgentStore(authDB)
	if err != nil {
		return nil, fmt.Errorf("initializing agent store: %w", err)
	}
	xlog.Info("Distributed agent store initialized")

	// Initialize agent event bridge
	agentBridge := agents.NewEventBridge(natsClient, agentStore, cfg.Distributed.InstanceID)

	// Start observable persister — captures observable_update events from workers
	// (which have no DB access) and persists them to PostgreSQL.
	if err := agentBridge.StartObservablePersister(); err != nil {
		xlog.Warn("Failed to start observable persister", "error", err)
	} else {
		xlog.Info("Observable persister started")
	}

	// Initialize Phase 4 stores (MCP, Gallery, FineTune, Skills)
	distStores, err := distributed.InitStores(authDB)
	if err != nil {
		return nil, fmt.Errorf("initializing distributed stores: %w", err)
	}

	// Initialize file manager with local cache
	cacheDir := cfg.DataPath + "/cache"
	fileMgr, err := storage.NewFileManager(store, cacheDir)
	if err != nil {
		return nil, fmt.Errorf("initializing file manager: %w", err)
	}
	xlog.Info("File manager initialized", "cacheDir", cacheDir)

	// Create FileStager for distributed file transfer
	var fileStager nodes.FileStager
	if cfg.Distributed.StorageURL != "" {
		fileStager = nodes.NewS3NATSFileStager(fileMgr, natsClient)
		xlog.Info("File stager initialized (S3+NATS)")
	} else {
		fileStager = nodes.NewHTTPFileStager(func(nodeID string) (string, error) {
			node, err := registry.Get(nodeID)
			if err != nil {
				return "", err
			}
			if node.HTTPAddress == "" {
				return "", fmt.Errorf("node %s has no HTTP address for file transfer", nodeID)
			}
			return node.HTTPAddress, nil
		}, cfg.Distributed.RegistrationToken)
		xlog.Info("File stager initialized (HTTP direct transfer)")
	}
	// Create RemoteUnloaderAdapter — needed by SmartRouter and startup.go
	remoteUnloader := nodes.NewRemoteUnloaderAdapter(registry, natsClient)

	// All dependencies ready — build SmartRouter with all options at once
	router := nodes.NewSmartRouter(registry, nodes.SmartRouterOptions{
		Unloader:      remoteUnloader,
		FileStager:    fileStager,
		GalleriesJSON: routerGalleriesJSON,
		AuthToken:     routerAuthToken,
		DB:            authDB,
	})

	// Create ModelRouterAdapter to wire into ModelLoader
	modelAdapter := nodes.NewModelRouterAdapter(router)

	success = true
	return &DistributedServices{
		Nats:         natsClient,
		Store:        store,
		Registry:     registry,
		Router:       router,
		Health:       healthMon,
		JobStore:     jobStore,
		Dispatcher:   dispatcher,
		AgentStore:   agentStore,
		AgentBridge:  agentBridge,
		DistStores:   distStores,
		FileMgr:      fileMgr,
		FileStager:   fileStager,
		ModelAdapter: modelAdapter,
		Unloader:     remoteUnloader,
	}, nil
}

func isPostgresURL(url string) bool {
	return strings.HasPrefix(url, "postgres://") || strings.HasPrefix(url, "postgresql://")
}
