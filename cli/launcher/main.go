package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

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
	launcher := coreLauncher.NewLauncher(ui, myWindow, myApp)

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

	// Setup signal handling for graceful shutdown
	setupSignalHandling(launcher)

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

// setupSignalHandling sets up signal handlers for graceful shutdown
func setupSignalHandling(launcher *coreLauncher.Launcher) {
	// Create a channel to receive OS signals
	sigChan := make(chan os.Signal, 1)

	// Register for interrupt and terminate signals
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Handle signals in a separate goroutine
	go func() {
		sig := <-sigChan
		log.Printf("Received signal %v, shutting down gracefully...", sig)

		// Perform cleanup
		if err := launcher.Shutdown(); err != nil {
			log.Printf("Error during shutdown: %v", err)
		}

		// Exit the application
		os.Exit(0)
	}()
}
