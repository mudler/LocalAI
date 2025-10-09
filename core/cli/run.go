package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/application"
	cli_api "github.com/mudler/LocalAI/core/cli/api"
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/signals"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type RunCMD struct {
	ModelArgs []string `arg:"" optional:"" name:"models" help:"Model configuration URLs to load"`

	ExternalBackends             []string      `env:"LOCALAI_EXTERNAL_BACKENDS,EXTERNAL_BACKENDS" help:"A list of external backends to load from gallery on boot" group:"backends"`
	BackendsPath                 string        `env:"LOCALAI_BACKENDS_PATH,BACKENDS_PATH" type:"path" default:"${basepath}/backends" help:"Path containing backends used for inferencing" group:"backends"`
	BackendsSystemPath           string        `env:"LOCALAI_BACKENDS_SYSTEM_PATH,BACKEND_SYSTEM_PATH" type:"path" default:"/usr/share/localai/backends" help:"Path containing system backends used for inferencing" group:"backends"`
	ModelsPath                   string        `env:"LOCALAI_MODELS_PATH,MODELS_PATH" type:"path" default:"${basepath}/models" help:"Path containing models used for inferencing" group:"storage"`
	GeneratedContentPath         string        `env:"LOCALAI_GENERATED_CONTENT_PATH,GENERATED_CONTENT_PATH" type:"path" default:"/tmp/generated/content" help:"Location for generated content (e.g. images, audio, videos)" group:"storage"`
	UploadPath                   string        `env:"LOCALAI_UPLOAD_PATH,UPLOAD_PATH" type:"path" default:"/tmp/localai/upload" help:"Path to store uploads from files api" group:"storage"`
	LocalaiConfigDir             string        `env:"LOCALAI_CONFIG_DIR" type:"path" default:"${basepath}/configuration" help:"Directory for dynamic loading of certain configuration files (currently api_keys.json and external_backends.json)" group:"storage"`
	LocalaiConfigDirPollInterval time.Duration `env:"LOCALAI_CONFIG_DIR_POLL_INTERVAL" help:"Typically the config path picks up changes automatically, but if your system has broken fsnotify events, set this to an interval to poll the LocalAI Config Dir (example: 1m)" group:"storage"`
	// The alias on this option is there to preserve functionality with the old `--config-file` parameter
	ModelsConfigFile         string   `env:"LOCALAI_MODELS_CONFIG_FILE,CONFIG_FILE" aliases:"config-file" help:"YAML file containing a list of model backend configs" group:"storage"`
	BackendGalleries         string   `env:"LOCALAI_BACKEND_GALLERIES,BACKEND_GALLERIES" help:"JSON list of backend galleries" group:"backends" default:"${backends}"`
	Galleries                string   `env:"LOCALAI_GALLERIES,GALLERIES" help:"JSON list of galleries" group:"models" default:"${galleries}"`
	AutoloadGalleries        bool     `env:"LOCALAI_AUTOLOAD_GALLERIES,AUTOLOAD_GALLERIES" group:"models" default:"true"`
	AutoloadBackendGalleries bool     `env:"LOCALAI_AUTOLOAD_BACKEND_GALLERIES,AUTOLOAD_BACKEND_GALLERIES" group:"backends" default:"true"`
	PreloadModels            string   `env:"LOCALAI_PRELOAD_MODELS,PRELOAD_MODELS" help:"A List of models to apply in JSON at start" group:"models"`
	Models                   []string `env:"LOCALAI_MODELS,MODELS" help:"A List of model configuration URLs to load" group:"models"`
	PreloadModelsConfig      string   `env:"LOCALAI_PRELOAD_MODELS_CONFIG,PRELOAD_MODELS_CONFIG" help:"A List of models to apply at startup. Path to a YAML config file" group:"models"`

	F16         bool `name:"f16" env:"LOCALAI_F16,F16" help:"Enable GPU acceleration" group:"performance"`
	Threads     int  `env:"LOCALAI_THREADS,THREADS" short:"t" help:"Number of threads used for parallel computation. Usage of the number of physical cores in the system is suggested" group:"performance"`
	ContextSize int  `env:"LOCALAI_CONTEXT_SIZE,CONTEXT_SIZE" help:"Default context size for models" group:"performance"`

	Address                            string   `env:"LOCALAI_ADDRESS,ADDRESS" default:":8080" help:"Bind address for the API server" group:"api"`
	CORS                               bool     `env:"LOCALAI_CORS,CORS" help:"" group:"api"`
	CORSAllowOrigins                   string   `env:"LOCALAI_CORS_ALLOW_ORIGINS,CORS_ALLOW_ORIGINS" group:"api"`
	CSRF                               bool     `env:"LOCALAI_CSRF" help:"Enables fiber CSRF middleware" group:"api"`
	UploadLimit                        int      `env:"LOCALAI_UPLOAD_LIMIT,UPLOAD_LIMIT" default:"15" help:"Default upload-limit in MB" group:"api"`
	APIKeys                            []string `env:"LOCALAI_API_KEY,API_KEY" help:"List of API Keys to enable API authentication. When this is set, all the requests must be authenticated with one of these API keys" group:"api"`
	DisableWebUI                       bool     `env:"LOCALAI_DISABLE_WEBUI,DISABLE_WEBUI" default:"false" help:"Disables the web user interface. When set to true, the server will only expose API endpoints without serving the web interface" group:"api"`
	DisablePredownloadScan             bool     `env:"LOCALAI_DISABLE_PREDOWNLOAD_SCAN" help:"If true, disables the best-effort security scanner before downloading any files." group:"hardening" default:"false"`
	OpaqueErrors                       bool     `env:"LOCALAI_OPAQUE_ERRORS" default:"false" help:"If true, all error responses are replaced with blank 500 errors. This is intended only for hardening against information leaks and is normally not recommended." group:"hardening"`
	UseSubtleKeyComparison             bool     `env:"LOCALAI_SUBTLE_KEY_COMPARISON" default:"false" help:"If true, API Key validation comparisons will be performed using constant-time comparisons rather than simple equality. This trades off performance on each request for resiliancy against timing attacks." group:"hardening"`
	DisableApiKeyRequirementForHttpGet bool     `env:"LOCALAI_DISABLE_API_KEY_REQUIREMENT_FOR_HTTP_GET" default:"false" help:"If true, a valid API key is not required to issue GET requests to portions of the web ui. This should only be enabled in secure testing environments" group:"hardening"`
	DisableMetricsEndpoint             bool     `env:"LOCALAI_DISABLE_METRICS_ENDPOINT,DISABLE_METRICS_ENDPOINT" default:"false" help:"Disable the /metrics endpoint" group:"api"`
	HttpGetExemptedEndpoints           []string `env:"LOCALAI_HTTP_GET_EXEMPTED_ENDPOINTS" default:"^/$,^/browse/?$,^/talk/?$,^/p2p/?$,^/chat/?$,^/text2image/?$,^/tts/?$,^/static/.*$,^/swagger.*$" help:"If LOCALAI_DISABLE_API_KEY_REQUIREMENT_FOR_HTTP_GET is overriden to true, this is the list of endpoints to exempt. Only adjust this in case of a security incident or as a result of a personal security posture review" group:"hardening"`
	Peer2Peer                          bool     `env:"LOCALAI_P2P,P2P" name:"p2p" default:"false" help:"Enable P2P mode" group:"p2p"`
	Peer2PeerDHTInterval               int      `env:"LOCALAI_P2P_DHT_INTERVAL,P2P_DHT_INTERVAL" default:"360" name:"p2p-dht-interval" help:"Interval for DHT refresh (used during token generation)" group:"p2p"`
	Peer2PeerOTPInterval               int      `env:"LOCALAI_P2P_OTP_INTERVAL,P2P_OTP_INTERVAL" default:"9000" name:"p2p-otp-interval" help:"Interval for OTP refresh (used during token generation)" group:"p2p"`
	Peer2PeerToken                     string   `env:"LOCALAI_P2P_TOKEN,P2P_TOKEN,TOKEN" name:"p2ptoken" help:"Token for P2P mode (optional)" group:"p2p"`
	Peer2PeerNetworkID                 string   `env:"LOCALAI_P2P_NETWORK_ID,P2P_NETWORK_ID" help:"Network ID for P2P mode, can be set arbitrarly by the user for grouping a set of instances" group:"p2p"`
	ParallelRequests                   bool     `env:"LOCALAI_PARALLEL_REQUESTS,PARALLEL_REQUESTS" help:"Enable backends to handle multiple requests in parallel if they support it (e.g.: llama.cpp or vllm)" group:"backends"`
	SingleActiveBackend                bool     `env:"LOCALAI_SINGLE_ACTIVE_BACKEND,SINGLE_ACTIVE_BACKEND" help:"Allow only one backend to be run at a time" group:"backends"`
	PreloadBackendOnly                 bool     `env:"LOCALAI_PRELOAD_BACKEND_ONLY,PRELOAD_BACKEND_ONLY" default:"false" help:"Do not launch the API services, only the preloaded models / backends are started (useful for multi-node setups)" group:"backends"`
	ExternalGRPCBackends               []string `env:"LOCALAI_EXTERNAL_GRPC_BACKENDS,EXTERNAL_GRPC_BACKENDS" help:"A list of external grpc backends" group:"backends"`
	EnableWatchdogIdle                 bool     `env:"LOCALAI_WATCHDOG_IDLE,WATCHDOG_IDLE" default:"false" help:"Enable watchdog for stopping backends that are idle longer than the watchdog-idle-timeout" group:"backends"`
	WatchdogIdleTimeout                string   `env:"LOCALAI_WATCHDOG_IDLE_TIMEOUT,WATCHDOG_IDLE_TIMEOUT" default:"15m" help:"Threshold beyond which an idle backend should be stopped" group:"backends"`
	EnableWatchdogBusy                 bool     `env:"LOCALAI_WATCHDOG_BUSY,WATCHDOG_BUSY" default:"false" help:"Enable watchdog for stopping backends that are busy longer than the watchdog-busy-timeout" group:"backends"`
	WatchdogBusyTimeout                string   `env:"LOCALAI_WATCHDOG_BUSY_TIMEOUT,WATCHDOG_BUSY_TIMEOUT" default:"5m" help:"Threshold beyond which a busy backend should be stopped" group:"backends"`
	Federated                          bool     `env:"LOCALAI_FEDERATED,FEDERATED" help:"Enable federated instance" group:"federated"`
	DisableGalleryEndpoint             bool     `env:"LOCALAI_DISABLE_GALLERY_ENDPOINT,DISABLE_GALLERY_ENDPOINT" help:"Disable the gallery endpoints" group:"api"`
	MachineTag                         string   `env:"LOCALAI_MACHINE_TAG,MACHINE_TAG" help:"Add Machine-Tag header to each response which is useful to track the machine in the P2P network" group:"api"`
	LoadToMemory                       []string `env:"LOCALAI_LOAD_TO_MEMORY,LOAD_TO_MEMORY" help:"A list of models to load into memory at startup" group:"models"`

	Version bool
}

func (r *RunCMD) Run(ctx *cliContext.Context) error {
	if r.Version {
		fmt.Println(internal.Version)
		return nil
	}

	os.MkdirAll(r.BackendsPath, 0750)
	os.MkdirAll(r.ModelsPath, 0750)

	systemState, err := system.GetSystemState(
		system.WithBackendSystemPath(r.BackendsSystemPath),
		system.WithModelPath(r.ModelsPath),
		system.WithBackendPath(r.BackendsPath),
	)
	if err != nil {
		return err
	}

	opts := []config.AppOption{
		config.WithConfigFile(r.ModelsConfigFile),
		config.WithJSONStringPreload(r.PreloadModels),
		config.WithYAMLConfigPreload(r.PreloadModelsConfig),
		config.WithSystemState(systemState),
		config.WithContextSize(r.ContextSize),
		config.WithDebug(zerolog.GlobalLevel() <= zerolog.DebugLevel),
		config.WithGeneratedContentDir(r.GeneratedContentPath),
		config.WithUploadDir(r.UploadPath),
		config.WithDynamicConfigDir(r.LocalaiConfigDir),
		config.WithDynamicConfigDirPollInterval(r.LocalaiConfigDirPollInterval),
		config.WithF16(r.F16),
		config.WithStringGalleries(r.Galleries),
		config.WithBackendGalleries(r.BackendGalleries),
		config.WithCors(r.CORS),
		config.WithCorsAllowOrigins(r.CORSAllowOrigins),
		config.WithCsrf(r.CSRF),
		config.WithThreads(r.Threads),
		config.WithUploadLimitMB(r.UploadLimit),
		config.WithApiKeys(r.APIKeys),
		config.WithModelsURL(append(r.Models, r.ModelArgs...)...),
		config.WithExternalBackends(r.ExternalBackends...),
		config.WithOpaqueErrors(r.OpaqueErrors),
		config.WithEnforcedPredownloadScans(!r.DisablePredownloadScan),
		config.WithSubtleKeyComparison(r.UseSubtleKeyComparison),
		config.WithDisableApiKeyRequirementForHttpGet(r.DisableApiKeyRequirementForHttpGet),
		config.WithHttpGetExemptedEndpoints(r.HttpGetExemptedEndpoints),
		config.WithP2PNetworkID(r.Peer2PeerNetworkID),
		config.WithLoadToMemory(r.LoadToMemory),
		config.WithMachineTag(r.MachineTag),
		config.WithAPIAddress(r.Address),
	}

	if r.DisableMetricsEndpoint {
		opts = append(opts, config.DisableMetricsEndpoint)
	}

	token := ""
	if r.Peer2Peer || r.Peer2PeerToken != "" {
		log.Info().Msg("P2P mode enabled")
		token = r.Peer2PeerToken
		if token == "" {
			// IF no token is provided, and p2p is enabled,
			// we generate one and wait for the user to pick up the token (this is for interactive)
			log.Info().Msg("No token provided, generating one")
			token = p2p.GenerateToken(r.Peer2PeerDHTInterval, r.Peer2PeerOTPInterval)
			log.Info().Msg("Generated Token:")
			fmt.Println(token)

			log.Info().Msg("To use the token, you can run the following command in another node or terminal:")
			fmt.Printf("export TOKEN=\"%s\"\nlocal-ai worker p2p-llama-cpp-rpc\n", token)
		}
		opts = append(opts, config.WithP2PToken(token))
	}

	backgroundCtx := context.Background()

	idleWatchDog := r.EnableWatchdogIdle
	busyWatchDog := r.EnableWatchdogBusy

	if r.DisableWebUI {
		opts = append(opts, config.DisableWebUI)
	}

	if r.DisableGalleryEndpoint {
		opts = append(opts, config.DisableGalleryEndpoint)
	}

	if idleWatchDog || busyWatchDog {
		opts = append(opts, config.EnableWatchDog)
		if idleWatchDog {
			opts = append(opts, config.EnableWatchDogIdleCheck)
			dur, err := time.ParseDuration(r.WatchdogIdleTimeout)
			if err != nil {
				return err
			}
			opts = append(opts, config.SetWatchDogIdleTimeout(dur))
		}
		if busyWatchDog {
			opts = append(opts, config.EnableWatchDogBusyCheck)
			dur, err := time.ParseDuration(r.WatchdogBusyTimeout)
			if err != nil {
				return err
			}
			opts = append(opts, config.SetWatchDogBusyTimeout(dur))
		}
	}
	if r.ParallelRequests {
		opts = append(opts, config.EnableParallelBackendRequests)
	}
	if r.SingleActiveBackend {
		opts = append(opts, config.EnableSingleBackend)
	}

	// split ":" to get backend name and the uri
	for _, v := range r.ExternalGRPCBackends {
		backend := v[:strings.IndexByte(v, ':')]
		uri := v[strings.IndexByte(v, ':')+1:]
		opts = append(opts, config.WithExternalBackend(backend, uri))
	}

	if r.AutoloadGalleries {
		opts = append(opts, config.EnableGalleriesAutoload)
	}

	if r.AutoloadBackendGalleries {
		opts = append(opts, config.EnableBackendGalleriesAutoload)
	}

	if r.PreloadBackendOnly {
		_, err := application.New(opts...)
		return err
	}

	app, err := application.New(opts...)
	if err != nil {
		return fmt.Errorf("failed basic startup tasks with error %s", err.Error())
	}

	appHTTP, err := http.API(app)
	if err != nil {
		log.Error().Err(err).Msg("error during HTTP App construction")
		return err
	}

	if err := cli_api.StartP2PStack(backgroundCtx, r.Address, token, r.Peer2PeerNetworkID, r.Federated, app); err != nil {
		return err
	}

	signals.RegisterGracefulTerminationHandler(func() {
		if err := app.ModelLoader().StopAllGRPC(); err != nil {
			log.Error().Err(err).Msg("error while stopping all grpc backends")
		}
	})

	return appHTTP.Listen(r.Address)
}
