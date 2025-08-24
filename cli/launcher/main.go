package main

import (
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"
	coreLauncher "github.com/mudler/LocalAI/core/launcher"
)

func main() {
	// Create the application with unique ID
	myApp := app.NewWithID("com.localai.launcher")
	myApp.SetIcon(resourceIconPng)
	myWindow := myApp.NewWindow("LocalAI Launcher")
	myWindow.Resize(fyne.NewSize(800, 600))

	// Create the launcher UI
	ui := coreLauncher.NewLauncherUI()

	// Initialize the launcher with UI context
	launcher := coreLauncher.NewLauncher()
	launcher.SetUI(ui)
	launcher.SetWindow(myWindow)
	launcher.SetApp(myApp)

	// Setup the UI
	content := ui.CreateMainUI(launcher)
	myWindow.SetContent(content)

	// Setup window close behavior - minimize to tray instead of closing
	myWindow.SetCloseIntercept(func() {
		myWindow.Hide()
	})

	// Setup system tray using Fyne's built-in approach``
	if desk, ok := myApp.(desktop.App); ok {
		// Create a dynamic systray manager
		systray := coreLauncher.NewSystrayManager(launcher, myWindow, desk, myApp, resourceIconPng)
		launcher.SetSystray(systray)
	}

	// Initialize the launcher state
	go func() {
		if err := launcher.Initialize(); err != nil {
			log.Printf("Failed to initialize launcher: %v", err)
			if launcher.GetUI() != nil {
				launcher.GetUI().UpdateStatus("Failed to initialize: " + err.Error())
			}
		} else {
			// Load configuration into UI
			launcher.GetUI().LoadConfiguration()
			launcher.GetUI().UpdateStatus("Ready")
		}
	}()

	// Run the application in background (window only shown when "Settings" is clicked)
	myApp.Run()
}
