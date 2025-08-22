package launcher

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
)

// Config represents the launcher configuration
type Config struct {
	ModelsPath      string            `json:"models_path"`
	BackendsPath    string            `json:"backends_path"`
	Address         string            `json:"address"`
	AutoStart       bool              `json:"auto_start"`
	LogLevel        string            `json:"log_level"`
	EnvironmentVars map[string]string `json:"environment_vars"`
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

	// Process management
	localaiCmd    *exec.Cmd
	isRunning     bool
	logBuffer     *strings.Builder
	logMutex      sync.RWMutex
	statusChannel chan string

	// UI state
	lastUpdateCheck time.Time
}

// NewLauncher creates a new launcher instance
func NewLauncher() *Launcher {
	return &Launcher{
		releaseManager: NewReleaseManager(),
		config:         &Config{},
		logBuffer:      &strings.Builder{},
		statusChannel:  make(chan string, 100),
		ctx:            context.Background(),
	}
}

// Initialize sets up the launcher
func (l *Launcher) Initialize() error {
	// Load configuration
	if err := l.loadConfig(); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Set default paths if not configured
	if l.config.ModelsPath == "" {
		homeDir, _ := os.UserHomeDir()
		l.config.ModelsPath = filepath.Join(homeDir, ".localai", "models")
	}
	if l.config.BackendsPath == "" {
		homeDir, _ := os.UserHomeDir()
		l.config.BackendsPath = filepath.Join(homeDir, ".localai", "backends")
	}
	if l.config.Address == "" {
		l.config.Address = ":8080"
	}
	if l.config.LogLevel == "" {
		l.config.LogLevel = "info"
	}
	if l.config.EnvironmentVars == nil {
		l.config.EnvironmentVars = make(map[string]string)
	}

	// Create directories
	os.MkdirAll(l.config.ModelsPath, 0755)
	os.MkdirAll(l.config.BackendsPath, 0755)

	// System tray is now handled in main.go using Fyne's built-in approach

	// Check if LocalAI is installed
	if !l.releaseManager.IsLocalAIInstalled() {
		l.updateStatus("No LocalAI installation found")
		if l.ui != nil {
			// Offer to download the latest version
			go func() {
				time.Sleep(1 * time.Second) // Wait for UI to be ready
				available, version, err := l.CheckForUpdates()
				if err == nil && available {
					l.ui.NotifyUpdateAvailable(version)
				}
			}()
		}
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
	l.updateStatus("LocalAI is starting...")
	l.updateRunningState(true)

	// Start log monitoring
	go l.monitorLogs(stdout, "STDOUT")
	go l.monitorLogs(stderr, "STDERR")

	// Monitor process
	go func() {
		err := l.localaiCmd.Wait()
		l.isRunning = false
		l.updateRunningState(false)
		if err != nil {
			l.updateStatus(fmt.Sprintf("LocalAI stopped with error: %v", err))
		} else {
			l.updateStatus("LocalAI stopped")
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
	l.updateRunningState(false)
	l.updateStatus("LocalAI stopped")
	return nil
}

// IsRunning returns whether LocalAI is currently running
func (l *Launcher) IsRunning() bool {
	return l.isRunning
}

// GetLogs returns the current log buffer
func (l *Launcher) GetLogs() string {
	l.logMutex.RLock()
	defer l.logMutex.RUnlock()
	return l.logBuffer.String()
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

// CheckForUpdates checks if there are any available updates
func (l *Launcher) CheckForUpdates() (bool, string, error) {
	available, version, err := l.releaseManager.IsUpdateAvailable()
	if err != nil {
		return false, "", err
	}
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

		// Notify UI of new log content
		if l.ui != nil {
			l.ui.OnLogUpdate(logLine)
		}

		// Check for startup completion
		if strings.Contains(line, "API server listening") {
			l.updateStatus("LocalAI is running")
		}
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
	ticker := time.NewTicker(4 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			available, version, err := l.CheckForUpdates()
			if err == nil && available {
				l.updateStatus(fmt.Sprintf("Update available: %s", version))
				if l.ui != nil {
					l.ui.NotifyUpdateAvailable(version)
				}
			}
		case <-l.ctx.Done():
			return
		}
	}
}

// loadConfig loads configuration from file
func (l *Launcher) loadConfig() error {
	homeDir, _ := os.UserHomeDir()
	configPath := filepath.Join(homeDir, ".localai", "launcher.json")

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Create default config
		return l.saveConfig()
	}

	// Load existing config (simplified - would use json.Unmarshal in real implementation)
	// For now, return nil to use defaults
	return nil
}

// saveConfig saves configuration to file
func (l *Launcher) saveConfig() error {
	homeDir, _ := os.UserHomeDir()
	configDir := filepath.Join(homeDir, ".localai")
	os.MkdirAll(configDir, 0755)

	// Save config (simplified - would use json.Marshal in real implementation)
	// For now, just return nil
	return nil
}
