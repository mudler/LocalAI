package application

import (
	"cmp"
	"context"
	"errors"
	"github.com/mudler/LocalAI/core/services/collections"
	"github.com/mudler/LocalAI/core/services/messaging"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	corebackend "github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/library"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/pins"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/addons/recovery"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/core/services/addons/updates"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/cloudproxy/mitm"
	"github.com/mudler/LocalAI/core/services/facerecognition"
	"github.com/mudler/LocalAI/core/services/failover"
	"github.com/mudler/LocalAI/core/services/failover/distsync"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/monitoring"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/routing/admission"
	"github.com/mudler/LocalAI/core/services/routing/billing"
	"github.com/mudler/LocalAI/core/services/routing/corpus"
	"github.com/mudler/LocalAI/core/services/routing/pii"
	"github.com/mudler/LocalAI/core/services/routing/piidetector"
	"github.com/mudler/LocalAI/core/services/routing/router"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/convert"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/voiceprofile"
	"github.com/mudler/LocalAI/core/services/voicerecognition"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
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
	addonPolicy         *policy.Policy
	addonStore          *policy.Store
	addonResourceExists func(context.Context, policy.ResourceKey) (bool, error)
	// addonReconcileTick drives the pending-registration retry; nil is a real
	// 30 second ticker. addonReconcile is that loop once the owner started it.
	addonReconcileTick <-chan time.Time
	addonReconcile     *policy.ReconcileLoop
	addonGateway       *gateway.Gateway
	addonServer        *http.Server
	addonSources       *catalog.Sources
	// addonAdminCheck is the owner's administrator check, the one the manager gets.
	addonAdminCheck func(context.Context, contract.Actor) error
	librarySnap     librarySnapshot
	backendLibrary  *backendlib.Installer
	addonManager    *addons.Manager
	addonHostOps    *addonruntime.HostOps
	addonDataRoot   string // parent of the private data directory of each add-on; empty when none runs here
	addonsProbe     addonsProbeState
	modelsProbe     modelsProbeState

	// libraryStore is the library sources file when no add-on owner runs here.
	libraryStore   *catalog.Sources
	libraryStoreMu sync.Mutex

	// The pin store is built once so the routes and the retention sweep share it.
	addonPinStoreOnce    sync.Once
	addonPinStore        pins.Store
	addonPinStoreErr     error
	webSocketTicketsOnce sync.Once
	webSocketTickets     *auth.WebSocketTickets
	backendLoader        *config.ModelConfigLoader
	modelLoader          *model.ModelLoader
	applicationConfig    *config.ApplicationConfig
	startupConfig        *config.ApplicationConfig // Stores original config from env vars (before file loading)
	templatesEvaluator   *templates.Evaluator
	galleryService       *galleryop.GalleryService
	collectionsService   atomic.Pointer[collections.Service]
	faceRegistry         facerecognition.Registry
	voiceRegistry        voicerecognition.Registry
	voiceProfileStore    *voiceprofile.Store
	studioStore          store.Store      // the studio loop's chats; see initStudioStore
	studioStoreKind      string           // "gorm", "json" or "memory"
	studioAgents         agents.Store     // saved agents; same kind as the chats store
	studioNotify         notify.Store     // notifications; same kind as the chats store
	studioPush           notify.PushStore // nil in memory-only mode: push needs somewhere to keep subscriptions
	studioInbox          *notify.Service
	studioMarks          convert.Marks // what the conversion of the old data did
	studioTriggers       *studio.Triggers
	studioTriggersStop   context.CancelFunc
	studioConnStore      connectors.Store
	studioConnectors     *studio.Connectors
	studioConnectorsStop context.CancelFunc
	studioProvider       *provider.Provider
	studioService        *studio.Service
	studioTempDir        string // removed at shutdown; set only in memory-only mode
	studioFiles          *files.Service
	studioFilesDB        *gorm.DB // private database of the workspace; nil when it uses authDB
	studioFilesTemp      string   // blob cache folder of a cluster with no data path; removed at shutdown
	authDB               *gorm.DB
	metricsService       *monitoring.LocalAIMetricsService
	statsRecorder        *billing.Recorder
	fallbackUser         *auth.User
	piiRedactor          *pii.Redactor
	piiEvents            pii.EventStore
	mitmCA               atomic.Pointer[mitm.CA]
	mitmServer           atomic.Pointer[mitm.Server]
	mitmMutex            sync.Mutex // serializes Stop+Start; readers use atomic loads
	// mitmHostConflicts records duplicate-host claims across model configs.
	// Non-empty disables the MITM listener until resolved — the strict
	// 1-to-1 host↔model invariant the dispatcher relies on. Read by
	// /api/middleware/status so the admin UI can surface the cause.
	mitmHostConflicts atomic.Pointer[map[string][]string]
	routerDecisions   router.DecisionStore
	routerRegistry    *router.Registry
	routerCorpus      *corpus.Manager
	admissionLimiter  *admission.Limiter
	failoverManager   *failover.Manager
	watchdogMutex     sync.Mutex
	watchdogStop      chan bool
	p2pMutex          sync.Mutex
	p2pCtx            context.Context
	p2pCancel         context.CancelFunc

	// Distributed mode services (nil when not in distributed mode)
	distributed *DistributedServices

	// failoverSync shares failover state between frontends; nil in
	// standalone mode or when it could not start.
	failoverSync *distsync.Sync
	// failoverLock is the probe-leader lock this frontend holds or tries
	// for; nil when failoverSync is nil.
	failoverLock *advisorylock.HeldLock

	// Upgrade checker (background service for detecting backend upgrades)
	upgradeChecker *UpgradeChecker

	// LocalAI Assistant in-process MCP server. nil when DisableLocalAIAssistant
	// is set; otherwise initialised in start() after galleryService.
	localAIAssistant *mcpTools.LocalAIAssistantHolder

	// assistantClient is the concrete inproc client backing localAIAssistant.
	// start() constructs it before failoverManager exists (see New() in
	// startup.go), so New() sets assistantClient.Failover once the manager
	// is built, using this field to reach back into the already-registered
	// MCP tool set. nil when DisableLocalAIAssistant is set.
	assistantClient *localaiInproc.Client

	// startupComplete flips to true once New() has finished its whole startup
	// sequence. It backs the /readyz probe.
	//
	// The expensive step is the model preload: since #10949 it materializes
	// HuggingFace artifacts for managed backends, which is tens of GB for a
	// large model (31 GB observed on a live cluster). Tracking it explicitly
	// means readiness reports lifecycle state instead of "the handler was
	// reachable", so a replica that is still starting can be kept out of a
	// load balancer's rotation.
	startupComplete atomic.Bool

	shutdownOnce sync.Once
}

// WebSocketTickets returns this frontend's short-lived upgrade ticket store.
func (a *Application) WebSocketTickets() *auth.WebSocketTickets {
	a.webSocketTicketsOnce.Do(func() { a.webSocketTickets = auth.NewWebSocketTickets() })
	return a.webSocketTickets
}

// Ready reports whether the application has finished starting up and can serve
// traffic. It backs the /readyz probe and is safe to call from any goroutine,
// including while startup is still running.
func (a *Application) Ready() bool { return a.startupComplete.Load() }

// markStartupComplete flips the application to ready. Called once, at the very
// end of New(), so every startup step — model preload included — has finished
// before the process advertises itself as able to serve.
func (a *Application) markStartupComplete() { a.startupComplete.Store(true) }

func newApplication(appConfig *config.ApplicationConfig) *Application {
	corebackend.ConfigureGlobalBackendAdmission(appConfig.MaxConcurrentBackendRequests)
	trace.ConfigureBackendTraceMaxInFlight(appConfig.MaxConcurrentBackendRequests)
	ml := model.NewModelLoader(appConfig.SystemState)

	// Apply the per-model load-failure cooldown (0 disables). Set here rather
	// than in the watchdog block so it takes effect regardless of whether the
	// watchdog/LRU limiter is enabled.
	ml.SetLoadFailureCooldown(appConfig.ModelLoadFailureCooldown, 0)

	// Close MCP sessions when a model is unloaded (watchdog eviction, manual shutdown, etc.)
	ml.OnModelUnload(func(modelName string) {
		mcpTools.CloseMCPSessions(modelName)
	})

	// Record a model_load backend trace for every real backend load, so the
	// Traces UI shows which backend runtime served each model and how long
	// the load took. Load failures are traced by the modality wrappers.
	ml.SetLoadLifecycleObserver(corebackend.ModelLoadTraceObserver(appConfig))

	app := &Application{
		backendLoader: config.NewModelConfigLoader(
			appConfig.SystemState.Model.ModelsPath,
			config.WithArtifactMaterializer(appConfig.ModelArtifactMaterializer),
			config.WithReloadDiagnostics(appConfig.DiagnosticsRecorder),
			config.WithPreloadDisplay(appConfig.ModelPreloadRenderMode, appConfig.DisableModelPreloadColor),
		),
		modelLoader:        ml,
		applicationConfig:  appConfig,
		templatesEvaluator: templates.NewEvaluator(appConfig.SystemState.Model.ModelsPath),
		voiceProfileStore:  voiceprofile.NewStore(appConfig.DataPath),
		// KNN corpus files live under <state dir>/router-corpus (same
		// DataPath → DynamicConfigsDir precedence the agent pool uses).
		routerCorpus: corpus.NewManager(filepath.Join(
			cmp.Or(appConfig.DataPath, appConfig.DynamicConfigsDir, "."), "router-corpus")),
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
		return corebackend.StoreBackend(ml, appConfig, app.backendLoader, storeName, "")
	}
	app.faceRegistry = facerecognition.NewStoreRegistry(faceStoreResolver, faceStoreName, faceEmbeddingDim)

	// Voice (speaker) recognition registry — same plumbing, separate
	// namespace so embedding spaces stay isolated (a face vector and a
	// speaker vector are not comparable and differ in dimensionality).
	// The registry also splits its store per embedding dimension, so speaker
	// encoders of different sizes can coexist.
	voiceStoreResolver := func(_ context.Context, storeName string) (pkggrpc.Backend, error) {
		return corebackend.StoreBackend(ml, appConfig, app.backendLoader, storeName, "")
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

// VoiceProfileStore returns the persistent library of reusable voice-cloning
// references. It is distinct from VoiceRegistry, which stores speaker
// recognition embeddings rather than synthesis reference audio.
func (a *Application) VoiceProfileStore() *voiceprofile.Store {
	return a.voiceProfileStore
}

// AuthDB returns the auth database connection, or nil if auth is not enabled.
func (a *Application) AuthDB() *gorm.DB {
	return a.authDB
}

// MetricsService returns the OTel + Prometheus metric service. nil when
// --disable-metrics is set or initialisation failed at startup.
//
// The service is created in startup.go before any counter is registered
// so that otel.SetMeterProvider runs early enough for the billing
// recorder's counters to bind to the Prom-backed provider rather than
// the no-op global. core/http/app.go reuses this instance instead of
// constructing its own — two providers would orphan one set of counters
// behind whichever provider lost the SetMeterProvider race.
func (a *Application) MetricsService() *monitoring.LocalAIMetricsService {
	return a.metricsService
}

// StatsRecorder returns the billing recorder used by the usage
// middleware. It is non-nil whenever stats are not explicitly disabled
// — i.e., the no-auth single-user path still gets a working recorder
// (in-memory by default). Routes register UsageMiddleware against this
// recorder regardless of auth state.
func (a *Application) StatsRecorder() *billing.Recorder {
	return a.statsRecorder
}

// FallbackUser is the synthetic "local" user that UsageMiddleware uses
// to attribute requests when no authenticated user is on the context
// (i.e., --auth is off). nil when auth is on, since real users are
// always available there.
func (a *Application) FallbackUser() *auth.User {
	return a.fallbackUser
}

// PIIRedactor returns the regex-tier PII redactor or nil if PII
// filtering is disabled. The chat-route middleware uses this to apply
// redaction before dispatch.
func (a *Application) PIIRedactor() *pii.Redactor {
	return a.piiRedactor
}

// PIIEvents returns the PII event store. Same nil-when-disabled
// semantics as PIIRedactor; admin REST and MCP read tools call List
// against it.
func (a *Application) PIIEvents() pii.EventStore {
	return a.piiEvents
}

// PIINERResolver returns the resolver the chat PII middleware uses to
// turn a configured detector model name into a ready-to-use NERConfig:
// a token-classifier bound over the shared model loader (lazy — the
// model loads on first Detect) plus the detection policy read from that
// model's own pii_detection block. Unknown names resolve to (zero,
// false) so the middleware fails closed. Pass it via pii.WithNERResolver.
func (a *Application) PIINERResolver() pii.NERDetectorResolver {
	return func(modelName string) (pii.NERConfig, bool) {
		if modelName == "" {
			return pii.NERConfig{}, false
		}
		cfg, ok := a.ModelConfigLoader().GetModelConfig(modelName)
		if !ok {
			return pii.NERConfig{}, false
		}

		// Pattern detectors match secrets with the restricted-regex tier
		// in-process (no backend load). Build a pattern matcher instead of the
		// gRPC token-classifier; on a compile error fail closed with an error
		// detector so the request is blocked, not silently unscanned.
		if cfg.IsPatternDetector() {
			det, err := piidetector.NewPattern(cfg, a.ApplicationConfig())
			if err != nil {
				det = pii.NewErrNERDetector(err.Error())
			}
			return pii.NERConfigFromRaw(
				det,
				0, // patterns are deterministic — no confidence floor
				cfg.PIIDetectionDefaultAction(),
				patternEntityActions(cfg),
				pii.SourcePattern,
			), true
		}

		det := piidetector.New(a.ModelLoader(), cfg, a.ApplicationConfig())
		nc := pii.NERConfigFromRaw(
			det,
			cfg.PIIDetectionMinScore(),
			cfg.PIIDetectionDefaultAction(),
			cfg.PIIDetectionEntityActions(),
			pii.SourceNER,
		)
		nc.ExtendToNextWord = cfg.PIIDetectionExtendToNextWord()
		nc.ExtendToPreviousWord = cfg.PIIDetectionExtendToPreviousWord()
		terms, files := cfg.PIIDetectionProtectedTerms()
		nc.ProtectedTerms = pii.LoadProtectedTerms(terms, files, a.ApplicationConfig().SystemState.Model.ModelsPath)
		return nc, true
	}
}

// patternEntityActions merges a pattern detector's per-pattern Action overrides
// into its entity_actions map. A pattern reports matches under its Name, so a
// per-pattern action is just an entity_actions[Name] entry; explicit
// entity_actions still win if both are set.
func patternEntityActions(cfg config.ModelConfig) map[string]string {
	out := cfg.PIIDetectionEntityActions()
	for _, p := range cfg.PIIDetection.Patterns {
		if p.Action == "" || p.Name == "" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		if _, exists := out[p.Name]; !exists {
			out[p.Name] = p.Action
		}
	}
	return out
}

// ResolvePIIPolicy resolves the effective request-side PII policy for a
// consuming model, layering the instance-wide default detector
// (PIIDefaultDetectors, set via POST /api/settings) on top of the per-model
// config. It is the single decision point shared by the chat middleware (via
// WithPolicyResolver) and the MITM listener so both agree.
//
//   - enabled: an explicit pii.enabled on the model always wins (true OR
//     false). Otherwise PII is on when the backend defaults it on — today
//     that means cloud-proxy models, which cross the network to a third party.
//   - detectors: the model's own pii.detectors, or — when it lists none — the
//     global PIIDefaultDetectors fallback. This is what makes cloud-proxy/MITM
//     redaction work out of the box.
//
// appConfig is read live, so changes via the settings API take effect on the
// next request without a restart.
func (a *Application) ResolvePIIPolicy(cfg *config.ModelConfig) (enabled bool, detectors []string) {
	if cfg == nil {
		return false, nil
	}
	appCfg := a.ApplicationConfig()

	// PIIIsEnabled already encodes "explicit pii.enabled wins, else backend
	// default (cloud-proxy)" — the single source of that rule.
	enabled = cfg.PIIIsEnabled()
	if !enabled {
		return false, nil
	}

	detectors = cfg.PIIDetectors()
	if len(detectors) == 0 {
		detectors = append([]string(nil), appCfg.PIIDefaultDetectors...)
	}
	return true, detectors // enabled is necessarily true past the !enabled guard
}

// PIIPolicyResolver adapts ResolvePIIPolicy to pii.PolicyResolver for
// pii.WithPolicyResolver. The middleware carries the resolved model config as
// `any` (the MODEL_CONFIG context value, a *config.ModelConfig); this asserts
// it back and applies the instance-wide defaults.
func (a *Application) PIIPolicyResolver() pii.PolicyResolver {
	return func(modelCfg any) (bool, []string) {
		cfg, ok := modelCfg.(*config.ModelConfig)
		if !ok {
			return false, nil
		}
		return a.ResolvePIIPolicy(cfg)
	}
}

// MITMCA returns the cloudproxy MITM proxy's CA, or nil when the
// MITM listener is disabled.
func (a *Application) MITMCA() *mitm.CA { return a.mitmCA.Load() }

// MITMServer returns the running MITM proxy or nil.
func (a *Application) MITMServer() *mitm.Server { return a.mitmServer.Load() }

// MITMHostConflicts returns a snapshot of host→[]model-name pairs that
// are claimed by 2+ model configs. Empty when the 1-to-1 invariant
// holds. Non-empty disables the MITM listener — read by the admin
// status endpoint to explain why.
func (a *Application) MITMHostConflicts() map[string][]string {
	p := a.mitmHostConflicts.Load()
	if p == nil {
		return nil
	}
	return *p
}

// MITMHostOwners returns the host→model-name map, useful for the
// admin status endpoint. The lookup is recomputed on each call to
// stay current with model-config edits without needing a
// MITMRestart.
func (a *Application) MITMHostOwners() map[string]string {
	if a.backendLoader == nil {
		return nil
	}
	return a.backendLoader.MITMHostOwners().Owners
}

// RouterDecisions returns the routing decision store. nil when stats
// are disabled (--disable-stats), unless WithRouterDecisionLog explicitly
// retains the log. A nil store skips logging but still rewrites requests.
func (a *Application) RouterDecisions() router.DecisionStore {
	return a.routerDecisions
}

// RouterClassifierRegistry returns the process-wide classifier cache.
// Shared between the OpenAI and Anthropic route middlewares so the
// admin stats endpoint sees every live classifier — and so a
// classifier built on the OpenAI route is reused on Anthropic.
func (a *Application) RouterClassifierRegistry() *router.Registry {
	return a.routerRegistry
}

// AdmissionLimiter returns the per-model admission limiter. The
// admission middleware uses it to gate concurrent requests; the
// admin status surface reads InFlight/Capacity from it for live
// load visibility.
func (a *Application) AdmissionLimiter() *admission.Limiter {
	return a.admissionLimiter
}

// FailoverManager serves failover chains. Never nil after New.
func (a *Application) FailoverManager() *failover.Manager { return a.failoverManager }

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

// Shutdown stops backend gRPC processes and distributed services
// synchronously on the caller's stack. The context-cancel goroutine wired
// in New does the same work asynchronously, which races test-binary exit
// and CLI shutdown — orphaning spawned mock-backend / llama.cpp / etc.
// children to init. Callers that need a guarantee that cleanup has
// finished before they proceed (AfterSuite/AfterEach, signal handlers)
// must call this. Safe to call multiple times.
func (a *Application) Shutdown() error {
	var err error
	a.shutdownOnce.Do(func() {
		if a.studioConnectorsStop != nil {
			a.studioConnectorsStop()
		}
		if a.studioConnectors != nil {
			a.studioConnectors.Close()
		}
		if a.studioTriggersStop != nil {
			a.studioTriggersStop()
		}
		if a.studioTriggers != nil {
			a.studioTriggers.Close()
		}
		if a.studioService != nil {
			_ = a.studioService.Close(context.Background())
		}
		if a.studioTempDir != "" {
			_ = os.RemoveAll(a.studioTempDir)
		}
		a.closeStudioFilesDB(a.studioFilesDB)
		if a.studioFilesTemp != "" {
			_ = os.RemoveAll(a.studioFilesTemp)
		}
		if a.addonServer != nil {
			_ = a.addonServer.Close()
		}
		if a.addonStore != nil {
			_ = a.addonStore.Close()
		}
		if a.addonManager != nil {
			err = a.addonManager.Close()
		}
		// Before distributed shutdown: the sync's subscriptions live on the
		// NATS connection that closes there.
		a.stopFailoverDistributed()
		a.distributed.Shutdown()
		if a.modelLoader != nil {
			err = errors.Join(err, a.modelLoader.StopAllGRPC())
		}
		if a.voiceProfileStore != nil {
			if closeErr := a.voiceProfileStore.Close(); err == nil {
				err = closeErr
			}
		}
	})
	return err
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
		// #nosec G404 -- jitter only spreads retries, it is not security sensitive.
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

func (a *Application) start() (startErr error) {
	if err := a.startAddons(); err != nil {
		return err
	}
	defer func() {
		if startErr != nil && a.addonManager != nil {
			_ = a.addonManager.Close()
		}
	}()

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
		var nodeRegistry *nodes.NodeRegistry
		if a.distributed != nil {
			nodeRegistry = a.distributed.Registry
		}
		assistantClient := localaiInproc.New(
			a.applicationConfig,
			a.applicationConfig.SystemState,
			a.backendLoader,
			a.modelLoader,
			a.galleryService,
			nodeRegistry,
		)
		// Wire usage tracking so the assistant's get_usage_stats tool
		// returns real data; nil values keep the tool returning a clear
		// "unavailable" error if startup ran with --disable-stats.
		assistantClient.StatsRecorder = a.statsRecorder
		assistantClient.FallbackUser = a.fallbackUser
		assistantClient.VoiceProfiles = a.voiceProfileStore
		if a.backendLibrary != nil {
			assistantClient.BackendLibrary = libraryInstallNoProgress{a.backendLibrary}
		}
		if a.distributed != nil && a.distributed.Unloader != nil {
			assistantClient.LoadStopper = a.distributed.Unloader
		}
		// PII filter — same nil-or-real wiring.
		assistantClient.PIIRedactor = a.piiRedactor
		assistantClient.PIIEvents = a.piiEvents
		assistantClient.RouterDecisions = a.routerDecisions
		// Router corpus tools — same factories the RouteModel middleware
		// uses, so the assistant and the request path agree on store
		// namespaces and model resolution.
		assistantClient.RouterCorpus = a.RouterCorpus()
		assistantClient.RouterEmbedder = a.Embedder
		assistantClient.RouterEmbedderFingerprint = a.EmbedderFingerprint
		assistantClient.RouterVectorStore = a.VectorStore
		// Failover chains: failoverManager does not exist yet at this point
		// in startup (New() in startup.go builds it after start() returns),
		// so it can't be wired here like the fields above. New() sets
		// assistantClient.Failover directly once the manager is built;
		// stash the client so it can reach back into it.
		a.assistantClient = assistantClient
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

	return nil
}

// CollectionsService returns the knowledge-base collections of the server, or
// nil while they start or when the feature is off.
func (a *Application) CollectionsService() *collections.Service { return a.collectionsService.Load() }

// StartCollections starts the collections service. Like the agent pool it runs
// after the HTTP server is listening, because the vector engines call the
// embeddings API while they open a collection.
func (a *Application) StartCollections() {
	if !a.applicationConfig.AgentPool.Enabled {
		return
	}
	var bus messaging.Broadcaster
	if d := a.Distributed(); d != nil {
		if d.Registry != nil {
			a.waitForHealthyWorker()
		}
		if d.Nats != nil {
			bus = d.Nats
		}
	}
	svc, err := collections.New(a.applicationConfig, bus)
	if err != nil {
		xlog.Error("Failed to start the collections service", "error", err)
		return
	}
	a.collectionsService.Store(svc)
}

// AddonManager is nil unless the deployment explicitly enables addons.
func (a *Application) AddonManager() *addons.Manager { return a.addonManager }

// BackendLibrary installs backends from the library entries of kind backend.
// It is nil unless this instance owns the add-on libraries.
func (a *Application) BackendLibrary() *backendlib.Installer { return a.backendLibrary }

// AddonSources exposes owner-only source administration and explicit live refresh.
// Forwarders never load source configuration or perform catalog I/O.
func (a *Application) AddonSources() *catalog.Sources { return a.addonSources }

func (a *Application) startAddons() error {
	c := a.applicationConfig.Addons
	if !c.Enabled {
		return nil
	}
	if a.addonManager != nil {
		return addons.ErrConflict
	}
	mode := a.AddonOwnershipMode()
	if mode != auth.AddonModeNamedUser && mode != auth.AddonModeLocalOwner {
		return addons.ErrConfiguration
	}
	if c.Transport != nil {
		switch mode {
		case auth.AddonModeLocalOwner:
			// The HTTP layer checked the caller's role when an account database
			// exists; here the only principal is the local owner.
			c.AdminCheck = func(_ context.Context, actor contract.Actor) error { return auth.ValidateLocalOwnerActor(actor) }
		default:
			if a.authDB == nil {
				return addons.ErrForbidden
			}
			c.AdminCheck = func(ctx context.Context, actor contract.Actor) error {
				return auth.ValidateAddonActor(ctx, a.authDB, actor, true)
			}
		}
	}
	a.addonAdminCheck = c.AdminCheck
	owner := addons.OwnerConfig{InstanceID: c.InstanceID, OwnerID: c.OwnerID, OwnerAddress: c.OwnerAddress, StorageID: c.StorageID, Root: c.Root, SharedStorage: c.SharedStorage}
	var cat catalog.Catalog
	var installer *packages.Installer
	// Replicas must not load even source metadata from the owner's volume.
	if c.InstanceID == c.OwnerID && c.OwnerID != "" {
		if c.AdminCheck == nil {
			return addons.ErrForbidden
		}
		sources, err := catalog.NewSources(filepath.Join(c.Root, "sources.json"), c.AdminCheck, catalog.CredentialResolver{})
		if err != nil {
			return err
		}
		var defaults []catalog.Source
		if c.OfficialLibrary {
			defaults = append(defaults, catalog.OfficialLibrary())
		}
		if c.OfficialBackendLibrary {
			defaults = append(defaults, library.DefaultBackendLibrary())
		}
		sources.SetOfficial(a.applicationConfig.OfficialLibraries)
		if len(defaults) > 0 {
			if _, err = sources.SeedDefaults(defaults...); err != nil {
				return err
			}
		}
		host := c.Host
		if host == (contract.HostCompatibility{}) {
			host = contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: contract.HostSDKVersion, DataFormat: 1, OS: runtime.GOOS, Arch: runtime.GOARCH}
		}
		cat = catalog.Catalog{Sources: sources, TargetOS: host.OS, TargetArch: host.Arch}
		installer = &packages.Installer{Root: filepath.Join(c.Root, "packages"), Catalog: cat, Host: host}
	}
	m, err := addons.NewManager(a.applicationConfig.Context, owner, cat, installer, c.AdminCheck)
	if err != nil {
		return err
	}
	if c.InstanceID == c.OwnerID {
		// Data recovery sits beside the manager: an upgrade of an add-on that
		// has data goes through it, and its journals are settled before any
		// process starts. It gets the owner's policy once that exists.
		account := func(context.Context, string) error { return gateway.ErrForbidden }
		if a.AddonOwnershipMode() != auth.AddonModeLocalOwner && a.authDB != nil {
			users := auth.NewAddonUsers(a.authDB)
			account = users.Active
		}
		rec, rerr := recovery.New(recovery.Config{
			Root: c.Root, Host: m.RecoveryHost(), Admin: c.AdminCheck, Account: account,
			SelectionsDir: filepath.Join(c.Root, "selections"),
			Retention:     recovery.Retention{Keep: c.BackupKeep, MaxAge: c.BackupMaxAge},
		})
		if rerr != nil {
			_ = m.Close()
			return rerr
		}
		m.SetRecovery(rec)
		// The update check reads the libraries and the journal the owner
		// holds; it applies nothing, and its cache is bounded.
		m.EnableUpdates(updates.Config{TTL: c.UpdateCacheTTL, MinRefresh: c.UpdateRefreshInterval})
		if err = m.Reconcile(a.applicationConfig.Context); err != nil {
			_ = m.Close()
			return err
		}
	}
	if cat.Sources != nil {
		if err = cat.Sources.RefreshEnabled(a.applicationConfig.Context); err != nil {
			if a.applicationConfig.Context.Err() != nil {
				_ = m.Close()
				return err
			}
			// An unreachable library must not keep LocalAI or the installed
			// add-ons down. The cache stays empty until an explicit refresh.
			xlog.Warn("Could not refresh the add-on libraries at startup", "error", err)
		}
	}
	if c.InstanceID == c.OwnerID {
		limits := contract.DefaultLimits()
		a.writeModelsProbe()
		a.addonHostOps = a.newAddonHostOps(m, limits)
		a.addonDataRoot = filepath.Join(c.Root, "data")
		supervisor, err := addonruntime.NewSupervisor(addonruntime.Config{StateRoot: filepath.Join(c.Root, "runtime"), DataRoot: filepath.Join(c.Root, "data"), Limits: limits, ReadyTimeout: limits.ReadTimeout, StableHealth: limits.BackoffMax, HostOps: a.addonHostOps})
		if err != nil {
			_ = m.Close()
			return err
		}
		if err = m.AttachRuntime(a.applicationConfig.Context, supervisor); err != nil {
			_ = supervisor.Close(context.Background())
			_ = m.Close()
			return err
		}
	}
	a.addonSources = cat.Sources
	if cat.Sources != nil {
		a.backendLibrary = &backendlib.Installer{
			Catalog: cat, SystemState: a.applicationConfig.SystemState, ModelLoader: a.modelLoader,
			MaxBytes: c.BackendMaxBytes, RequireIntegrity: a.applicationConfig.RequireBackendIntegrity,
		}
	}
	a.addonManager = m
	if c.InstanceID == c.OwnerID {
		go a.runAddonAutoUpgrade(a.applicationConfig.Context, upgradeInitialDelay, upgradeCheckInterval)
	}
	if err := a.startAddonGateway(); err != nil {
		_ = m.Close()
		return err
	}
	return nil
}

// libraryInstallNoProgress adapts the library installer to the assistant's
// installer interface, which does not report progress.
type libraryInstallNoProgress struct{ in *backendlib.Installer }

func (l libraryInstallNoProgress) Install(ctx context.Context, sourceID, backend, version string) error {
	return l.in.Install(ctx, sourceID, backend, version, backendlib.Progress{})
}

func (l libraryInstallNoProgress) Updates(ctx context.Context) ([]localaitools.BackendLibraryUpdate, error) {
	found, err := l.in.Updates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]localaitools.BackendLibraryUpdate, 0, len(found))
	for _, u := range found {
		out = append(out, localaitools.BackendLibraryUpdate{
			Name: u.Name, SourceID: u.SourceID,
			InstalledVersion: u.InstalledVersion, InstalledDigest: u.InstalledDigest,
			Version: u.Version, Digest: u.Digest, Signed: u.Signed,
		})
	}
	return out, nil
}
