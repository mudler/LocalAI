package cli

import (
	"github.com/alecthomas/kong"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
)

// KongVars are the variables the flag defaults of every command use. A test
// that builds its own parser must use them too: kong fails on a default that
// names a variable it does not know.
func KongVars(d system.DefaultDirs) kong.Vars {
	return kong.Vars{
		"basepath":           d.Base,
		"backendssystempath": d.SystemBackends,
		// Per-user temp locations for ephemeral writable content. A fixed
		// shared name under /tmp collides across accounts on multi-user hosts
		// (notably macOS, where /tmp is the shared /private/tmp for everyone),
		// failing startup with "mkdir /tmp/generated/content: permission
		// denied". See DefaultGeneratedContentPath.
		"generatedcontentpath": DefaultGeneratedContentPath(),
		"uploadpath":           DefaultUploadPath(),
		"galleries":            config.DefaultGalleriesJSON,
		"backends":             config.DefaultBackendGalleriesJSON,
		"version":              internal.PrintableVersion(),
	}
}

// LogDefaultDirs says where the default folders are when that is not the
// current folder of earlier releases, so a user can find their models.
func LogDefaultDirs(d system.DefaultDirs) {
	switch d.Origin {
	case system.OriginStandard:
		xlog.Debug("Default data folder", "folder", d.Base)
	case system.OriginEnv:
		xlog.Debug("Default data folder from "+system.BasePathEnv, "folder", d.Base)
	case system.OriginLegacy:
		xlog.Warn("Using the LocalAI folders in the current folder, as earlier releases did. To stop this message, move them to the standard folder, or set "+system.BasePathEnv+" or the path flags",
			"folder", d.Base, "found", d.Reason, "standard", d.Standard)
	case system.OriginFallback:
		xlog.Warn("The standard data folder is unknown, so the default folders are in the current folder",
			"folder", d.Base, "reason", d.Reason)
	}
}

// namedPath is a folder with the flag that sets it, for warnings.
type namedPath struct{ flag, path string }

// longPaths returns the folders that are long enough on goos that a backend
// may fail to open the files in them.
func longPaths(goos string, paths []namedPath) []namedPath {
	var long []namedPath
	for _, p := range paths {
		if p.path != "" && system.LongPath(goos, p.path) {
			long = append(long, p)
		}
	}
	return long
}

func warnLongPaths(goos string, paths []namedPath) {
	for _, p := range longPaths(goos, paths) {
		xlog.Warn("This folder path is long. A backend may fail to open files in it, because Windows limits most programs to 260 characters. Use a shorter folder, or enable long paths in Windows",
			"flag", p.flag, "path", p.path, "length", len(p.path))
	}
}
