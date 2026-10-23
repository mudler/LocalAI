package launcher

import (
	"errors"
	"fmt"
	"log"
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
	autoStartCheck    *widget.Check
	startOnBootCheck  *widget.Check

	// Mode selection and the worker profile. Both profiles keep their drafts in
	// these widgets when the owner switches.
	modeRadio       *widget.RadioGroup
	localBox        *fyne.Container
	workerBox       *fyne.Container
	frontendEntry   *widget.Entry
	tokenEntry      *widget.Entry
	clearTokenCheck *widget.Check
	advertisedEntry *widget.Entry
	nodeNameEntry   *widget.Entry
	grpcMaxEntry    *widget.Entry
	natsURLEntry    *widget.Entry
	tlsCAEntry      *widget.Entry
	tlsCertEntry    *widget.Entry
	tlsKeyEntry     *widget.Entry
	jwtEntry        *widget.Entry
	clearJWTCheck   *widget.Check
	seedEntry       *widget.Entry
	clearSeedCheck  *widget.Check
	workerEnvEntry  *widget.Entry
	plaintextCheck  *widget.Check
	previewLabel    *widget.Label
	joinExplanation *widget.Label

	// Environment Variables
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
	// Truncate the status text with an ellipsis. Status messages can carry a
	// download error containing a long, unbreakable URL/path; without this the
	// label demands the full single-line width and stretches the window (and
	// the progress bar) arbitrarily wide. The full error is still shown in the
	// error dialog.
	statusLabel := widget.NewLabel("Initializing...")
	statusLabel.Truncation = fyne.TextTruncateEllipsis

	maskedEntry := func() *widget.Entry {
		e := widget.NewPasswordEntry()
		return e
	}
	return &LauncherUI{
		modeRadio:         widget.NewRadioGroup([]string{modeLocalLabel, modeJoinLabel}, nil),
		frontendEntry:     widget.NewEntry(),
		tokenEntry:        maskedEntry(),
		clearTokenCheck:   widget.NewCheck("Clear the saved registration token", nil),
		advertisedEntry:   widget.NewEntry(),
		nodeNameEntry:     widget.NewEntry(),
		grpcMaxEntry:      widget.NewEntry(),
		natsURLEntry:      widget.NewEntry(),
		tlsCAEntry:        widget.NewEntry(),
		tlsCertEntry:      widget.NewEntry(),
		tlsKeyEntry:       widget.NewEntry(),
		jwtEntry:          maskedEntry(),
		clearJWTCheck:     widget.NewCheck("Clear the saved NATS JWT", nil),
		seedEntry:         maskedEntry(),
		clearSeedCheck:    widget.NewCheck("Clear the saved NATS user seed", nil),
		workerEnvEntry:    widget.NewMultiLineEntry(),
		plaintextCheck:    widget.NewCheck("I accept plaintext http:// or nats:// inside a protected network (this session only)", nil),
		previewLabel:      widget.NewLabel(""),
		joinExplanation:   widget.NewLabel("Joining contributes this machine's inference capacity to an existing deployment. It does not create a deployment."),
		statusLabel:       statusLabel,
		versionLabel:      widget.NewLabel("Version: Unknown"),
		startStopButton:   widget.NewButton("Start LocalAI", nil),
		webUIButton:       widget.NewButton("Open WebUI", nil),
		updateButton:      widget.NewButton("Check for Updates", nil),
		modelsPathEntry:   widget.NewEntry(),
		backendsPathEntry: widget.NewEntry(),
		addressEntry:      widget.NewEntry(),
		logLevelSelect:    widget.NewSelect([]string{"error", "warn", "info", "debug", "trace"}, nil),
		autoStartCheck:    widget.NewCheck("Start selected mode when launcher opens", nil),
		startOnBootCheck:  widget.NewCheck("Start LocalAI on system boot", nil),
		logText:           widget.NewMultiLineEntry(),
		progressBar:       widget.NewProgressBar(),
		envVarsData:       []EnvVar{}, // Initialize the environment variables slice
	}
}

// CreateMainUI creates the main UI layout
func (ui *LauncherUI) CreateMainUI(launcher *Launcher) *fyne.Container {
	ui.launcher = launcher
	ui.setupBindings()

	// Main tab with status and controls
	// Configuration is now the main content
	configTab := ui.createConfigTab()

	// Create a simple container instead of tabs since we only have settings
	tabs := container.NewVBox(
		widget.NewCard("LocalAI Launcher Settings", "", configTab),
	)

	return tabs
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

	// Server configuration (local mode)
	serverCard := widget.NewCard("Server", "", container.NewGridWithColumns(2,
		widget.NewLabel("Address:"),
		ui.addressEntry,
	))
	// Shared by both modes
	sharedCard := widget.NewCard("Startup", "", container.NewVBox(
		container.NewGridWithColumns(2,
			widget.NewLabel("Log Level:"),
			ui.logLevelSelect,
		),
		ui.autoStartCheck,
		ui.startOnBootCheck,
	))

	// Save button
	saveButton := widget.NewButton("Save Configuration", func() {
		ui.saveConfiguration()
	})

	// Environment Variables section
	envCard := ui.createEnvironmentSection()

	ui.localBox = container.NewVBox(serverCard, envCard)
	ui.workerBox = ui.createWorkerSection()
	ui.workerBox.Hide()
	ui.modeRadio.Horizontal = true
	ui.modeRadio.Required = true
	ui.modeRadio.OnChanged = func(selected string) { ui.showMode(selected) }
	ui.modeRadio.SetSelected(modeLocalLabel)

	return container.NewVBox(
		widget.NewCard("Mode", "Run LocalAI here, or join an existing distributed server. Only one runs at a time.", ui.modeRadio),
		pathsCard,
		sharedCard,
		ui.localBox,
		ui.workerBox,
		saveButton,
	)
}

const (
	modeLocalLabel = "Run locally"
	modeJoinLabel  = "Join a distributed server"
)

// showMode shows one profile's settings. The other profile's widgets keep
// their drafts.
func (ui *LauncherUI) showMode(selected string) {
	if ui.localBox == nil || ui.workerBox == nil {
		return
	}
	if selected == modeJoinLabel {
		ui.localBox.Hide()
		ui.workerBox.Show()
	} else {
		ui.workerBox.Hide()
		ui.localBox.Show()
	}
	ui.refreshModeControls()
}

// selectedRole is the role the controls act on: the saved mode, so a draft
// switch does not relabel a running role's controls.
func (ui *LauncherUI) selectedRole() string {
	if ui.launcher != nil && ui.launcher.config != nil && ui.launcher.config.Mode == roleWorker {
		return roleWorker
	}
	return roleStandalone
}

func (ui *LauncherUI) refreshModeControls() {
	ui.UpdateRunningState(ui.launcher != nil && ui.launcher.IsRunning())
}

// createWorkerSection builds the "Join a distributed server" settings.
func (ui *LauncherUI) createWorkerSection() *fyne.Container {
	ui.frontendEntry.SetPlaceHolder("https://controller.example")
	ui.advertisedEntry.SetPlaceHolder("worker.example:50051")
	ui.nodeNameEntry.SetPlaceHolder("optional")
	ui.grpcMaxEntry.SetPlaceHolder("65535")
	ui.natsURLEntry.SetPlaceHolder("tls://nats.example:4222")
	ui.workerEnvEntry.SetPlaceHolder("KEY=VALUE, one per line")
	ui.workerEnvEntry.SetMinRowsVisible(3)
	ui.joinExplanation.Wrapping = fyne.TextWrapWord
	ui.previewLabel.Wrapping = fyne.TextWrapWord
	ui.plaintextCheck.OnChanged = func(v bool) {
		if ui.launcher != nil {
			ui.launcher.SetPlaintextAcknowledged(v)
		}
	}
	refresh := func(string) { ui.refreshPreview() }
	ui.advertisedEntry.OnChanged = refresh
	ui.grpcMaxEntry.OnChanged = refresh

	connection := widget.NewCard("Connection", "", container.NewVBox(
		ui.joinExplanation,
		container.NewGridWithColumns(2,
			widget.NewLabel("Frontend URL:"), ui.frontendEntry,
			widget.NewLabel("Registration token:"), ui.tokenEntry,
			widget.NewLabel("Advertised address:"), ui.advertisedEntry,
			widget.NewLabel("Node name:"), ui.nodeNameEntry,
		),
		ui.clearTokenCheck,
		ui.previewLabel,
	))
	advanced := widget.NewAccordion(widget.NewAccordionItem("Advanced connection settings", container.NewVBox(
		container.NewGridWithColumns(2,
			widget.NewLabel("NATS URL:"), ui.natsURLEntry,
			widget.NewLabel("NATS CA file:"), ui.tlsCAEntry,
			widget.NewLabel("NATS client certificate:"), ui.tlsCertEntry,
			widget.NewLabel("NATS client key:"), ui.tlsKeyEntry,
			widget.NewLabel("Maximum gRPC port:"), ui.grpcMaxEntry,
			widget.NewLabel("NATS JWT override:"), ui.jwtEntry,
			widget.NewLabel("NATS user seed override:"), ui.seedEntry,
		),
		ui.clearJWTCheck,
		ui.clearSeedCheck,
		ui.plaintextCheck,
		widget.NewLabel("Runtime environment overrides:"),
		ui.workerEnvEntry,
		widget.NewLabel("Secrets are stored in plaintext in a protected file; they are not encrypted. Leave a secret blank to keep the saved value."),
	)))
	return container.NewVBox(connection, advanced)
}

func (ui *LauncherUI) refreshPreview() {
	text, err := reachabilityPreview(ui.advertisedEntry.Text, ui.grpcMaxEntry.Text)
	if err != nil {
		text = err.Error()
	}
	ui.previewLabel.SetText(text)
}

// collectConfig builds the candidate configuration from the widgets: both
// profiles, the selected mode, and masked secrets kept unless changed.
func (ui *LauncherUI) collectConfig() (*Config, error) {
	config := ui.launcher.GetConfig()
	config.ModelsPath = ui.modelsPathEntry.Text
	config.BackendsPath = ui.backendsPathEntry.Text
	config.Address = ui.addressEntry.Text
	config.LogLevel = ui.logLevelSelect.Selected
	autoStart := ui.autoStartCheck.Checked
	config.AutoStart = &autoStart
	config.StartOnBoot = ui.startOnBootCheck.Checked
	config.EnvironmentVars = make(map[string]string)
	for _, envVar := range ui.envVarsData {
		config.EnvironmentVars[envVar.Key] = envVar.Value
	}
	form := workerForm{
		FrontendURL: ui.frontendEntry.Text, AdvertisedAddress: ui.advertisedEntry.Text, NodeName: ui.nodeNameEntry.Text,
		GRPCMaxPort: ui.grpcMaxEntry.Text, NATSURL: ui.natsURLEntry.Text, TLSCA: ui.tlsCAEntry.Text,
		TLSCert: ui.tlsCertEntry.Text, TLSKey: ui.tlsKeyEntry.Text, EnvLines: ui.workerEnvEntry.Text,
		Token: secretEdit{Replacement: ui.tokenEntry.Text, Clear: ui.clearTokenCheck.Checked},
		JWT:   secretEdit{Replacement: ui.jwtEntry.Text, Clear: ui.clearJWTCheck.Checked},
		Seed:  secretEdit{Replacement: ui.seedEntry.Text, Clear: ui.clearSeedCheck.Checked},
	}
	next, err := form.Apply(config)
	if err != nil {
		return nil, err
	}
	if ui.modeRadio.Selected == modeJoinLabel {
		next.Mode = roleWorker
	} else {
		next.Mode = roleStandalone
	}
	return next, nil
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
		log.Printf("addEnvironmentVariable: key is empty, showing error")
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

	// The new variable is a draft until Save, like every other setting.
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
	}
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

	// The log level is a draft until Save; a change may need the running role
	// stopped, which only the save flow can ask for.
}

// startLocalAI starts the LocalAI service
func (ui *LauncherUI) startLocalAI() {
	fyne.Do(func() {
		ui.startStopButton.Disable()
	})
	ui.UpdateStatus("Starting...")

	go func() {
		err := ui.launcher.StartLocalAI()
		if err != nil {
			ui.UpdateStatus("Failed to start: " + err.Error())
			fyne.DoAndWait(func() {
				dialog.ShowError(err, ui.launcher.window)
			})
		} else {
			ui.UpdateRunningState(true)
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
	ui.UpdateStatus("Stopping...")

	go func() {
		err := ui.launcher.StopLocalAI()
		if err != nil {
			// Stop stays available until shutdown is confirmed.
			fyne.DoAndWait(func() {
				dialog.ShowError(err, ui.launcher.window)
			})
		} else {
			ui.UpdateRunningState(false)
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

// saveConfiguration validates and persists both profiles and the selected mode.
// A change that affects a running role asks for a confirmed stop first.
func (ui *LauncherUI) saveConfiguration() {
	candidate, err := ui.collectConfig()
	if err != nil {
		dialog.ShowError(err, ui.launcher.window)
		return
	}
	err = ui.launcher.ApplySettings(candidate, false)
	switch {
	case err == nil:
		dialog.ShowInformation("Configuration", "Configuration saved successfully", ui.launcher.window)
		ui.LoadConfiguration()
	case errors.Is(err, errStopRequired):
		message := widget.NewLabel("This change needs the running role to stop first. Stop and apply waits until the runtime and its backend processes are confirmed gone, saves the settings, and does not start anything. Active inference may be interrupted.")
		message.Wrapping = fyne.TextWrapWord
		dialog.NewCustomConfirm("Stop and apply?", "Stop and apply", "Cancel", message, func(stop bool) {
			if !stop {
				return // Cancel leaves the active configuration unchanged
			}
			ui.UpdateStatus("Stopping...")
			go func() {
				applyErr := ui.launcher.ApplySettings(candidate, true)
				fyne.Do(func() {
					if applyErr != nil {
						ui.UpdateStatus("Failed: " + applyErr.Error())
						dialog.ShowError(applyErr, ui.launcher.window)
						return
					}
					ui.UpdateStatus("Settings applied. Start the selected mode when you are ready.")
					ui.LoadConfiguration()
				})
			}()
		}, ui.launcher.window).Show()
	default:
		log.Printf("saveConfiguration: failed to save config")
		dialog.ShowError(err, ui.launcher.window)
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
		err := ui.launcher.DownloadUpdate(version, func(downloaded, total int64) {
			fyne.Do(func() {
				if total > 0 {
					ui.progressBar.SetValue(float64(downloaded) / float64(total))
				}
			})
			// The progress bar already shows the percentage, so report the
			// human-readable size here instead of repeating the percent.
			if total > 0 {
				ui.UpdateStatus(fmt.Sprintf("Downloading update %s… %s / %s", version, formatBytes(downloaded), formatBytes(total)))
			} else {
				ui.UpdateStatus(fmt.Sprintf("Downloading update %s… %s", version, formatBytes(downloaded)))
			}
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
			text := currentText + logLine
			if len(text) > maxLogBuffer {
				text = text[len(text)-maxLogBuffer:]
			}
			ui.logText.SetText(text)

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
		log.Printf("UI LoadConfiguration: launcher is nil")
		return
	}

	config := ui.launcher.GetConfig()

	ui.modelsPathEntry.SetText(config.ModelsPath)
	ui.backendsPathEntry.SetText(config.BackendsPath)
	ui.addressEntry.SetText(config.Address)
	ui.logLevelSelect.SetSelected(config.LogLevel)
	ui.autoStartCheck.SetChecked(autoStartChecked(config))
	ui.startOnBootCheck.SetChecked(config.StartOnBoot)

	// Worker profile. Secret values are never loaded into widgets.
	form := newWorkerForm(config)
	ui.frontendEntry.SetText(form.FrontendURL)
	ui.advertisedEntry.SetText(form.AdvertisedAddress)
	ui.nodeNameEntry.SetText(form.NodeName)
	ui.grpcMaxEntry.SetText(form.GRPCMaxPort)
	ui.natsURLEntry.SetText(form.NATSURL)
	ui.tlsCAEntry.SetText(form.TLSCA)
	ui.tlsCertEntry.SetText(form.TLSCert)
	ui.tlsKeyEntry.SetText(form.TLSKey)
	ui.workerEnvEntry.SetText(form.EnvLines)
	for _, m := range []struct {
		entry *widget.Entry
		clear *widget.Check
		saved bool
	}{{ui.tokenEntry, ui.clearTokenCheck, form.Token.Saved}, {ui.jwtEntry, ui.clearJWTCheck, form.JWT.Saved}, {ui.seedEntry, ui.clearSeedCheck, form.Seed.Saved}} {
		m.entry.SetText("")
		m.clear.SetChecked(false)
		if m.saved {
			m.entry.SetPlaceHolder("Saved. Leave blank to keep, or type a new value.")
			m.clear.Show()
		} else {
			m.entry.SetPlaceHolder("Not set")
			m.clear.Hide()
		}
	}
	ui.plaintextCheck.SetChecked(ui.launcher.PlaintextAcknowledged())
	ui.refreshPreview()
	if config.Mode == roleWorker {
		ui.modeRadio.SetSelected(modeJoinLabel)
	} else {
		ui.modeRadio.SetSelected(modeLocalLabel)
	}

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

	log.Printf("UI LoadConfiguration: configuration loaded successfully")
}

// autoStartChecked is the automatic-start checkbox state for a profile. Legacy
// standalone profiles keep their default-enabled behavior; a worker profile
// starts only when the owner chose it.
func autoStartChecked(c *Config) bool {
	if c.Mode == roleWorker {
		return c.AutoStart != nil && *c.AutoStart
	}
	return c.AutoStart == nil || *c.AutoStart
}

// UpdateRunningState updates UI based on the running state, named for the role.
// Stop stays available while a runtime or unresolved ownership of one exists.
func (ui *LauncherUI) UpdateRunningState(isRunning bool) {
	update := func() {
		worker := ui.selectedRole() == roleWorker
		switch {
		case worker && isRunning:
			ui.startStopButton.SetText("Stop worker")
		case worker:
			ui.startStopButton.SetText("Start worker")
		case isRunning:
			ui.startStopButton.SetText("Stop LocalAI")
		default:
			ui.startStopButton.SetText("Start LocalAI")
		}
		if worker {
			ui.webUIButton.SetText("Open deployment UI")
			if _, err := ui.launcher.DeploymentURL(); err == nil {
				ui.webUIButton.Enable()
			} else {
				ui.webUIButton.Disable()
			}
			return
		}
		ui.webUIButton.SetText("Open WebUI")
		if isRunning {
			ui.webUIButton.Enable()
		} else {
			ui.webUIButton.Disable()
		}
	}
	if ui.launcher != nil && ui.launcher.app == nil {
		update()
		return
	}
	fyne.Do(update)
}

// WelcomeDontShowAgainChecked reports the initial state of the welcome
// window's "don't show this welcome window again" checkbox for the given
// config: checked only when the user has already opted out of the welcome
// window.
func WelcomeDontShowAgainChecked(config *Config) bool {
	return config != nil && config.ShowWelcome != nil && !*config.ShowWelcome
}

// ShowWelcomeWindow displays the welcome window with helpful information
func (ui *LauncherUI) ShowWelcomeWindow() {
	if ui.launcher == nil || ui.launcher.window == nil {
		log.Printf("Cannot show welcome window: launcher or window is nil")
		return
	}

	fyne.DoAndWait(func() {
		// Create welcome window
		welcomeWindow := ui.launcher.app.NewWindow("Welcome to LocalAI Launcher")
		welcomeWindow.Resize(fyne.NewSize(600, 500))
		welcomeWindow.CenterOnScreen()
		welcomeWindow.SetCloseIntercept(func() {
			welcomeWindow.Close()
		})

		// Title
		titleLabel := widget.NewLabel("Welcome to LocalAI Launcher!")
		titleLabel.TextStyle = fyne.TextStyle{Bold: true}
		titleLabel.Alignment = fyne.TextAlignCenter

		// Welcome message
		welcomeText := `LocalAI Launcher makes it easy to run LocalAI on your system.

What you can do:
• Start and stop LocalAI server
• Configure models and backends paths
• Set environment variables
• Check for updates automatically
• Access LocalAI WebUI when running

Getting Started:
1. Configure your models and backends paths
2. Click "Start LocalAI" to begin
3. Use "Open WebUI" to access the interface
4. Check the system tray for quick access`

		welcomeLabel := widget.NewLabel(welcomeText)
		welcomeLabel.Wrapping = fyne.TextWrapWord

		// Useful links section
		linksTitle := widget.NewLabel("Useful Links:")
		linksTitle.TextStyle = fyne.TextStyle{Bold: true}

		// Create link buttons
		docsButton := widget.NewButton("📚 Documentation", func() {
			ui.openURL("https://localai.io/docs/")
		})

		githubButton := widget.NewButton("🐙 GitHub Repository", func() {
			ui.openURL("https://github.com/mudler/LocalAI")
		})

		modelsButton := widget.NewButton("🤖 Model Gallery", func() {
			ui.openURL("https://localai.io/models/")
		})

		communityButton := widget.NewButton("💬 Community", func() {
			ui.openURL("https://discord.gg/XgwjKptP7Z")
		})

		// Checkbox to disable welcome window. The initial state is applied
		// BEFORE the change callback is attached: SetChecked fires OnChanged,
		// and letting the initialization itself persist a ShowWelcome flip is
		// exactly the bug that suppressed this window forever after its first
		// showing (#11673).
		dontShowAgainCheck := widget.NewCheck("Don't show this welcome window again", nil)
		dontShowAgainCheck.SetChecked(WelcomeDontShowAgainChecked(ui.launcher.GetConfig()))
		dontShowAgainCheck.OnChanged = func(checked bool) {
			if ui.launcher != nil {
				config := ui.launcher.GetConfig()
				v := !checked
				config.ShowWelcome = &v
				ui.launcher.SetConfig(config)
			}
		}

		// Close button
		closeButton := widget.NewButton("Get Started", func() {
			welcomeWindow.Close()
		})
		closeButton.Importance = widget.HighImportance

		// Layout
		linksContainer := container.NewVBox(
			linksTitle,
			docsButton,
			githubButton,
			modelsButton,
			communityButton,
		)

		content := container.NewVBox(
			titleLabel,
			widget.NewSeparator(),
			welcomeLabel,
			widget.NewSeparator(),
			linksContainer,
			widget.NewSeparator(),
			dontShowAgainCheck,
			widget.NewSeparator(),
			closeButton,
		)

		welcomeWindow.SetContent(content)
		welcomeWindow.Show()
	})
}

// openURL opens a URL in the default browser
func (ui *LauncherUI) openURL(urlString string) {
	parsedURL, err := url.Parse(urlString)
	if err != nil {
		log.Printf("Failed to parse URL %s: %v", urlString, err)
		return
	}
	fyne.CurrentApp().OpenURL(parsedURL)
}
