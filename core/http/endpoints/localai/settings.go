package localai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/endpoints/openresponses"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/hostexec"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/vram"
	"github.com/mudler/LocalAI/pkg/vrambudget"
	"github.com/mudler/xlog"
)

// GetSettingsEndpoint returns current settings with precedence (env > file > defaults)
func GetSettingsEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		appConfig := app.ApplicationConfig()
		settings := appConfig.ToRuntimeSettings()
		return c.JSON(http.StatusOK, settings)
	}
}

// UpdateSettingsEndpoint updates settings, saves to file, and applies immediately
func UpdateSettingsEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		appConfig := app.ApplicationConfig()
		startupConfig := app.StartupConfig()

		if startupConfig == nil {
			startupConfig = appConfig
		}

		body, err := io.ReadAll(c.Request().Body)
		if err != nil {
			return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
				Success: false,
				Error:   "Failed to read request body: " + err.Error(),
			})
		}

		var settings config.RuntimeSettings
		if err := json.Unmarshal(body, &settings); err != nil {
			return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
				Success: false,
				Error:   "Failed to parse JSON: " + err.Error(),
			})
		}

		// The ports and the SearXNG address of the chats' web tools.
		if settings.ChatsWebPorts != nil {
			if _, err := config.ParsePorts(*settings.ChatsWebPorts); err != nil {
				return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
					Success: false,
					Error:   "Invalid chats_web_ports: " + err.Error(),
				})
			}
		}
		if settings.ChatsWebSearXNGURL != nil {
			if err := config.ValidateSearXNGURL(*settings.ChatsWebSearXNGURL); err != nil {
				return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
					Success: false,
					Error:   "Invalid chats_web_searxng_url: " + err.Error(),
				})
			}
		}

		// The network of the Sandbox commands.
		if msg := validateEgressSettings(settings, body, appConfig.ChatsEgressAllowPrivate); msg != "" {
			return c.JSON(http.StatusBadRequest, schema.SettingsResponse{Success: false, Error: msg})
		}

		// Validate timeouts if provided
		if settings.WatchdogIdleTimeout != nil {
			if _, err := time.ParseDuration(*settings.WatchdogIdleTimeout); err != nil {
				return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
					Success: false,
					Error:   "Invalid watchdog_idle_timeout format: " + err.Error(),
				})
			}
		}
		if settings.WatchdogBusyTimeout != nil {
			if _, err := time.ParseDuration(*settings.WatchdogBusyTimeout); err != nil {
				return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
					Success: false,
					Error:   "Invalid watchdog_busy_timeout format: " + err.Error(),
				})
			}
		}
		if settings.WatchdogInterval != nil {
			if _, err := time.ParseDuration(*settings.WatchdogInterval); err != nil {
				return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
					Success: false,
					Error:   "Invalid watchdog_interval format: " + err.Error(),
				})
			}
		}
		if settings.LRUEvictionRetryInterval != nil {
			if _, err := time.ParseDuration(*settings.LRUEvictionRetryInterval); err != nil {
				return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
					Success: false,
					Error:   "Invalid lru_eviction_retry_interval format: " + err.Error(),
				})
			}
		}
		if settings.OpenResponsesStoreTTL != nil {
			if *settings.OpenResponsesStoreTTL != "0" && *settings.OpenResponsesStoreTTL != "" {
				if _, err := time.ParseDuration(*settings.OpenResponsesStoreTTL); err != nil {
					return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
						Success: false,
						Error:   "Invalid open_responses_store_ttl format: " + err.Error(),
					})
				}
			}
		}

		// Reject a malformed VRAM budget up front so a bad value never reaches
		// the config (ApplyRuntimeSettings is fail-open and would silently skip
		// installing the cap). An empty string is allowed: it clears the cap.
		if settings.VRAMBudget != nil && *settings.VRAMBudget != "" {
			if _, err := vrambudget.Parse(*settings.VRAMBudget); err != nil {
				return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
					Success: false,
					Error:   "Invalid vram_budget format: " + err.Error(),
				})
			}
		}

		// The built-in chat tools: a mode the chats understand, and pins only for
		// tools and skills an add-on may replace.
		if settings.ChatsHostExec != nil && !hostexec.ValidMode(*settings.ChatsHostExec) {
			return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
				Success: false,
				Error:   "Invalid chats_host_exec: use off, ask or on",
			})
		}
		if settings.ChatsToolProviders != nil {
			for tool, addon := range *settings.ChatsToolProviders {
				if !(contract.Replaceable(tool) || contract.ReplaceableSkill(tool)) || !validAddonID(addon) {
					return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
						Success: false,
						Error:   "Invalid chats_tool_providers: map a replaceable tool (bash, web_search, web_fetch) or skill (xlsx, docx, pptx, pdf) to an add-on ID",
					})
				}
			}
		}

		// The owner's value is the deployment's value. A frontend reads it from
		// the owner at every refresh, so a change made here would be undone and
		// would not reach the other frontends.
		if app.AddonFrontend() && settings.OfficialLibraries != nil && *settings.OfficialLibraries != appConfig.OfficialLibraries {
			return c.JSON(http.StatusConflict, schema.SettingsResponse{
				Success: false,
				Error:   "official_libraries is set on the add-on owner, not on a frontend",
			})
		}

		// Generate P2P token before saving so the real token is persisted (not "0")
		if settings.P2PToken != nil && *settings.P2PToken == "0" {
			token := p2p.GenerateToken(60, 60)
			settings.P2PToken = &token
		}

		// Save to file
		if appConfig.DynamicConfigsDir == "" {
			return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
				Success: false,
				Error:   "DynamicConfigsDir is not set",
			})
		}

		// Read whatever is already persisted: it is both the source of truth
		// for branding asset filenames (below) and the base we merge this
		// request onto before writing. A read failure must not let a Save
		// silently discard the existing settings — surface it instead.
		persisted, err := appConfig.ReadPersistedSettings()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, schema.SettingsResponse{
				Success: false,
				Error:   "Failed to read existing settings: " + err.Error(),
			})
		}

		// Branding asset filenames are owned exclusively by
		// /api/branding/asset/{kind} (upload/delete). The Settings page also
		// round-trips them via GET /api/settings, but its local state is stale
		// once an asset has been uploaded — clicking Save would otherwise
		// clobber the uploaded basename with the empty string the UI loaded
		// at page open. Replace whatever the body sent for these three fields
		// with the values currently on disk so /api/settings can never
		// regress them.
		settings.LogoFile = persisted.LogoFile
		settings.LogoHorizontalFile = persisted.LogoHorizontalFile
		settings.FaviconFile = persisted.FaviconFile

		// The UI reads ApiKeys from GET /api/settings, which already returns the
		// merged env+runtime list. When the user clicks Save, the same merged
		// list comes back in the POST body. Strip the env-supplied keys from
		// the incoming list before we persist or re-merge, otherwise each save
		// duplicates the env keys on top of the previous merge (#9071).
		if settings.ApiKeys != nil {
			envKeys := startupConfig.ApiKeys
			envSet := make(map[string]struct{}, len(envKeys))
			for _, k := range envKeys {
				envSet[k] = struct{}{}
			}
			runtimeOnly := make([]string, 0, len(*settings.ApiKeys))
			for _, k := range *settings.ApiKeys {
				if _, fromEnv := envSet[k]; fromEnv {
					continue
				}
				runtimeOnly = append(runtimeOnly, k)
			}
			settings.ApiKeys = &runtimeOnly
		}

		// Persist as a partial update: overlay only the fields this request set
		// onto the settings already on disk. Focused admin pages POST just the
		// keys they own (the Middleware proxy tab sends only mitm_listen; the
		// detector table only pii_default_detectors), so writing the request
		// body verbatim would null every unrelated setting (the no-omitempty
		// api_keys / pii_default_detectors fields even round-trip as JSON
		// null). The full Settings page still round-trips every field, so its
		// Save is unchanged.
		toPersist := persisted
		toPersist.MergeNonNil(settings)
		if err := appConfig.WritePersistedSettings(toPersist); err != nil {
			return c.JSON(http.StatusInternalServerError, schema.SettingsResponse{
				Success: false,
				Error:   "Failed to write settings file: " + err.Error(),
			})
		}

		// Apply settings using centralized method
		prevGalleries, prevBackendGalleries := appConfig.Galleries, appConfig.BackendGalleries
		// The legacy ApplyRuntimeSettings result drives live watchdog
		// reconfiguration, not a server restart. Resolver preference changes
		// take effect only at startup and must not cycle that unrelated service.
		prevDevelopmentPreference := appConfig.PreferDevelopmentBackends
		watchdogChanged := appConfig.ApplyRuntimeSettings(&settings)
		restartRequired := prevDevelopmentPreference != appConfig.PreferDevelopmentBackends
		gallery.ResetGalleryModelCacheIfChanged(prevGalleries, prevBackendGalleries, appConfig)
		if settings.OfficialLibraries != nil {
			// The shipped default sources of every kind follow the switch now.
			if err := app.ApplyLibraries(); err != nil {
				xlog.Warn("could not apply the official libraries setting", "error", err)
			}
		}
		if settings.VRAMPersistentCache != nil || settings.AutoloadGalleries != nil {
			if appConfig.VRAMPersistentCache && appConfig.AutoloadGalleries {
				vram.ConfigurePersistentCache(filepath.Join(appConfig.SystemState.Model.ModelsPath, "..", "cache", "vram"), 24*time.Hour)
			} else {
				vram.DisablePersistentCache()
			}
		}

		// Handle API keys specially (merge with startup keys)
		if settings.ApiKeys != nil {
			appConfig.ApiKeys = config.MergeAPIKeys(startupConfig.ApiKeys, *settings.ApiKeys)
		}

		// Update backend logging dynamically
		if settings.EnableBackendLogging != nil {
			app.ModelLoader().SetBackendLoggingEnabled(*settings.EnableBackendLogging)
			xlog.Info("Updated backend logging setting", "enableBackendLogging", *settings.EnableBackendLogging)
		}

		// Update watchdog dynamically for settings that don't require restart
		if settings.ForceEvictionWhenBusy != nil {
			currentWD := app.ModelLoader().GetWatchDog()
			if currentWD != nil {
				currentWD.SetForceEvictionWhenBusy(*settings.ForceEvictionWhenBusy)
				xlog.Info("Updated watchdog force eviction when busy setting", "forceEvictionWhenBusy", *settings.ForceEvictionWhenBusy)
			}
		}

		// Update ModelLoader LRU eviction retry settings dynamically
		maxRetries := appConfig.LRUEvictionMaxRetries
		retryInterval := appConfig.LRUEvictionRetryInterval
		if settings.LRUEvictionMaxRetries != nil {
			maxRetries = *settings.LRUEvictionMaxRetries
		}
		if settings.LRUEvictionRetryInterval != nil {
			if dur, err := time.ParseDuration(*settings.LRUEvictionRetryInterval); err == nil {
				retryInterval = dur
			}
		}
		if settings.LRUEvictionMaxRetries != nil || settings.LRUEvictionRetryInterval != nil {
			app.ModelLoader().SetLRUEvictionRetrySettings(maxRetries, retryInterval)
			xlog.Info("Updated LRU eviction retry settings", "maxRetries", maxRetries, "retryInterval", retryInterval)
		}

		// Update Open Responses store TTL dynamically
		if settings.OpenResponsesStoreTTL != nil {
			ttl := time.Duration(0)
			if *settings.OpenResponsesStoreTTL != "0" && *settings.OpenResponsesStoreTTL != "" {
				if dur, err := time.ParseDuration(*settings.OpenResponsesStoreTTL); err == nil {
					ttl = dur
				} else {
					xlog.Warn("Invalid Open Responses store TTL format", "ttl", *settings.OpenResponsesStoreTTL, "error", err)
				}
			}
			// Import the store package
			store := openresponses.GetGlobalStore()
			store.SetTTL(ttl)
			xlog.Info("Updated Open Responses store TTL", "ttl", ttl)
		}

		// Restart watchdog if settings changed.
		//
		// The live start/stop decision derives from the post-apply config
		// (WatchdogShouldRun) rather than the raw watchdog_enabled request
		// field: the React master toggle only ever writes the idle/busy flags,
		// so keying off watchdog_enabled left the live watchdog stopped on a
		// cold enable until the next restart (#9125). WatchdogShouldRun mirrors
		// the gating in startWatchdog, so a cold enable starts it immediately
		// and a full disable (both checks off, no LRU / memory reclaimer) stops
		// it.
		if watchdogChanged {
			if !appConfig.WatchdogShouldRun() {
				if err := app.StopWatchdog(); err != nil {
					xlog.Error("Failed to stop watchdog", "error", err)
					return c.JSON(http.StatusInternalServerError, schema.SettingsResponse{
						Success: false,
						Error:   "Settings saved but failed to stop watchdog: " + err.Error(),
					})
				}
			} else {
				if err := app.RestartWatchdog(); err != nil {
					xlog.Error("Failed to restart watchdog", "error", err)
					return c.JSON(http.StatusInternalServerError, schema.SettingsResponse{
						Success: false,
						Error:   "Settings saved but failed to restart watchdog: " + err.Error(),
					})
				}
			}
		}

		// Rebuild the MITM listener when its address OR the instance-wide
		// default detectors change. The per-host detector map is resolved once
		// at listener start (startMITMLocked → ResolvePIIPolicy), so a
		// default-detector change is otherwise invisible to cloud-proxy traffic
		// until the next restart — an admin toggling a default detector would
		// see no redaction. RestartMITM is a no-op when the listener is
		// disabled (empty address).
		if settings.MITMListen != nil || settings.PIIDefaultDetectors != nil {
			if err := app.RestartMITM(); err != nil {
				xlog.Error("Failed to restart MITM proxy", "error", err)
				return c.JSON(http.StatusInternalServerError, schema.SettingsResponse{
					Success: false,
					Error:   "Settings saved but failed to restart MITM proxy: " + err.Error(),
				})
			}
		}

		// Restart P2P if P2P settings changed
		p2pChanged := settings.P2PToken != nil || settings.P2PNetworkID != nil || settings.Federated != nil
		if p2pChanged {
			if settings.P2PToken != nil && *settings.P2PToken == "" {
				if err := app.StopP2P(); err != nil {
					xlog.Error("Failed to stop P2P", "error", err)
					return c.JSON(http.StatusInternalServerError, schema.SettingsResponse{
						Success: false,
						Error:   "Settings saved but failed to stop P2P: " + err.Error(),
					})
				}
			} else {
				if err := app.RestartP2P(); err != nil {
					xlog.Error("Failed to restart P2P", "error", err)
					return c.JSON(http.StatusInternalServerError, schema.SettingsResponse{
						Success: false,
						Error:   "Settings saved but failed to restart P2P: " + err.Error(),
					})
				}
			}
		}

		return c.JSON(http.StatusOK, schema.SettingsResponse{
			Success:         true,
			Message:         "Settings updated successfully",
			RestartRequired: restartRequired,
		})
	}
}

var addonIDShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func validAddonID(id string) bool { return addonIDShape.MatchString(id) }

// validateEgressSettings checks the chats_egress_* settings of a request and
// returns the error to show, or "". Turning chats_egress_allow_private on
// needs chats_egress_private_confirm with the typed phrase in the same body;
// the phrase is a person's act, it is not stored.
func validateEgressSettings(s config.RuntimeSettings, body []byte, privateOn bool) string {
	for name, v := range map[string]*string{"chats_egress_ceiling": s.ChatsEgressCeiling, "chats_egress_default": s.ChatsEgressDefault} {
		if v != nil && !config.ValidEgressPreset(*v) {
			return "Invalid " + name + ": use off, registries or all"
		}
	}
	if s.ChatsEgressPorts != nil {
		if _, err := config.ParseEgressPorts(*s.ChatsEgressPorts); err != nil {
			return "Invalid chats_egress_ports: " + err.Error()
		}
	}
	if s.ChatsEgressExtraRegistries != nil {
		if _, err := config.ParseEgressHosts(*s.ChatsEgressExtraRegistries); err != nil {
			return "Invalid chats_egress_extra_registries: " + err.Error()
		}
	}
	if s.ChatsEgressAuditDays != nil && (*s.ChatsEgressAuditDays < 1 || *s.ChatsEgressAuditDays > 365) {
		return "Invalid chats_egress_audit_days: use 1 to 365"
	}
	if s.ChatsMaxIterations != nil && (*s.ChatsMaxIterations < 1 || *s.ChatsMaxIterations > config.MaxChatsMaxIterations) {
		return fmt.Sprintf("Invalid chats_max_iterations: use 1 to %d", config.MaxChatsMaxIterations)
	}
	if s.ChatsEgressAllowPrivate != nil && *s.ChatsEgressAllowPrivate && !privateOn {
		var confirm struct {
			Phrase string `json:"chats_egress_private_confirm"`
		}
		_ = json.Unmarshal(body, &confirm)
		if strings.TrimSpace(confirm.Phrase) != config.EgressPrivatePhrase {
			return "Turning chats_egress_allow_private on needs chats_egress_private_confirm: \"" + config.EgressPrivatePhrase + "\""
		}
	}
	return ""
}
