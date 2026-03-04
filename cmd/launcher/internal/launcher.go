package launcher

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// Config represents the launcher configuration
type Config struct {
	ModelsPath      string            `json:"models_path"`
	BackendsPath    string            `json:"backends_path"`
	Address         string            `json:"address"`
	AutoStart       bool              `json:"auto_start"`
	StartOnBoot     bool              `json:"start_on_boot"`
	LogLevel        string            `json:"log_level"`
	EnvironmentVars map[string]string `json:"environment_vars"`
	ShowWelcome     *bool             `json:"show_welcome"`
}

// Launcher represents the main launcher application
type Launcher struct {
	// Core components
	releaseManager *ReleaseManager
	config         *Config
	ui             *LauncherUI
	systray        *SystrayManager
	ctx            context.Context
	window         fyne.Window
	app            fyne.App

	// Process management
	localaiCmd    *exec.Cmd
	isRunning     bool
	logBuffer     *strings.Builder
	logMutex      sync.RWMutex
	statusChannel chan string

	// Logging
	logFile *os.File
	logPath string

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
	// Create logs directory in data folder
	dataPath := l.GetDataPath()
	logsDir := filepath.Join(dataPath, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Create log file with timestamp
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	l.logPath = filepath.Join(logsDir, fmt.Sprintf("localai_%s.log", timestamp))

	logFile, err := os.Create(l.logPath)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}

	l.logFile = logFile
	return nil
}

// Initialize sets up the launcher
func (l *Launcher) Initialize() error {
	if l.app == nil {
		return fmt.Errorf("app is nil")
	}
	log.Printf("Initializing launcher...")

	// Setup logging
	if err := l.setupLogging(); err != nil {
		return fmt.Errorf("failed to setup logging: %w", err)
	}

	// Load configuration
	log.Printf("Loading configuration...")
	if err := l.loadConfig(); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	log.Printf("Configuration loaded, current state: ModelsPath=%s, BackendsPath=%s, Address=%s, LogLevel=%s",
		l.config.ModelsPath, l.config.BackendsPath, l.config.Address, l.config.LogLevel)

	// Clean up any partial downloads
	log.Printf("Cleaning up partial downloads...")
	if err := l.releaseManager.CleanupPartialDownloads(); err != nil {
		log.Printf("Warning: failed to cleanup partial downloads: %v", err)
	}

	if l.config.StartOnBoot {
		l.StartLocalAI()
	}
	// Set default paths if not configured (only if not already loaded from config)
	if l.config.ModelsPath == "" {
		homeDir, _ := os.UserHomeDir()
		l.config.ModelsPath = filepath.Join(homeDir, ".localai", "models")
		log.Printf("Setting default ModelsPath: %s", l.config.ModelsPath)
	}
	if l.config.BackendsPath == "" {
		homeDir, _ := os.UserHomeDir()
		l.config.BackendsPath = filepath.Join(homeDir, ".localai", "backends")
		log.Printf("Setting default BackendsPath: %s", l.config.BackendsPath)
	}
	if l.config.Address == "" {
		l.config.Address = "127.0.0.1:8080"
		log.Printf("Setting default Address: %s", l.config.Address)
	}
	if l.config.LogLevel == "" {
		l.config.LogLevel = "info"
		log.Printf("Setting default LogLevel: %s", l.config.LogLevel)
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
		log.Printf("Warning: failed to save default configuration: %v", err)
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
	}

	// Check for updates periodically
	go l.periodicUpdateCheck()

	return nil
}

// StartLocalAI starts the LocalAI server
func (l *Launcher) StartLocalAI() error {
	if l.isRunning {
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

	// Build command arguments
	args := []string{
		"run",
		"--models-path", l.config.ModelsPath,
		"--backends-path", l.config.BackendsPath,
		"--address", l.config.Address,
		"--log-level", l.config.LogLevel,
	}

	l.localaiCmd = exec.CommandContext(l.ctx, binaryPath, args...)

	// Apply environment variables
	if len(l.config.EnvironmentVars) > 0 {
		env := os.Environ()
		for key, value := range l.config.EnvironmentVars {
			env = append(env, fmt.Sprintf("%s=%s", key, value))
		}
		l.localaiCmd.Env = env
	}

	// Setup logging
	stdout, err := l.localaiCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := l.localaiCmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the process
	if err := l.localaiCmd.Start(); err != nil {
		return fmt.Errorf("failed to start LocalAI: %w", err)
	}

	l.isRunning = true

	fyne.Do(func() {
		l.updateStatus("LocalAI is starting...")
		l.updateRunningState(true)
	})

	// Start log monitoring
	go l.monitorLogs(stdout, "STDOUT")
	go l.monitorLogs(stderr, "STDERR")

	// Monitor process with startup timeout
	go func() {
		// Wait for process to start or fail
		err := l.localaiCmd.Wait()
		l.isRunning = false
		fyne.Do(func() {
			l.updateRunningState(false)
			if err != nil {
				l.updateStatus(fmt.Sprintf("LocalAI stopped with error: %v", err))
			} else {
				l.updateStatus("LocalAI stopped")
			}
		})
	}()

	// Add startup timeout detection
	go func() {
		time.Sleep(10 * time.Second) // Wait 10 seconds for startup
		if l.isRunning {
			// Check if process is still alive
			if l.localaiCmd.Process != nil {
				if err := l.localaiCmd.Process.Signal(syscall.Signal(0)); err != nil {
					// Process is dead, mark as not running
					l.isRunning = false
					fyne.Do(func() {
						l.updateRunningState(false)
						l.updateStatus("LocalAI failed to start properly")
					})
				}
			}
		}
	}()

	return nil
}

// StopLocalAI stops the LocalAI server
func (l *Launcher) StopLocalAI() error {
	if !l.isRunning || l.localaiCmd == nil {
		return fmt.Errorf("LocalAI is not running")
	}

	// Gracefully terminate the process
	if err := l.localaiCmd.Process.Signal(os.Interrupt); err != nil {
		// If graceful termination fails, force kill
		if killErr := l.localaiCmd.Process.Kill(); killErr != nil {
			return fmt.Errorf("failed to kill LocalAI process: %w", killErr)
		}
	}

	l.isRunning = false
	fyne.Do(func() {
		l.updateRunningState(false)
		l.updateStatus("LocalAI stopped")
	})
	return nil
}

// IsRunning returns whether LocalAI is currently running
func (l *Launcher) IsRunning() bool {
	return l.isRunning
}

// Shutdown performs cleanup when the application is closing
func (l *Launcher) Shutdown() error {
	log.Printf("Launcher shutting down, stopping LocalAI...")

	// Stop LocalAI if it's running
	if l.isRunning {
		if err := l.StopLocalAI(); err != nil {
			log.Printf("Error stopping LocalAI during shutdown: %v", err)
		}
	}

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
	return l.config
}

// SetConfig updates the configuration
func (l *Launcher) SetConfig(config *Config) error {
	l.config = config
	return l.saveConfig()
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
	address := l.config.Address
	if strings.HasPrefix(address, ":") {
		address = "localhost" + address
	}
	if !strings.HasPrefix(address, "http") {
		address = "http://" + address
	}
	return address
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
func (l *Launcher) DownloadUpdate(version string, progressCallback func(float64)) error {
	return l.releaseManager.DownloadRelease(version, progressCallback)
}

// GetCurrentVersion returns the current installed version
func (l *Launcher) GetCurrentVersion() string {
	return l.releaseManager.GetInstalledVersion()
}

// GetCurrentStatus returns the current status
func (l *Launcher) GetCurrentStatus() string {
	select {
	case status := <-l.statusChannel:
		return status
	default:
		if l.isRunning {
			return "LocalAI is running"
		}
		return "Ready"
	}
}

// GetLastStatus returns the last known status without consuming from channel
func (l *Launcher) GetLastStatus() string {
	if l.isRunning {
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
		dialogWindow.Resize(fyne.NewSize(500, 350))
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
func (l *Launcher) showDownloadProgress(version, title string) {
	fyne.DoAndWait(func() {
		// Create progress window
		progressWindow := l.app.NewWindow("Downloading LocalAI")
		progressWindow.Resize(fyne.NewSize(400, 250))
		progressWindow.CenterOnScreen()
		progressWindow.SetCloseIntercept(func() {
			progressWindow.Close()
		})

		// Progress bar
		progressBar := widget.NewProgressBar()
		progressBar.SetValue(0)

		// Status label
		statusLabel := widget.NewLabel("Preparing download...")

		// Release notes button
		releaseNotesButton := widget.NewButton("View Release Notes", func() {
			releaseNotesURL, err := l.githubReleaseNotesURL(version)
			if err != nil {
				log.Printf("Failed to parse URL: %v", err)
				return
			}

			l.app.OpenURL(releaseNotesURL)
		})

		// Progress container
		progressContainer := container.NewVBox(
			widget.NewLabel(title),
			progressBar,
			statusLabel,
			widget.NewSeparator(),
			releaseNotesButton,
		)

		progressWindow.SetContent(progressContainer)
		progressWindow.Show()

		// Start download in background
		go func() {
			err := l.DownloadUpdate(version, func(progress float64) {
				// Update progress bar
				fyne.Do(func() {
					progressBar.SetValue(progress)
					percentage := int(progress * 100)
					statusLabel.SetText(fmt.Sprintf("Downloading... %d%%", percentage))
				})
			})

			// Handle completion
			fyne.Do(func() {
				if err != nil {
					statusLabel.SetText(fmt.Sprintf("Download failed: %v", err))
					// Show error dialog
					dialog.ShowError(err, progressWindow)
				} else {
					statusLabel.SetText("Download completed successfully!")
					progressBar.SetValue(1.0)

					// Show success dialog
					dialog.ShowConfirm("Installation Complete",
						"LocalAI has been downloaded and installed successfully. You can now start LocalAI from the launcher.",
						func(close bool) {
							progressWindow.Close()
							// Update status and refresh systray menu
							l.updateStatus("LocalAI installed successfully")

							if l.systray != nil {
								l.systray.recreateMenu()
							}
						}, progressWindow)
				}
			})
		}()
	})
}

// monitorLogs monitors the output of LocalAI and adds it to the log buffer
func (l *Launcher) monitorLogs(reader io.Reader, prefix string) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
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
		l.logMutex.Unlock()

		// Write to log file if available
		if l.logFile != nil {
			if _, err := l.logFile.WriteString(logLine); err != nil {
				log.Printf("Failed to write to log file: %v", err)
			}
		}

		fyne.Do(func() {
			// Notify UI of new log content
			if l.ui != nil {
				l.ui.OnLogUpdate(logLine)
			}

			// Check for startup completion
			if strings.Contains(line, "API server listening") {
				l.updateStatus("LocalAI is running")
			}
		})
	}
}

// updateStatus updates the status and notifies UI
func (l *Launcher) updateStatus(status string) {
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

// loadConfig loads configuration from file
func (l *Launcher) loadConfig() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	configPath := filepath.Join(homeDir, ".localai", "launcher.json")
	log.Printf("Loading config from: %s", configPath)

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Printf("Config file not found, creating default config")
		// Create default config
		return l.saveConfig()
	}

	// Load existing config
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	log.Printf("Config file content: %s", string(configData))

	log.Printf("loadConfig: about to unmarshal JSON data")
	if err := json.Unmarshal(configData, l.config); err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}
	log.Printf("loadConfig: JSON unmarshaled successfully")

	log.Printf("Loaded config: ModelsPath=%s, BackendsPath=%s, Address=%s, LogLevel=%s",
		l.config.ModelsPath, l.config.BackendsPath, l.config.Address, l.config.LogLevel)
	log.Printf("Environment vars: %v", l.config.EnvironmentVars)

	return nil
}

// saveConfig saves configuration to file
func (l *Launcher) saveConfig() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	configDir := filepath.Join(homeDir, ".localai")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Marshal config to JSON
	log.Printf("saveConfig: marshaling config with EnvironmentVars: %v", l.config.EnvironmentVars)
	configData, err := json.MarshalIndent(l.config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	log.Printf("saveConfig: JSON marshaled successfully, length: %d", len(configData))

	configPath := filepath.Join(configDir, "launcher.json")
	log.Printf("Saving config to: %s", configPath)
	log.Printf("Config content: %s", string(configData))

	if err := os.WriteFile(configPath, configData, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	log.Printf("Config saved successfully")
	return nil
}
