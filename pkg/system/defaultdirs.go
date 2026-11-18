package system

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// BasePathEnv moves every default folder that LocalAI derives from its base
// folder (models, backends, data, configuration and downloading) at once. It is
// an environment variable and not a flag because the defaults of all flags are
// computed before the command line is parsed, and because every subcommand
// (run, worker, backends, models, ...) must see the same value. It can also be
// set in one of the env files, which are read before it.
const BasePathEnv = "LOCALAI_BASE_PATH"

// UnixBackendsSystemPath is the default folder for backends that an
// administrator or a distribution package installs, on Linux and macOS.
const UnixBackendsSystemPath = "/var/lib/local-ai/backends"

// LongPathWarnLength is the length above which a Windows path gets a warning.
// LocalAI itself handles long paths, but the backends it starts and the tools
// they load are not always long-path aware, and Windows limits those to 260
// characters. The margin leaves room for the file names below the folder.
const LongPathWarnLength = 200

// Origin says where DefaultDirs.Base comes from.
type Origin string

const (
	// OriginWorkingDir is the current folder: the default on Linux and macOS.
	OriginWorkingDir Origin = "working-dir"
	// OriginStandard is the standard per-user folder of the OS (Windows).
	OriginStandard Origin = "standard"
	// OriginEnv is LOCALAI_BASE_PATH.
	OriginEnv Origin = "env"
	// OriginLegacy is a current folder that already holds LocalAI files on
	// Windows, from a release that used the current folder.
	OriginLegacy Origin = "legacy"
	// OriginFallback is the current folder because the standard folder is
	// unknown, for example for an account without a profile.
	OriginFallback Origin = "fallback"
)

// DefaultDirs is where LocalAI keeps its files when no flag or variable says
// otherwise. A flag or a variable of one folder always wins over these.
type DefaultDirs struct {
	// Base is the parent of models, backends, data, configuration and downloading.
	Base   string
	Origin Origin
	// Reason explains OriginLegacy (the LocalAI file found, relative to Base)
	// and OriginFallback (the error), for the log.
	Reason string
	// Standard is the standard folder that OriginLegacy did not use.
	Standard string
	// SystemBackends holds backends that an administrator installs. It is not
	// moved by LOCALAI_BASE_PATH.
	SystemBackends string
	// EnvFiles in load order. A variable keeps the first value it gets.
	EnvFiles []string
}

type knownFolder int

const (
	folderLocalAppData knownFolder = iota
	folderProgramData
)

// dirsHost is everything resolveDefaultDirs reads from the system, so that the
// Windows rules run as unit tests on any OS.
type dirsHost struct {
	goos   string
	cwd    string
	home   string // empty when unknown
	getenv func(string) string
	folder func(knownFolder) (string, error)
}

// ResolveDefaultDirs returns the default folders for this host.
func ResolveDefaultDirs() DefaultDirs {
	// The same value kong.ExpandPath(".") gives, which was the base before.
	cwd, err := filepath.Abs(".")
	if err != nil {
		cwd = "."
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return resolveDefaultDirs(dirsHost{
		goos:   runtime.GOOS,
		cwd:    cwd,
		home:   home,
		getenv: os.Getenv,
		folder: knownFolderPath,
	})
}

func resolveDefaultDirs(h dirsHost) DefaultDirs {
	windows := h.goos == "windows"
	d := DefaultDirs{SystemBackends: UnixBackendsSystemPath}

	var standard, machine string
	if windows {
		if local, err := h.folder(folderLocalAppData); err == nil && local != "" {
			standard = filepath.Join(local, "LocalAI")
		} else {
			d.Reason = fmt.Sprintf("the local application data folder is unknown: %v", err)
		}
		if pd, err := h.folder(folderProgramData); err == nil && pd != "" {
			machine = filepath.Join(pd, "LocalAI")
			d.SystemBackends = filepath.Join(machine, "backends")
		}
	}
	d.EnvFiles = envFiles(h, standard, machine)

	switch {
	case h.getenv(BasePathEnv) != "":
		d.Base, d.Origin, d.Reason = expandBase(h.getenv(BasePathEnv), h), OriginEnv, ""
	case !windows:
		d.Base, d.Origin = h.cwd, OriginWorkingDir
	case standard == "":
		d.Base, d.Origin = h.cwd, OriginFallback
	default:
		d.Base, d.Origin = standard, OriginStandard
		if !samePath(h.cwd, standard) {
			if marker := legacyMarker(h.cwd); marker != "" {
				d.Base, d.Origin, d.Reason, d.Standard = h.cwd, OriginLegacy, marker, standard
			}
		}
	}
	return d
}

// envFiles keeps the list of earlier releases on Linux and macOS. Windows has
// no /etc: the machine file is under ProgramData, and a per-user file is in
// the standard folder.
func envFiles(h dirsHost, standard, machine string) []string {
	files := []string{".env", "localai.env"}
	if h.home != "" {
		files = append(files, filepath.Join(h.home, "localai.env"), filepath.Join(h.home, ".config/localai.env"))
	}
	if h.goos != "windows" {
		return append(files, "/etc/localai.env")
	}
	if standard != "" {
		files = append(files, filepath.Join(standard, "localai.env"))
	}
	if machine != "" {
		files = append(files, filepath.Join(machine, "localai.env"))
	}
	return files
}

func expandBase(p string, h dirsHost) string {
	if h.home != "" && (p == "~" || strings.HasPrefix(p, "~/") || (h.goos == "windows" && strings.HasPrefix(p, `~\`))) {
		p = filepath.Join(h.home, p[1:])
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(h.cwd, p)
	}
	return filepath.Clean(p)
}

// samePath compares two Windows paths, which have no case.
func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// legacyMarker returns the first file in dir that only LocalAI writes, or "".
// A bare "models" or "data" folder is not enough: many projects have one.
func legacyMarker(dir string) string {
	if entries, err := os.ReadDir(filepath.Join(dir, "backends")); err == nil {
		for _, e := range entries {
			rel := filepath.Join("backends", e.Name(), "metadata.json")
			if e.IsDir() && isFile(filepath.Join(dir, rel)) {
				return rel
			}
		}
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "models")); err == nil {
		for _, e := range entries {
			switch strings.ToLower(filepath.Ext(e.Name())) {
			case ".yaml", ".yml", ".gguf":
				if !e.IsDir() {
					return filepath.Join("models", e.Name())
				}
			}
		}
	}
	for _, rel := range []string{
		filepath.Join("data", "database.db"),
		filepath.Join("configuration", "runtime_settings.json"),
		filepath.Join("configuration", "api_keys.json"),
	} {
		if isFile(filepath.Join(dir, rel)) {
			return rel
		}
	}
	return ""
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// LongPath reports whether p is long enough on goos that a backend or a tool
// may fail to open it.
func LongPath(goos, p string) bool {
	return goos == "windows" && len(p) > LongPathWarnLength
}

func isWindows() bool { return runtime.GOOS == "windows" }

// StandardDataDir returns %LOCALAPPDATA%\LocalAI on Windows. It is an error on
// other systems, which have no such folder for LocalAI. Unlike
// DefaultDirs.Base it ignores LOCALAI_BASE_PATH and the current folder, for
// files that belong to the user and not to a server.
func StandardDataDir() (string, error) {
	return standardDataDir(runtime.GOOS, knownFolderPath)
}

func standardDataDir(goos string, folder func(knownFolder) (string, error)) (string, error) {
	if goos != "windows" {
		return "", fmt.Errorf("no standard LocalAI data folder on %s", goos)
	}
	local, err := folder(folderLocalAppData)
	if err != nil {
		return "", err
	}
	if local == "" {
		return "", fmt.Errorf("the local application data folder is empty")
	}
	return filepath.Join(local, "LocalAI"), nil
}
