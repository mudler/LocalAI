package main

import "embed"

//go:embed backend-assets/*
var backendAssets embed.FS
