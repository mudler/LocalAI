package launcher

import (
	"fmt"
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// EnvVar represents an environment variable
type EnvVar struct {
	Key   string
	Value string
}

// LauncherUI handles the user interface
type LauncherUI struct {
	// Status display
	statusLabel  *widget.Label
	versionLabel *widget.Label

	// Control buttons
	startStopButton *widget.Button
	webUIButton     *widget.Button
	updateButton    *widget.Button
	downloadButton  *widget.Button

	// Configuration
	modelsPathEntry   *widget.Entry
	backendsPathEntry *widget.Entry
	addressEntry      *widget.Entry
	logLevelSelect    *widget.Select

	// Environment Variables
	envVarsList              *widget.List
	envVarsData              []EnvVar
	newEnvKeyEntry           *widget.Entry
	newEnvValueEntry         *widget.Entry
	updateEnvironmentDisplay func()

	// Logs
	logText *widget.Entry

	// Progress
	progressBar *widget.ProgressBar

	// Update management
	latestVersion string

	// Reference to launcher
	launcher *Launcher
}

// NewLauncherUI creates a new UI instance
func NewLauncherUI() *LauncherUI {
	return &LauncherUI{
		statusLabel:       widget.NewLabel("Initializing..."),
		versionLabel:      widget.NewLabel("Version: Unknown"),
		startStopButton:   widget.NewButton("Start LocalAI", nil),
		webUIButton:       widget.NewButton("Open WebUI", nil),
		updateButton:      widget.NewButton("Check for Updates", nil),
		modelsPathEntry:   widget.NewEntry(),
		backendsPathEntry: widget.NewEntry(),
		addressEntry:      widget.NewEntry(),
		logLevelSelect:    widget.NewSelect([]string{"error", "warn", "info", "debug", "trace"}, nil),
		logText:           widget.NewMultiLineEntry(),
		progressBar:       widget.NewProgressBar(),
	}
}

// CreateMainUI creates the main UI layout
func (ui *LauncherUI) CreateMainUI(launcher *Launcher) *container.AppTabs {
	ui.launcher = launcher
	ui.setupBindings()

	// Main tab with status and controls
	mainTab := ui.createMainTab()

	// Configuration tab
	configTab := ui.createConfigTab()

	// Logs tab
	logsTab := ui.createLogsTab()

	// Updates tab
	updatesTab := ui.createUpdatesTab()

	tabs := container.NewAppTabs(
		container.NewTabItem("Main", mainTab),
		container.NewTabItem("Configuration", configTab),
		container.NewTabItem("Logs", logsTab),
		container.NewTabItem("Updates", updatesTab),
	)

	return tabs
}

// createMainTab creates the main control tab
func (ui *LauncherUI) createMainTab() *fyne.Container {
	// Status section
	statusCard := widget.NewCard("Status", "", container.NewVBox(
		ui.statusLabel,
		ui.versionLabel,
	))

	// Control buttons
	controlsCard := widget.NewCard("Controls", "", container.NewHBox(
		ui.startStopButton,
		ui.webUIButton,
	))

	return container.NewVBox(
		statusCard,
		controlsCard,
	)
}

// createConfigTab creates the configuration tab
func (ui *LauncherUI) createConfigTab() *fyne.Container {
	// Path configuration
	pathsCard := widget.NewCard("Paths", "", container.NewGridWithColumns(2,
		widget.NewLabel("Models Path:"),
		ui.modelsPathEntry,
		widget.NewLabel("Backends Path:"),
		ui.backendsPathEntry,
	))

	// Server configuration
	serverCard := widget.NewCard("Server", "", container.NewGridWithColumns(2,
		widget.NewLabel("Address:"),
		ui.addressEntry,
		widget.NewLabel("Log Level:"),
		ui.logLevelSelect,
	))

	// Save button
	saveButton := widget.NewButton("Save Configuration", func() {
		ui.saveConfiguration()
	})

	// Environment Variables section
	envCard := ui.createEnvironmentSection()

	return container.NewVBox(
		pathsCard,
		serverCard,
		envCard,
		saveButton,
	)
}

// createEnvironmentSection creates the environment variables section for the config tab
func (ui *LauncherUI) createEnvironmentSection() *fyne.Container {
	// Initialize environment variables widgets
	ui.newEnvKeyEntry = widget.NewEntry()
	ui.newEnvKeyEntry.SetPlaceHolder("Environment Variable Name")

	ui.newEnvValueEntry = widget.NewEntry()
	ui.newEnvValueEntry.SetPlaceHolder("Environment Variable Value")

	// Add button
	addButton := widget.NewButton("Add Environment Variable", func() {
		ui.addEnvironmentVariable()
	})

	// Environment variables list with delete buttons
	ui.envVarsData = []EnvVar{}

	// Create container for environment variables
	envVarsContainer := container.NewVBox()

	// Update function to rebuild the environment variables display
	ui.updateEnvironmentDisplay = func() {
		envVarsContainer.Objects = nil
		for i, envVar := range ui.envVarsData {
			index := i // Capture index for closure

			// Create row with label and delete button
			envLabel := widget.NewLabel(fmt.Sprintf("%s = %s", envVar.Key, envVar.Value))
			deleteBtn := widget.NewButton("Delete", func() {
				ui.confirmDeleteEnvironmentVariable(index)
			})
			deleteBtn.Importance = widget.DangerImportance

			row := container.NewBorder(nil, nil, nil, deleteBtn, envLabel)
			envVarsContainer.Add(row)
		}
		envVarsContainer.Refresh()
	}

	// Create a scrollable container for the environment variables
	envScroll := container.NewScroll(envVarsContainer)
	envScroll.SetMinSize(fyne.NewSize(400, 150))

	// Input section for adding new environment variables
	inputSection := container.NewVBox(
		container.NewGridWithColumns(2,
			ui.newEnvKeyEntry,
			ui.newEnvValueEntry,
		),
		addButton,
	)

	// Environment variables card
	envCard := widget.NewCard("Environment Variables", "", container.NewVBox(
		inputSection,
		widget.NewSeparator(),
		envScroll,
	))

	return container.NewVBox(envCard)
}

// addEnvironmentVariable adds a new environment variable
func (ui *LauncherUI) addEnvironmentVariable() {
	key := ui.newEnvKeyEntry.Text
	value := ui.newEnvValueEntry.Text

	if key == "" {
		dialog.ShowError(fmt.Errorf("environment variable name cannot be empty"), ui.launcher.window)
		return
	}

	// Check if key already exists
	for _, envVar := range ui.envVarsData {
		if envVar.Key == key {
			dialog.ShowError(fmt.Errorf("environment variable '%s' already exists", key), ui.launcher.window)
			return
		}
	}

	ui.envVarsData = append(ui.envVarsData, EnvVar{Key: key, Value: value})
	fyne.Do(func() {
		if ui.updateEnvironmentDisplay != nil {
			ui.updateEnvironmentDisplay()
		}
		// Clear input fields
		ui.newEnvKeyEntry.SetText("")
		ui.newEnvValueEntry.SetText("")
	})

	// Save to configuration
	ui.saveEnvironmentVariables()
}

// removeEnvironmentVariable removes an environment variable by index
func (ui *LauncherUI) removeEnvironmentVariable(index int) {
	if index >= 0 && index < len(ui.envVarsData) {
		ui.envVarsData = append(ui.envVarsData[:index], ui.envVarsData[index+1:]...)
		fyne.Do(func() {
			if ui.updateEnvironmentDisplay != nil {
				ui.updateEnvironmentDisplay()
			}
		})
		ui.saveEnvironmentVariables()
	}
}

// saveEnvironmentVariables saves environment variables to the configuration
func (ui *LauncherUI) saveEnvironmentVariables() {
	if ui.launcher == nil {
		return
	}

	config := ui.launcher.GetConfig()
	config.EnvironmentVars = make(map[string]string)

	for _, envVar := range ui.envVarsData {
		config.EnvironmentVars[envVar.Key] = envVar.Value
	}

	ui.launcher.SetConfig(config)
}

// confirmDeleteEnvironmentVariable shows confirmation dialog for deleting an environment variable
func (ui *LauncherUI) confirmDeleteEnvironmentVariable(index int) {
	if index >= 0 && index < len(ui.envVarsData) {
		envVar := ui.envVarsData[index]
		dialog.ShowConfirm("Remove Environment Variable",
			fmt.Sprintf("Remove environment variable '%s'?", envVar.Key),
			func(remove bool) {
				if remove {
					ui.removeEnvironmentVariable(index)
				}
			}, ui.launcher.window)
	}
}

// createLogsTab creates the logs display tab
func (ui *LauncherUI) createLogsTab() *fyne.Container {
	ui.logText.SetPlaceHolder("LocalAI logs will appear here...")
	ui.logText.Wrapping = fyne.TextWrapWord

	// Clear logs button
	clearButton := widget.NewButton("Clear Logs", func() {
		ui.logText.SetText("")
	})

	return container.NewVBox(
		clearButton,
		ui.logText,
	)
}

// createUpdatesTab creates the updates management tab
func (ui *LauncherUI) createUpdatesTab() *fyne.Container {
	// Update status
	updateStatus := widget.NewLabel("Click 'Check for Updates' to check for new versions")

	// Update controls
	checkButton := widget.NewButton("Check for Updates", func() {
		ui.checkForUpdates()
	})

	ui.downloadButton = widget.NewButton("Download Latest", func() {
		ui.downloadUpdate()
	})
	ui.downloadButton.Disable()

	// Progress bar
	ui.progressBar.Hide()

	return container.NewVBox(
		updateStatus,
		container.NewHBox(checkButton, ui.downloadButton),
		ui.progressBar,
	)
}

// setupBindings sets up event handlers for UI elements
func (ui *LauncherUI) setupBindings() {
	// Start/Stop button
	ui.startStopButton.OnTapped = func() {
		if ui.launcher.IsRunning() {
			ui.stopLocalAI()
		} else {
			ui.startLocalAI()
		}
	}

	// WebUI button
	ui.webUIButton.OnTapped = func() {
		ui.openWebUI()
	}
	ui.webUIButton.Disable() // Disabled until LocalAI is running

	// Update button
	ui.updateButton.OnTapped = func() {
		ui.checkForUpdates()
	}

	// Log level selection
	ui.logLevelSelect.OnChanged = func(selected string) {
		if ui.launcher != nil {
			config := ui.launcher.GetConfig()
			config.LogLevel = selected
			ui.launcher.SetConfig(config)
		}
	}
}

// startLocalAI starts the LocalAI service
func (ui *LauncherUI) startLocalAI() {
	fyne.Do(func() {
		ui.startStopButton.Disable()
	})
	ui.UpdateStatus("Starting LocalAI...")

	go func() {
		err := ui.launcher.StartLocalAI()
		if err != nil {
			ui.UpdateStatus("Failed to start: " + err.Error())
			fyne.DoAndWait(func() {
				dialog.ShowError(err, ui.launcher.window)
			})
		} else {
			fyne.Do(func() {
				ui.startStopButton.SetText("Stop LocalAI")
				ui.webUIButton.Enable()
			})
		}
		fyne.Do(func() {
			ui.startStopButton.Enable()
		})
	}()
}

// stopLocalAI stops the LocalAI service
func (ui *LauncherUI) stopLocalAI() {
	fyne.Do(func() {
		ui.startStopButton.Disable()
	})
	ui.UpdateStatus("Stopping LocalAI...")

	go func() {
		err := ui.launcher.StopLocalAI()
		if err != nil {
			fyne.DoAndWait(func() {
				dialog.ShowError(err, ui.launcher.window)
			})
		} else {
			fyne.Do(func() {
				ui.startStopButton.SetText("Start LocalAI")
				ui.webUIButton.Disable()
			})
		}
		fyne.Do(func() {
			ui.startStopButton.Enable()
		})
	}()
}

// openWebUI opens the LocalAI WebUI in the default browser
func (ui *LauncherUI) openWebUI() {
	webURL := ui.launcher.GetWebUIURL()
	parsedURL, err := url.Parse(webURL)
	if err != nil {
		dialog.ShowError(err, ui.launcher.window)
		return
	}

	// Open URL in default browser
	fyne.CurrentApp().OpenURL(parsedURL)
}

// saveConfiguration saves the current configuration
func (ui *LauncherUI) saveConfiguration() {
	config := ui.launcher.GetConfig()
	config.ModelsPath = ui.modelsPathEntry.Text
	config.BackendsPath = ui.backendsPathEntry.Text
	config.Address = ui.addressEntry.Text
	config.LogLevel = ui.logLevelSelect.Selected

	// Ensure environment variables are included in the configuration
	config.EnvironmentVars = make(map[string]string)
	for _, envVar := range ui.envVarsData {
		config.EnvironmentVars[envVar.Key] = envVar.Value
	}

	err := ui.launcher.SetConfig(config)
	if err != nil {
		dialog.ShowError(err, ui.launcher.window)
	} else {
		dialog.ShowInformation("Configuration", "Configuration saved successfully", ui.launcher.window)
	}
}

// checkForUpdates checks for available updates
func (ui *LauncherUI) checkForUpdates() {
	fyne.Do(func() {
		ui.updateButton.Disable()
	})
	ui.UpdateStatus("Checking for updates...")

	go func() {
		available, version, err := ui.launcher.CheckForUpdates()
		if err != nil {
			ui.UpdateStatus("Failed to check updates: " + err.Error())
			fyne.DoAndWait(func() {
				dialog.ShowError(err, ui.launcher.window)
			})
		} else if available {
			ui.latestVersion = version // Store the latest version
			ui.UpdateStatus("Update available: " + version)
			fyne.Do(func() {
				if ui.downloadButton != nil {
					ui.downloadButton.Enable()
				}
			})
			ui.NotifyUpdateAvailable(version)
		} else {
			ui.UpdateStatus("No updates available")
			fyne.DoAndWait(func() {
				dialog.ShowInformation("Updates", "You are running the latest version", ui.launcher.window)
			})
		}
		fyne.Do(func() {
			ui.updateButton.Enable()
		})
	}()
}

// downloadUpdate downloads the latest update
func (ui *LauncherUI) downloadUpdate() {
	// Use stored version or check for updates
	version := ui.latestVersion
	if version == "" {
		_, v, err := ui.launcher.CheckForUpdates()
		if err != nil {
			dialog.ShowError(err, ui.launcher.window)
			return
		}
		version = v
		ui.latestVersion = version
	}

	if version == "" {
		dialog.ShowError(fmt.Errorf("no version information available"), ui.launcher.window)
		return
	}

	// Disable buttons during download
	if ui.downloadButton != nil {
		fyne.Do(func() {
			ui.downloadButton.Disable()
		})
	}

	fyne.Do(func() {
		ui.progressBar.Show()
		ui.progressBar.SetValue(0)
	})
	ui.UpdateStatus("Downloading update " + version + "...")

	go func() {
		err := ui.launcher.DownloadUpdate(version, func(progress float64) {
			// Update progress bar
			fyne.Do(func() {
				ui.progressBar.SetValue(progress)
			})
			// Update status with percentage
			percentage := int(progress * 100)
			ui.UpdateStatus(fmt.Sprintf("Downloading update %s... %d%%", version, percentage))
		})

		fyne.Do(func() {
			ui.progressBar.Hide()
		})

		// Re-enable buttons after download
		if ui.downloadButton != nil {
			fyne.Do(func() {
				ui.downloadButton.Enable()
			})
		}

		if err != nil {
			fyne.DoAndWait(func() {
				ui.UpdateStatus("Failed to download update: " + err.Error())
				dialog.ShowError(err, ui.launcher.window)
			})
		} else {
			fyne.DoAndWait(func() {
				ui.UpdateStatus("Update downloaded successfully")
				dialog.ShowInformation("Update", "Update downloaded successfully. Please restart the launcher to use the new version.", ui.launcher.window)
			})
		}
	}()
}

// UpdateStatus updates the status label
func (ui *LauncherUI) UpdateStatus(status string) {
	if ui.statusLabel != nil {
		fyne.Do(func() {
			ui.statusLabel.SetText(status)
		})
	}
}

// OnLogUpdate handles new log content
func (ui *LauncherUI) OnLogUpdate(logLine string) {
	if ui.logText != nil {
		fyne.Do(func() {
			currentText := ui.logText.Text
			ui.logText.SetText(currentText + logLine)

			// Auto-scroll to bottom (simplified)
			ui.logText.CursorRow = len(ui.logText.Text)
		})
	}
}

// NotifyUpdateAvailable shows an update notification
func (ui *LauncherUI) NotifyUpdateAvailable(version string) {
	if ui.launcher != nil && ui.launcher.window != nil {
		fyne.DoAndWait(func() {
			dialog.ShowConfirm("Update Available",
				"A new version ("+version+") is available. Would you like to download it?",
				func(confirmed bool) {
					if confirmed {
						ui.downloadUpdate()
					}
				}, ui.launcher.window)
		})
	}
}

// LoadConfiguration loads the current configuration into UI elements
func (ui *LauncherUI) LoadConfiguration() {
	if ui.launcher == nil {
		return
	}

	config := ui.launcher.GetConfig()
	ui.modelsPathEntry.SetText(config.ModelsPath)
	ui.backendsPathEntry.SetText(config.BackendsPath)
	ui.addressEntry.SetText(config.Address)
	ui.logLevelSelect.SetSelected(config.LogLevel)

	// Load environment variables
	ui.envVarsData = []EnvVar{}
	for key, value := range config.EnvironmentVars {
		ui.envVarsData = append(ui.envVarsData, EnvVar{Key: key, Value: value})
	}
	if ui.updateEnvironmentDisplay != nil {
		fyne.Do(func() {
			ui.updateEnvironmentDisplay()
		})
	}

	// Update version display
	version := ui.launcher.GetCurrentVersion()
	ui.versionLabel.SetText("Version: " + version)
}

// UpdateRunningState updates UI based on LocalAI running state
func (ui *LauncherUI) UpdateRunningState(isRunning bool) {
	fyne.Do(func() {
		if isRunning {
			ui.startStopButton.SetText("Stop LocalAI")
			ui.webUIButton.Enable()
		} else {
			ui.startStopButton.SetText("Start LocalAI")
			ui.webUIButton.Disable()
		}
	})
}
