package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/mudler/LocalAI/pkg/workerstatus"
)

// Launcher represents the main launcher application
type Launcher struct {
	// Core components
	releaseManager *ReleaseManager
	configError    error
	config         *Config
	ui             *LauncherUI
	systray        *SystrayManager
	ctx            context.Context
	window         fyne.Window
	app            fyne.App

	// Process management: one serialized lifecycle owns the child.
	life     *lifecycle
	lifeOnce sync.Once
	lock     *userLock
	lockDir  string
	lockMu   sync.Mutex
	// applying is set while a stop-and-apply runs; start, save and update are
	// refused for that time.
	applying atomic.Bool
	// plaintextAck is the owner's per-session acknowledgement that plaintext
	// connections run inside a protected network. It is never saved.
	plaintextAck atomic.Bool
	statusMu     sync.Mutex
	statusMsg    string
	// statusPath and statusGeneration identify the current worker's private
	// status snapshot; tracker measures its freshness.
	statusPath       string
	statusGeneration string
	statusTracker    *workerstatus.Tracker
	// statusFromLogs is true only for a standalone runtime: a worker's state
	// comes from its status snapshot, never from log text.
	statusFromLogs bool
	logBuffer      *strings.Builder
	logMutex       sync.RWMutex
	statusChannel  chan string

	// Logging
	logFile  *os.File
	logPath  string
	logBytes int

	// UI state
	lastUpdateCheck time.Time
}

// NewLauncher creates a new launcher instance
func NewLauncher(ui *LauncherUI, window fyne.Window, app fyne.App) *Launcher {
	return &Launcher{
		releaseManager: NewReleaseManager(),
		config:         &Config{},
		logBuffer:      &strings.Builder{},
		statusChannel:  make(chan string, 100),
		ctx:            context.Background(),
		ui:             ui,
		window:         window,
		app:            app,
	}
}

// setupLogging sets up log file for LocalAI process output
func (l *Launcher) setupLogging() error {
	logFile, err := secureLogFile(l.GetDataPath())
	if err != nil {
		return err
	}
	l.logPath = logFile.Name()

	l.logFile = logFile
	return nil
}

// Initialize sets up the launcher
func (l *Launcher) Initialize() error {
	if l.app == nil {
		return fmt.Errorf("app is nil")
	}
	log.Printf("Initializing launcher...")

	// Verify settings storage before touching files beneath its directory.
	if err := l.loadConfig(); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if err := l.setupLogging(); err != nil {
		return fmt.Errorf("failed to setup logging: %w", err)
	}

	// Clean up any partial downloads
	log.Printf("Cleaning up partial downloads...")
	if err := l.releaseManager.CleanupPartialDownloads(); err != nil {
		log.Printf("Warning: failed to cleanup partial downloads: %v", err)
	}

	// Set default paths if not configured (only if not already loaded from config)
	if l.config.ModelsPath == "" {
		homeDir, _ := os.UserHomeDir()
		l.config.ModelsPath = filepath.Join(homeDir, ".localai", "models")
	}
	if l.config.BackendsPath == "" {
		homeDir, _ := os.UserHomeDir()
		l.config.BackendsPath = filepath.Join(homeDir, ".localai", "backends")
	}
	if l.config.Address == "" {
		l.config.Address = "127.0.0.1:8080"
	}
	if l.config.LogLevel == "" {
		l.config.LogLevel = "info"
	}
	if l.config.EnvironmentVars == nil {
		l.config.EnvironmentVars = make(map[string]string)
		log.Printf("Initializing empty EnvironmentVars map")
	}

	// Set default welcome window preference
	if l.config.ShowWelcome == nil {
		true := true
		l.config.ShowWelcome = &true
		log.Printf("Setting default ShowWelcome: true")
	}

	// Create directories
	os.MkdirAll(l.config.ModelsPath, 0755)
	os.MkdirAll(l.config.BackendsPath, 0755)

	// Save the configuration with default values
	if err := l.saveConfig(); err != nil {
		return err
	}

	// System tray is now handled in main.go using Fyne's built-in approach

	// Check if LocalAI is installed
	if !l.releaseManager.IsLocalAIInstalled() {
		log.Printf("No LocalAI installation found")
		fyne.Do(func() {
			l.updateStatus("No LocalAI installation found")
			if l.ui != nil {
				// Show dialog offering to download LocalAI
				l.showDownloadLocalAIDialog()
			}
		})
	} else if l.ShouldAutoStartServer() {
		// The launcher is a tray-only app: without this the user launches it,
		// sees no window and no server, and concludes it does nothing (#11673).
		log.Printf("Auto-starting LocalAI server")
		l.autoStartServer()
	}

	// Check for updates periodically
	go l.periodicUpdateCheck()

	return nil
}

// ShouldAutoStartServer reports whether the launcher should start the server
// without user interaction: at launcher startup and right after a fresh
// install. Unset means enabled; legacy StartOnBoot is inert.
func (l *Launcher) ShouldAutoStartServer() bool {
	if l.config == nil || l.configError != nil {
		return false
	}
	if l.config.Mode == roleWorker {
		// Joining a deployment is never a default: the owner must have chosen it.
		return l.config.AutoStart != nil && *l.config.AutoStart
	}
	if l.config.Mode != "" && l.config.Mode != roleStandalone {
		return false
	}
	return l.config.AutoStart == nil || *l.config.AutoStart
}

// autoStartServer starts LocalAI in the background and surfaces failures
// through the systray error dialog: during an auto-start there is no visible
// window for a regular error dialog to attach to.
func (l *Launcher) autoStartServer() {
	go func() {
		if err := l.StartLocalAI(); err != nil {
			log.Printf("Failed to auto-start LocalAI: %v", err)
			l.updateStatus(fmt.Sprintf("Failed to start LocalAI: %v", err))
			if l.systray != nil {
				l.systray.showStartupErrorDialog(err)
			}
		}
	}()
}

// StartLocalAI starts the LocalAI runtime for the selected mode.
func (l *Launcher) StartLocalAI() error {
	if l.configError != nil {
		return l.configError
	}
	if l.config == nil {
		return fmt.Errorf("launcher settings are missing")
	}
	if l.Applying() {
		return errLifecycleBusy
	}
	if l.IsRunning() {
		return fmt.Errorf("LocalAI is already running")
	}

	// Verify binary integrity before starting
	if err := l.releaseManager.VerifyInstalledBinary(); err != nil {
		// Binary is corrupted, remove it and offer to reinstall
		binaryPath := l.releaseManager.GetBinaryPath()
		if removeErr := os.Remove(binaryPath); removeErr != nil {
			log.Printf("Failed to remove corrupted binary: %v", removeErr)
		}
		return fmt.Errorf("LocalAI binary is corrupted: %v. Please reinstall LocalAI", err)
	}

	binaryPath := l.releaseManager.GetBinaryPath()
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		return fmt.Errorf("LocalAI binary not found. Please download a release first")
	}

	description, err := l.describeLaunch()
	if err != nil {
		return err
	}
	return l.launchRuntime(binaryPath, description)
}

// describeLaunch validates the active profile. A plaintext connection needs the
// owner's acknowledgement for this launcher session.
func (l *Launcher) describeLaunch() (launchDescription, error) {
	return buildLaunch(l.config, l.GetDataPath(), os.Environ(), l.plaintextAck.Load())
}

// SetPlaintextAcknowledged records that the owner accepts plaintext frontend or
// message-bus connections inside a protected network, for this session only.
func (l *Launcher) SetPlaintextAcknowledged(v bool) { l.plaintextAck.Store(v) }

// PlaintextAcknowledged reports the session acknowledgement.
func (l *Launcher) PlaintextAcknowledged() bool { return l.plaintextAck.Load() }

// Applying reports whether a stop-and-apply transition is under way.
func (l *Launcher) Applying() bool { return l.applying.Load() }

func (l *Launcher) runtimeContext() context.Context {
	if l.ctx == nil {
		return context.Background()
	}
	return l.ctx
}

func (l *Launcher) lifecycleController() *lifecycle {
	l.lifeOnce.Do(func() {
		if l.life == nil {
			l.life = newLifecycle()
		}
	})
	return l.life
}

// runUI runs f on the UI thread when an application exists.
func (l *Launcher) runUI(f func()) {
	if l.app == nil {
		f()
		return
	}
	fyne.Do(f)
}

// ensureLock takes the per-user launcher lock once and keeps it until
// Shutdown completes. A second launcher cannot start an overlapping role.
func (l *Launcher) ensureLock() error {
	l.lockMu.Lock()
	defer l.lockMu.Unlock()
	if l.lock != nil {
		return nil
	}
	dir := l.lockDir
	if dir == "" {
		d, err := configDirectory()
		if err != nil {
			return err
		}
		d.Close()
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir = filepath.Join(home, ".localai")
	}
	lock, err := acquireUserLock(dir)
	if err != nil {
		return err
	}
	l.lock = lock
	return nil
}

func (l *Launcher) releaseLock() {
	l.lockMu.Lock()
	defer l.lockMu.Unlock()
	if l.lock != nil {
		_ = l.lock.Release()
		l.lock = nil
	}
}

// launchRuntime starts one validated launch. A worker additionally needs a
// runtime that speaks the status contract and a private status channel; an
// older runtime is an upgrade error and never a log-parsing fallback.
func (l *Launcher) launchRuntime(binary string, d launchDescription) error {
	if err := l.ensureLock(); err != nil {
		return err
	}
	worker := d.role == roleWorker
	env := d.Env()
	var statusDir string
	if worker {
		if err := checkWorkerRuntime(l.runtimeContext(), binary); err != nil {
			return err
		}
		parent := filepath.Join(d.Dir(), "launcher-run")
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return fmt.Errorf("cannot create the worker status directory: %w", err)
		}
		path, generation, err := workerstatus.NewSession(parent)
		if err != nil {
			return err
		}
		statusDir = filepath.Dir(path)
		env = append(env, workerstatus.PathEnv+"="+path, workerstatus.GenerationEnv+"="+generation)
		l.statusMu.Lock()
		l.statusPath, l.statusGeneration = path, generation
		l.statusMu.Unlock()
	}

	filter := newLogFilter(d.secrets)
	spec := launchSpec{
		Binary:     binary,
		Args:       d.Args(),
		Env:        env,
		Dir:        d.Dir(),
		LedgerPath: d.ledger,
		Attach: func(cmd *exec.Cmd) error {
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				return fmt.Errorf("failed to create stdout pipe: %w", err)
			}
			stderr, err := cmd.StderrPipe()
			if err != nil {
				return fmt.Errorf("failed to create stderr pipe: %w", err)
			}
			go l.monitorFilteredLogs(stdout, "STDOUT", filter)
			go l.monitorFilteredLogs(stderr, "STDERR", newLogFilter(d.secrets))
			return nil
		},
		OnExit: func(generation uint64, err error) { l.runtimeExited(generation, err, statusDir) },
	}
	l.statusMu.Lock()
	l.statusFromLogs = !worker
	l.statusMu.Unlock()
	generation, err := l.lifecycleController().Start(spec)
	if err != nil {
		if statusDir != "" {
			_ = os.RemoveAll(statusDir)
		}
		return err
	}

	if worker {
		l.statusMu.Lock()
		l.statusTracker = &workerstatus.Tracker{Generation: l.statusGeneration, ExpectedPID: l.life.PID(), Started: time.Now()}
		l.statusMu.Unlock()
		go l.pollWorkerStatus(generation)
	}
	l.runUI(func() {
		if worker {
			l.updateStatus("Starting worker...")
		} else {
			l.updateStatus("LocalAI is starting...")
		}
		l.updateRunningState(true)
	})
	return nil
}

// runtimeExited is called once per run after cleanup finished or failed.
func (l *Launcher) runtimeExited(generation uint64, err error, statusDir string) {
	if errors.Is(err, errShutdownIncomplete) {
		l.runUI(func() { l.updateStatus("Failed: shutdown incomplete. Stop again to retry.") })
		return // keep the status directory: ownership is not resolved
	}
	if statusDir != "" {
		_ = os.RemoveAll(statusDir)
	}
	l.runUI(func() {
		l.updateRunningState(false)
		if err != nil {
			l.updateStatus(fmt.Sprintf("LocalAI stopped with error: %v", err))
		} else {
			l.updateStatus("LocalAI stopped")
		}
	})
}

// pollWorkerStatus shows the worker's evidence-backed state every two seconds
// while this generation is the live run. Results of older runs are dropped.
func (l *Launcher) pollWorkerStatus(generation uint64) {
	ticker := time.NewTicker(workerstatus.PollInterval)
	defer ticker.Stop()
	for range ticker.C {
		if !l.lifecycleController().Running(generation) {
			return
		}
		text := l.WorkerStatus()
		l.runUI(func() {
			if l.lifecycleController().Running(generation) {
				l.updateStatus(text)
			}
		})
	}
}

// WorkerStatus classifies the current worker snapshot into display text.
func (l *Launcher) WorkerStatus() string {
	l.statusMu.Lock()
	defer l.statusMu.Unlock()
	if l.statusTracker == nil || l.statusPath == "" {
		return "Starting worker..."
	}
	s, err := workerstatus.Read(l.statusPath, l.statusGeneration)
	if err != nil {
		return workerStateText("status_unavailable")
	}
	return workerStateText(l.statusTracker.Classify(s, time.Now()))
}

func workerStateText(state string) string {
	switch state {
	case "ready":
		return "Ready - waiting for work"
	case "starting":
		return "Starting worker..."
	case "pending":
		return "Waiting for approval"
	case "stopping":
		return "Stopping..."
	case "registration":
		return "Connection problem: registration"
	case "credentials":
		return "Connection problem: credentials"
	case "nats":
		return "Connection problem: message bus"
	case "subscriptions":
		return "Connection problem: lifecycle subscriptions"
	case "readiness":
		return "Connection problem: local readiness checks"
	case "heartbeat":
		return "Connection problem: heartbeat"
	default:
		return "Connection problem: status unavailable"
	}
}

// StopLocalAI runs the bounded stop sequence. It returns only after the
// runtime and its owned backend groups are confirmed gone, or reports that
// shutdown is incomplete and keeps further starts blocked.
func (l *Launcher) StopLocalAI() error {
	if l.lifecycleController().State() == lifecycleStopped {
		return fmt.Errorf("LocalAI is not running: %w", errNotRunning)
	}
	l.runUI(func() { l.updateStatus("Stopping...") })
	err := l.lifecycleController().Stop()
	if err != nil {
		l.runUI(func() { l.updateStatus("Failed: shutdown incomplete. Stop again to retry.") })
	}
	return err
}

// IsRunning reports whether a runtime, or unresolved ownership of one, exists.
// Stop stays available until shutdown is confirmed.
func (l *Launcher) IsRunning() bool {
	return l.lifecycleController().State() != lifecycleStopped
}

// Shutdown stops the runtime before the launcher exits. It does not claim
// success, and keeps the lock, when shutdown cannot be confirmed.
func (l *Launcher) Shutdown() error {
	log.Printf("Launcher shutting down, stopping LocalAI...")

	if l.IsRunning() {
		if err := l.StopLocalAI(); err != nil && !errors.Is(err, errNotRunning) {
			log.Printf("Error stopping LocalAI during shutdown: %v", err)
			return err
		}
	}
	l.releaseLock()

	// Close log file if open
	if l.logFile != nil {
		if err := l.logFile.Close(); err != nil {
			log.Printf("Error closing log file: %v", err)
		}
		l.logFile = nil
	}

	log.Printf("Launcher shutdown complete")
	return nil
}

// GetLogs returns the current log buffer
func (l *Launcher) GetLogs() string {
	l.logMutex.RLock()
	defer l.logMutex.RUnlock()
	return l.logBuffer.String()
}

// GetRecentLogs returns the most recent logs (last 50 lines) for better error display
func (l *Launcher) GetRecentLogs() string {
	l.logMutex.RLock()
	defer l.logMutex.RUnlock()

	content := l.logBuffer.String()
	lines := strings.Split(content, "\n")

	// Get last 50 lines
	if len(lines) > 50 {
		lines = lines[len(lines)-50:]
	}

	return strings.Join(lines, "\n")
}

// GetConfig returns the current configuration
func (l *Launcher) GetConfig() *Config {
	return l.config.clone()
}

// SetConfig updates the configuration
func (l *Launcher) SetConfig(config *Config) error {
	candidate := config.clone()
	if candidate == nil {
		return fmt.Errorf("launcher settings are missing")
	}
	if err := l.persistConfig(candidate); err != nil {
		l.configError = err
		return err
	}
	l.config = candidate
	l.configError = nil
	return nil
}

func (l *Launcher) GetUI() *LauncherUI {
	return l.ui
}

func (l *Launcher) SetSystray(systray *SystrayManager) {
	l.systray = systray
}

// GetReleaseManager returns the release manager
func (l *Launcher) GetReleaseManager() *ReleaseManager {
	return l.releaseManager
}

// GetWebUIURL returns the URL for the WebUI
func (l *Launcher) GetWebUIURL() string {
	if l.config != nil && l.config.Mode == roleWorker {
		// A worker hosts no UI: the browser goes to the deployment.
		u, _ := l.DeploymentURL()
		return u
	}
	address := l.config.Address
	if strings.HasPrefix(address, ":") {
		address = "localhost" + address
	}
	if !strings.HasPrefix(address, "http") {
		address = "http://" + address
	}
	return address
}

// BuildRunArgs assembles the argument list passed to `local-ai run`.
//
// Storage paths are anchored to the launcher's data directory instead of the
// server's own defaults. The server resolves data/config to ${basepath}
// (the launcher process CWD, often the user's home root) and generated-content
// /uploads to shared /tmp paths. On a shared /tmp (macOS routes /tmp to
// /private/tmp for every user) the first account to run LocalAI creates
// /tmp/generated with 0750 perms, so any other account then fails startup with
// "mkdir /tmp/generated/content: permission denied". Keeping every writable
// path under the per-user data directory avoids both the misplacement (#10610)
// and the cross-user /tmp collision.
func (l *Launcher) BuildRunArgs() []string {
	return standaloneArgs(l.config, l.GetDataPath())
}

// GetDataPath returns the path where LocalAI data and logs are stored
func (l *Launcher) GetDataPath() string {
	// LocalAI typically stores data in the current working directory or a models directory
	// First check if models path is configured
	if l.config != nil && l.config.ModelsPath != "" {
		// Return the parent directory of models path
		return filepath.Dir(l.config.ModelsPath)
	}

	// Fallback to home directory LocalAI folder
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(homeDir, ".localai")
}

// CheckForUpdates checks if there are any available updates
func (l *Launcher) CheckForUpdates() (bool, string, error) {
	log.Printf("CheckForUpdates: checking for available updates...")
	available, version, err := l.releaseManager.IsUpdateAvailable()
	if err != nil {
		log.Printf("CheckForUpdates: error occurred: %v", err)
		return false, "", err
	}
	log.Printf("CheckForUpdates: result - available=%v, version=%s", available, version)
	l.lastUpdateCheck = time.Now()
	return available, version, nil
}

// DownloadUpdate downloads the latest version
func (l *Launcher) DownloadUpdate(version string, progressCallback func(downloaded, total int64)) error {
	if l.Applying() || l.lifecycleController().State() == lifecycleStopping {
		return errLifecycleBusy
	}
	return l.releaseManager.DownloadRelease(version, progressCallback)
}

// GetCurrentVersion returns the current installed version
func (l *Launcher) GetCurrentVersion() string {
	return l.releaseManager.GetInstalledVersion()
}

// StatusText is the last status shown, without consuming it.
func (l *Launcher) StatusText() string {
	l.statusMu.Lock()
	defer l.statusMu.Unlock()
	return l.statusMsg
}

// GetCurrentStatus returns the current status
func (l *Launcher) GetCurrentStatus() string {
	select {
	case status := <-l.statusChannel:
		return status
	default:
		if l.IsRunning() {
			return "LocalAI is running"
		}
		return "Ready"
	}
}

// GetLastStatus returns the last known status without consuming from channel
func (l *Launcher) GetLastStatus() string {
	if l.IsRunning() {
		return "LocalAI is running"
	}

	// Check if LocalAI is installed
	if !l.releaseManager.IsLocalAIInstalled() {
		return "LocalAI not installed"
	}

	return "Ready"
}

func (l *Launcher) githubReleaseNotesURL(version string) (*url.URL, error) {
	// Construct GitHub release URL
	releaseURL := fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s",
		l.releaseManager.GitHubOwner,
		l.releaseManager.GitHubRepo,
		version)

	// Convert string to *url.URL
	return url.Parse(releaseURL)
}

// showDownloadLocalAIDialog shows a dialog offering to download LocalAI
func (l *Launcher) showDownloadLocalAIDialog() {
	if l.app == nil {
		log.Printf("Cannot show download dialog: app is nil")
		return
	}

	fyne.DoAndWait(func() {
		// Create a standalone window for the download dialog
		dialogWindow := l.app.NewWindow("LocalAI Installation Required")
		dialogWindow.CenterOnScreen()
		dialogWindow.SetCloseIntercept(func() {
			dialogWindow.Close()
		})

		// Create the dialog content
		titleLabel := widget.NewLabel("LocalAI Not Found")
		titleLabel.TextStyle = fyne.TextStyle{Bold: true}
		titleLabel.Alignment = fyne.TextAlignCenter

		messageLabel := widget.NewLabel("LocalAI is not installed on your system.\n\nWould you like to download and install the latest version?")
		messageLabel.Wrapping = fyne.TextWrapWord
		messageLabel.Alignment = fyne.TextAlignCenter

		// Buttons
		downloadButton := widget.NewButton("Download & Install", func() {
			dialogWindow.Close()
			l.downloadAndInstallLocalAI()
			if l.systray != nil {
				l.systray.recreateMenu()
			}
		})
		downloadButton.Importance = widget.HighImportance

		// Release notes button
		releaseNotesButton := widget.NewButton("View Release Notes", func() {
			// Get latest release info and open release notes
			go func() {
				release, err := l.releaseManager.GetLatestRelease()
				if err != nil {
					log.Printf("Failed to get latest release info: %v", err)
					return
				}

				releaseNotesURL, err := l.githubReleaseNotesURL(release.Version)
				if err != nil {
					log.Printf("Failed to parse URL: %v", err)
					return
				}

				l.app.OpenURL(releaseNotesURL)
			}()
		})

		skipButton := widget.NewButton("Skip for Now", func() {
			dialogWindow.Close()
		})

		// Layout - put release notes button above the main action buttons
		actionButtons := container.NewHBox(skipButton, downloadButton)
		content := container.NewVBox(
			titleLabel,
			widget.NewSeparator(),
			messageLabel,
			widget.NewSeparator(),
			releaseNotesButton,
			widget.NewSeparator(),
			actionButtons,
		)

		dialogWindow.SetContent(content)
		resizeToContent(dialogWindow, content)
		dialogWindow.Show()
	})
}

// downloadAndInstallLocalAI downloads and installs the latest LocalAI version
func (l *Launcher) downloadAndInstallLocalAI() {
	if l.app == nil {
		log.Printf("Cannot download LocalAI: app is nil")
		return
	}

	// First check what the latest version is
	go func() {
		log.Printf("Checking for latest LocalAI version...")
		available, version, err := l.CheckForUpdates()
		if err != nil {
			log.Printf("Failed to check for updates: %v", err)
			l.showDownloadError("Failed to check for latest version", err.Error())
			return
		}

		if !available {
			log.Printf("No updates available, but LocalAI is not installed")
			l.showDownloadError("No Version Available", "Could not determine the latest LocalAI version. Please check your internet connection and try again.")
			return
		}

		log.Printf("Latest version available: %s", version)
		// Show progress window with the specific version
		l.showDownloadProgress(version, fmt.Sprintf("Downloading LocalAI %s...", version))
	}()
}

// showDownloadError shows an error dialog for download failures
func (l *Launcher) showDownloadError(title, message string) {
	fyne.DoAndWait(func() {
		// Create error window
		errorWindow := l.app.NewWindow("Download Error")
		errorWindow.Resize(fyne.NewSize(400, 200))
		errorWindow.CenterOnScreen()
		errorWindow.SetCloseIntercept(func() {
			errorWindow.Close()
		})

		// Error content
		titleLabel := widget.NewLabel(title)
		titleLabel.TextStyle = fyne.TextStyle{Bold: true}
		titleLabel.Alignment = fyne.TextAlignCenter

		messageLabel := widget.NewLabel(message)
		messageLabel.Wrapping = fyne.TextWrapWord
		messageLabel.Alignment = fyne.TextAlignCenter

		// Close button
		closeButton := widget.NewButton("Close", func() {
			errorWindow.Close()
		})

		// Layout
		content := container.NewVBox(
			titleLabel,
			widget.NewSeparator(),
			messageLabel,
			widget.NewSeparator(),
			closeButton,
		)

		errorWindow.SetContent(content)
		errorWindow.Show()
	})
}

// showDownloadProgress shows a standalone progress window for downloading LocalAI
// after a fresh install (no LocalAI binary present yet).
func (l *Launcher) showDownloadProgress(version, title string) {
	l.showDownloadProgressWindow(version, title, func(win fyne.Window) {
		message := "LocalAI has been downloaded and installed successfully. You can now start LocalAI from the launcher."
		if l.ShouldAutoStartServer() {
			message = "LocalAI has been downloaded and installed successfully. It will start now: manage it and open the WebUI from the system tray icon."
		}
		dialog.ShowConfirm("Installation Complete", message,
			func(bool) {
				win.Close()
				l.updateStatus("LocalAI installed successfully")
				if l.systray != nil {
					l.systray.recreateMenu()
				}
				// A fresh install should end with a running server, not with
				// the user hunting for a start button in the tray (#11673).
				if l.ShouldAutoStartServer() && !l.IsRunning() {
					l.autoStartServer()
				}
			}, win)
	})
}

// showDownloadProgressWindow renders the download progress popup shared by every
// "download/upgrade LocalAI" entry point. It owns the progress bar, the
// human-readable byte readout, resume-aware retry, and content-fit window
// sizing so the behaviour stays identical everywhere. onSuccess runs (on the UI
// goroutine) once the download verifies, and is responsible for the success
// dialog and any follow-up; the window is passed in so it can be parented/closed.
func (l *Launcher) showDownloadProgressWindow(version, title string, onSuccess func(win fyne.Window)) {
	fyne.DoAndWait(func() {
		progressWindow := l.app.NewWindow("Downloading LocalAI")
		progressWindow.CenterOnScreen()
		progressWindow.SetCloseIntercept(func() {
			progressWindow.Close()
		})

		progressBar := widget.NewProgressBar()
		progressBar.SetValue(0)

		// Status label. Truncate with an ellipsis so a long "Download failed:
		// <url>" message can't stretch the window (and progress bar) to fit the
		// whole error on one line.
		statusLabel := widget.NewLabel("Preparing download...")
		statusLabel.Truncation = fyne.TextTruncateEllipsis

		releaseNotesButton := widget.NewButton("View Release Notes", func() {
			releaseNotesURL, err := l.githubReleaseNotesURL(version)
			if err != nil {
				log.Printf("Failed to parse URL: %v", err)
				return
			}
			l.app.OpenURL(releaseNotesURL)
		})

		// Retry button: hidden until a download fails. GitHub downloads are
		// flaky, and the underlying download resumes from the partial file, so
		// a retry continues where it left off rather than starting over.
		retryButton := widget.NewButton("Retry", nil)
		retryButton.Importance = widget.HighImportance
		retryButton.Hide()

		buttonRow := container.NewHBox(releaseNotesButton, retryButton)
		content := container.NewVBox(
			widget.NewLabel(title),
			progressBar,
			statusLabel,
			widget.NewSeparator(),
			buttonRow,
		)
		progressWindow.SetContent(content)
		resizeToContent(progressWindow, content)

		var startDownload func()
		startDownload = func() {
			retryButton.Hide()
			progressBar.SetValue(0)
			statusLabel.SetText("Preparing download...")
			resizeToContent(progressWindow, content)

			go func() {
				err := l.DownloadUpdate(version, func(downloaded, total int64) {
					fyne.Do(func() {
						if total > 0 {
							progressBar.SetValue(float64(downloaded) / float64(total))
							statusLabel.SetText(fmt.Sprintf("Downloading… %s / %s", formatBytes(downloaded), formatBytes(total)))
						} else {
							statusLabel.SetText(fmt.Sprintf("Downloading… %s", formatBytes(downloaded)))
						}
					})
				})

				fyne.Do(func() {
					if err != nil {
						statusLabel.SetText(fmt.Sprintf("Download failed: %v", err))
						retryButton.Show()
						resizeToContent(progressWindow, content)
						return
					}
					progressBar.SetValue(1.0)
					statusLabel.SetText("Download complete")
					onSuccess(progressWindow)
				})
			}()
		}
		retryButton.OnTapped = startDownload

		progressWindow.Show()
		startDownload()
	})
}

// resizeToContent sizes a window to fit its content (with a sane minimum width)
// so the dialog doesn't show a large blank gap below the last widget.
func resizeToContent(w fyne.Window, content fyne.CanvasObject) {
	size := content.MinSize()
	if size.Width < 400 {
		size.Width = 400
	}
	w.Resize(size)
}

// formatBytes renders a byte count as a human-readable size (e.g. "12.3 MB").
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// monitorLogs monitors the output of LocalAI and adds it to the log buffer
func (l *Launcher) monitorLogs(reader io.Reader, prefix string) {
	l.monitorFilteredLogs(reader, prefix, newLogFilter(nil))
}

func (l *Launcher) monitorFilteredLogs(reader io.Reader, prefix string, filter logFilter) {
	filter.consume(reader, func(line string) {
		timestamp := time.Now().Format("15:04:05")
		logLine := fmt.Sprintf("[%s] %s: %s\n", timestamp, prefix, line)

		l.logMutex.Lock()
		l.logBuffer.WriteString(logLine)
		// Keep log buffer size reasonable
		if l.logBuffer.Len() > 100000 { // 100KB
			content := l.logBuffer.String()
			// Keep last 50KB
			if len(content) > 50000 {
				l.logBuffer.Reset()
				l.logBuffer.WriteString(content[len(content)-50000:])
			}
		}

		// Write to log file if available
		if l.logFile != nil && l.logBytes+len(logLine) <= maxLogFile {
			n, _ := l.logFile.WriteString(logLine)
			l.logBytes += n
		}
		l.logMutex.Unlock()

		fyne.Do(func() {
			// Notify UI of new log content
			if l.ui != nil {
				l.ui.OnLogUpdate(logLine)
			}

			// Check for startup completion. A worker never takes its state from
			// log text.
			l.statusMu.Lock()
			fromLogs := l.statusFromLogs
			l.statusMu.Unlock()
			if fromLogs && strings.Contains(line, "API server listening") {
				l.updateStatus("LocalAI is running")
			}
		})
	})
}

// updateStatus updates the status and notifies UI
func (l *Launcher) updateStatus(status string) {
	l.statusMu.Lock()
	l.statusMsg = status
	l.statusMu.Unlock()
	select {
	case l.statusChannel <- status:
	default:
		// Channel full, skip
	}

	if l.ui != nil {
		l.ui.UpdateStatus(status)
	}

	if l.systray != nil {
		l.systray.UpdateStatus(status)
	}
}

// updateRunningState updates the running state in UI and systray
func (l *Launcher) updateRunningState(isRunning bool) {
	if l.ui != nil {
		l.ui.UpdateRunningState(isRunning)
	}

	if l.systray != nil {
		l.systray.UpdateRunningState(isRunning)
	}
}

// periodicUpdateCheck checks for updates periodically
func (l *Launcher) periodicUpdateCheck() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			available, version, err := l.CheckForUpdates()
			if err == nil && available {
				fyne.Do(func() {
					l.updateStatus(fmt.Sprintf("Update available: %s", version))
					if l.systray != nil {
						l.systray.NotifyUpdateAvailable(version)
					}
					if l.ui != nil {
						l.ui.NotifyUpdateAvailable(version)
					}
				})
			}
		case <-l.ctx.Done():
			return
		}
	}
}

// DeploymentURL is the saved frontend URL, validated, without credentials or a
// guessed internal route. The browser handles its own login.
func (l *Launcher) DeploymentURL() (string, error) {
	if l.config == nil {
		return "", fmt.Errorf("launcher settings are missing")
	}
	return launchURL(l.config.Worker.Connection.FrontendURL, false)
}

// errStopRequired means the change needs the running role stopped first.
var errStopRequired = errors.New("the running role must be stopped to apply this change")

// activeProfileChanged reports whether a change affects what the running role
// was launched with: a role switch, or any shared or active-profile setting.
func activeProfileChanged(old, next *Config) bool {
	role := func(c *Config) string {
		if c.Mode == "" {
			return roleStandalone
		}
		return c.Mode
	}
	if role(old) != role(next) {
		return true
	}
	if old.ModelsPath != next.ModelsPath || old.BackendsPath != next.BackendsPath || old.LogLevel != next.LogLevel {
		return true
	}
	if role(old) == roleWorker {
		return !reflect.DeepEqual(old.Worker, next.Worker)
	}
	return old.Address != next.Address || !reflect.DeepEqual(old.EnvironmentVars, next.EnvironmentVars)
}

// ApplySettings commits candidate. When a runtime exists and the change affects
// it, nothing is committed until the caller confirms a stop (stop=true) and the
// stop is verified. It never starts the replacement. Cancelling, a failed stop
// and an incomplete shutdown leave the active configuration unchanged.
func (l *Launcher) ApplySettings(candidate *Config, stop bool) error {
	if !l.applying.CompareAndSwap(false, true) {
		return errLifecycleBusy
	}
	defer l.applying.Store(false)
	state := l.lifecycleController().State()
	if state == lifecycleStarting || state == lifecycleStopping {
		return errLifecycleBusy
	}
	if state != lifecycleStopped && l.config != nil && activeProfileChanged(l.config, candidate) {
		if !stop {
			return errStopRequired
		}
		if err := l.StopLocalAI(); err != nil && !errors.Is(err, errNotRunning) {
			return err
		}
	}
	return l.SetConfig(candidate)
}
