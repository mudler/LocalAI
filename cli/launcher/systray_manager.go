package launcher

import (
	"fmt"
	"log"
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// SystrayManager manages the system tray functionality
type SystrayManager struct {
	launcher *Launcher
	window   fyne.Window
	app      fyne.App
	desk     desktop.App

	// Menu items that need dynamic updates
	startStopItem      *fyne.MenuItem
	updateStatusItem   *fyne.MenuItem
	hasUpdateAvailable bool
	latestVersion      string
}

// NewSystrayManager creates a new systray manager
func NewSystrayManager(launcher *Launcher, window fyne.Window, app fyne.App) *SystrayManager {
	return &SystrayManager{
		launcher: launcher,
		window:   window,
		app:      app,
	}
}

// setupMenu sets up the system tray menu
func (sm *SystrayManager) setupMenu(desk desktop.App) {
	sm.desk = desk

	// Create the start/stop toggle item
	sm.startStopItem = fyne.NewMenuItem("Start LocalAI", func() {
		sm.toggleLocalAI()
	})

	desk.SetSystemTrayIcon(resourceIconPng)

	// Initialize the menu state using recreateMenu
	sm.recreateMenu()
}

// toggleLocalAI starts or stops LocalAI based on current state
func (sm *SystrayManager) toggleLocalAI() {
	if sm.launcher.IsRunning() {
		go func() {
			if err := sm.launcher.StopLocalAI(); err != nil {
				log.Printf("Failed to stop LocalAI: %v", err)
				sm.showErrorDialog("Failed to Stop LocalAI", err.Error())
			}
		}()
	} else {
		go func() {
			if err := sm.launcher.StartLocalAI(); err != nil {
				log.Printf("Failed to start LocalAI: %v", err)
				sm.showStartupErrorDialog(err)
			}
		}()
	}
}

// openWebUI opens the LocalAI WebUI in the default browser
func (sm *SystrayManager) openWebUI() {
	if !sm.launcher.IsRunning() {
		return // LocalAI is not running
	}

	webURL := sm.launcher.GetWebUIURL()
	if parsedURL, err := url.Parse(webURL); err == nil {
		sm.app.OpenURL(parsedURL)
	}
}

// openDocumentation opens the LocalAI documentation
func (sm *SystrayManager) openDocumentation() {
	if parsedURL, err := url.Parse("https://localai.io"); err == nil {
		sm.app.OpenURL(parsedURL)
	}
}

// updateStartStopItem updates the start/stop menu item based on current state
func (sm *SystrayManager) updateStartStopItem() {
	// Since Fyne menu items can't change text dynamically, we recreate the menu
	sm.recreateMenu()
}

// recreateMenu recreates the entire menu with updated state
func (sm *SystrayManager) recreateMenu() {
	if sm.desk == nil {
		return
	}

	// Determine the start/stop text based on current state
	startStopText := "Start LocalAI"
	if sm.launcher.IsRunning() {
		startStopText = "Stop LocalAI"
	}

	menuItems := []*fyne.MenuItem{}

	// Add update notification if available
	if sm.hasUpdateAvailable {
		updateItem := fyne.NewMenuItem("🔔 New version available ("+sm.latestVersion+")", func() {
			sm.downloadUpdate()
		})
		menuItems = append(menuItems, updateItem)
		menuItems = append(menuItems, fyne.NewMenuItemSeparator())
	}

	// Core actions
	menuItems = append(menuItems,
		fyne.NewMenuItem(startStopText, func() {
			sm.toggleLocalAI()
		}),
		fyne.NewMenuItem("Open WebUI", func() {
			sm.openWebUI()
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Check for Updates", func() {
			sm.checkForUpdates()
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Settings", func() {
			sm.showSettings()
		}),
		fyne.NewMenuItem("Open Data Folder", func() {
			sm.openDataFolder()
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Documentation", func() {
			sm.openDocumentation()
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() {
			sm.app.Quit()
		}),
	)

	menu := fyne.NewMenu("LocalAI", menuItems...)
	sm.desk.SetSystemTrayMenu(menu)
}

// UpdateRunningState updates the systray based on running state
func (sm *SystrayManager) UpdateRunningState(isRunning bool) {
	sm.updateStartStopItem()
}

// checkForUpdates checks for available updates
func (sm *SystrayManager) checkForUpdates() {
	go func() {
		log.Printf("Checking for updates...")
		available, version, err := sm.launcher.CheckForUpdates()
		if err != nil {
			log.Printf("Failed to check for updates: %v", err)
			return
		}

		log.Printf("Update check result: available=%v, version=%s", available, version)
		if available {
			sm.hasUpdateAvailable = true
			sm.latestVersion = version
			sm.recreateMenu()
		}
	}()
}

// downloadUpdate downloads the latest update
func (sm *SystrayManager) downloadUpdate() {
	if !sm.hasUpdateAvailable {
		return
	}

	// Show progress window
	sm.showDownloadProgress(sm.latestVersion)
}

// showSettings shows the settings window
func (sm *SystrayManager) showSettings() {
	sm.window.Show()
	sm.window.RequestFocus()
}

// openDataFolder opens the data folder in file manager
func (sm *SystrayManager) openDataFolder() {
	dataPath := sm.launcher.GetDataPath()
	if parsedURL, err := url.Parse("file://" + dataPath); err == nil {
		sm.app.OpenURL(parsedURL)
	}
}

// NotifyUpdateAvailable sets update notification in systray
func (sm *SystrayManager) NotifyUpdateAvailable(version string) {
	sm.hasUpdateAvailable = true
	sm.latestVersion = version
	sm.recreateMenu()
}

// showErrorDialog shows a simple error dialog
func (sm *SystrayManager) showErrorDialog(title, message string) {
	fyne.DoAndWait(func() {
		dialog.ShowError(fmt.Errorf(message), sm.window)
	})
}

// showStartupErrorDialog shows a detailed error dialog with process logs
func (sm *SystrayManager) showStartupErrorDialog(err error) {
	fyne.DoAndWait(func() {
		// Get the recent process logs (more useful for debugging)
		logs := sm.launcher.GetRecentLogs()

		// Create error window
		errorWindow := sm.app.NewWindow("LocalAI Startup Failed")
		errorWindow.Resize(fyne.NewSize(600, 500))
		errorWindow.CenterOnScreen()

		// Error message
		errorLabel := widget.NewLabel(fmt.Sprintf("Failed to start LocalAI:\n%s", err.Error()))
		errorLabel.Wrapping = fyne.TextWrapWord

		// Logs display
		logsLabel := widget.NewLabel("Process Logs:")
		logsText := widget.NewMultiLineEntry()
		logsText.SetText(logs)
		logsText.Wrapping = fyne.TextWrapWord
		logsText.Disable() // Make it read-only

		// Buttons
		closeButton := widget.NewButton("Close", func() {
			errorWindow.Close()
		})

		retryButton := widget.NewButton("Retry", func() {
			errorWindow.Close()
			// Try to start again
			go func() {
				if retryErr := sm.launcher.StartLocalAI(); retryErr != nil {
					sm.showStartupErrorDialog(retryErr)
				}
			}()
		})

		openLogsButton := widget.NewButton("Open Logs Folder", func() {
			sm.openDataFolder()
		})

		// Layout
		buttons := container.NewHBox(closeButton, retryButton, openLogsButton)
		content := container.NewVBox(
			errorLabel,
			widget.NewSeparator(),
			logsLabel,
			logsText,
			widget.NewSeparator(),
			buttons,
		)

		errorWindow.SetContent(content)
		errorWindow.Show()
	})
}

// showDownloadProgress shows a progress window for downloading updates
func (sm *SystrayManager) showDownloadProgress(version string) {
	// Create a new window for download progress
	progressWindow := sm.app.NewWindow("Downloading LocalAI Update")
	progressWindow.Resize(fyne.NewSize(400, 200))
	progressWindow.CenterOnScreen()

	// Progress bar
	progressBar := widget.NewProgressBar()
	progressBar.SetValue(0)

	// Status label
	statusLabel := widget.NewLabel("Preparing download...")

	// Progress container
	progressContainer := container.NewVBox(
		widget.NewLabel(fmt.Sprintf("Downloading LocalAI version %s", version)),
		progressBar,
		statusLabel,
	)

	progressWindow.SetContent(progressContainer)
	progressWindow.Show()

	// Start download in background
	go func() {
		err := sm.launcher.DownloadUpdate(version, func(progress float64) {
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

				// Show restart dialog
				dialog.ShowConfirm("Update Downloaded",
					"LocalAI has been updated successfully. Please restart the launcher to use the new version.",
					func(restart bool) {
						if restart {
							sm.app.Quit()
						}
						progressWindow.Close()
					}, progressWindow)
			}
		})

		// Update systray menu
		if err == nil {
			sm.hasUpdateAvailable = false
			sm.latestVersion = ""
			sm.recreateMenu()
		}
	}()
}
