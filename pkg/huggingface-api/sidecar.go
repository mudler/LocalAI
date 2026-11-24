package hfapi

import "strings"

// Sidecar describes one kind of GGUF file that llama.cpp ships next to a main
// model instead of as a model of its own: a speculative-decoding drafter.
type Sidecar struct {
	// Kind is llama.cpp's name for the sidecar ("dspark", "dflash", ...).
	Kind string
	// Keyword is the filename fragment that marks the kind.
	Keyword string
	// SpecType is the llama.cpp `--spec-type` value that runs the drafter.
	SpecType string
}

// Sidecars lists the drafter kinds in the order an import prefers them when a
// repository ships more than one: the DFlash family first (the largest
// speed-up), then EAGLE3, then a standalone MTP head.
//
// The keywords mirror the `sidecars` table of llama.cpp's common/download.cpp
// (the mmproj entry aside, which the importer handles on its own):
//
//	{ "mtp",    "mtp-",    ... }
//	{ "dflash", "dflash-", ... }
//	{ "eagle3", "eagle3-", ... }
//	{ "dspark", "dspark-", ... }
//
// llama.cpp matches the keyword as a substring of the file name, without the
// directory, and case-sensitively.
var Sidecars = []Sidecar{
	{Kind: "dspark", Keyword: "dspark-", SpecType: "draft-dspark"},
	{Kind: "dflash", Keyword: "dflash-", SpecType: "draft-dflash"},
	{Kind: "eagle3", Keyword: "eagle3-", SpecType: "draft-eagle3"},
	{Kind: "mtp", Keyword: "mtp-", SpecType: "draft-mtp"},
}

// SidecarOf returns the sidecar kind a GGUF path belongs to, or false when the
// file name carries no sidecar keyword.
func SidecarOf(filePath string) (Sidecar, bool) {
	name := filePath
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	for _, s := range Sidecars {
		if strings.Contains(name, s.Keyword) {
			return s, true
		}
	}
	return Sidecar{}, false
}

// IsImatrix reports whether a GGUF path is an importance-matrix file, which
// llama.cpp never treats as a model (gguf_filename_is_model in
// common/download.cpp).
func IsImatrix(filePath string) bool {
	name := filePath
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return strings.Contains(name, "imatrix")
}
