package launcher

import (
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

// SystrayManager manages the system tray functionality
type SystrayManager struct {
	launcher *Launcher
	window   fyne.Window
	app      fyne.App
	desk     desktop.App

	// Menu items that need dynamic updates
	startStopItem *fyne.MenuItem
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

	menu := fyne.NewMenu("LocalAI",
		fyne.NewMenuItem("Show Launcher", func() {
			sm.window.Show()
			sm.window.RequestFocus()
		}),
		fyne.NewMenuItemSeparator(),
		sm.startStopItem,
		fyne.NewMenuItem("Open WebUI", func() {
			sm.openWebUI()
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Configuration", func() {
			sm.showConfiguration()
		}),
		fyne.NewMenuItem("View Logs", func() {
			sm.showLogs()
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

	desk.SetSystemTrayMenu(menu)
	desk.SetSystemTrayIcon(resourceIconPng)

	// Initialize the menu state
	sm.updateStartStopItem()
}

// toggleLocalAI starts or stops LocalAI based on current state
func (sm *SystrayManager) toggleLocalAI() {
	if sm.launcher.IsRunning() {
		go func() {
			if err := sm.launcher.StopLocalAI(); err != nil {
				// Handle error - could show notification
			}
		}()
	} else {
		go func() {
			if err := sm.launcher.StartLocalAI(); err != nil {
				// Handle error - could show notification
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

// showConfiguration shows the launcher and switches to configuration tab
func (sm *SystrayManager) showConfiguration() {
	sm.window.Show()
	sm.window.RequestFocus()
	// TODO: Switch to configuration tab programmatically
}

// showLogs shows the launcher and switches to logs tab
func (sm *SystrayManager) showLogs() {
	sm.window.Show()
	sm.window.RequestFocus()
	// TODO: Switch to logs tab programmatically
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

	menu := fyne.NewMenu("LocalAI",
		fyne.NewMenuItem("Show Launcher", func() {
			sm.window.Show()
			sm.window.RequestFocus()
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(startStopText, func() {
			sm.toggleLocalAI()
		}),
		fyne.NewMenuItem("Open WebUI", func() {
			sm.openWebUI()
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Configuration", func() {
			sm.showConfiguration()
		}),
		fyne.NewMenuItem("View Logs", func() {
			sm.showLogs()
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

	sm.desk.SetSystemTrayMenu(menu)
}

// UpdateRunningState updates the systray based on running state
func (sm *SystrayManager) UpdateRunningState(isRunning bool) {
	sm.updateStartStopItem()
}
