package main

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed logo.png
var logoData []byte

// resourceIconPng is the LocalAI logo icon
var resourceIconPng = &fyne.StaticResource{
	StaticName:    "logo.png",
	StaticContent: logoData,
}
