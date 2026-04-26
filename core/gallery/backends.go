// Package gallery provides installation and registration utilities for LocalAI backends,
// including meta-backend resolution based on system capabilities.
package gallery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/oci"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
	cp "github.com/otiai10/copy"
)

// ErrBackendNotFound is returned when a backend is not found in the system.
var ErrBackendNotFound = errors.New("backend not found")

const (
	metadataFile = "metadata.json"
	runFile      = "run.sh"
)

// Default fallback tag values
const (
	defaultLatestTag = "latest"
	defaultMasterTag = "master"
	defaultDevSuffix = "development"
)

// getFallbackTagValues returns the configurable fallback tag values from SystemState
func getFallbackTagValues(systemState *system.SystemState) (latestTag, masterTag, devSuffix string) {
	// Use SystemState fields if set, otherwise use defaults
	if systemState.BackendImagesReleaseTag != "" {
		latestTag = systemState.BackendImagesReleaseTag
	} else {
		latestTag = defaultLatestTag
	}
	if systemState.BackendImagesBranchTag != "" {
		masterTag = systemState.BackendImagesBranchTag
	} else {
		masterTag = defaultMasterTag
	}
	if systemState.BackendDevSuffix != "" {
		devSuffix = systemState.BackendDevSuffix
	} else {
		devSuffix = defaultDevSuffix
	}

	return latestTag, masterTag, devSuffix
}

// backendCandidate represents an installed concrete backend option for a given alias
type backendCandidate struct {
	name    string
	runFile string
}

// readBackendMetadata reads the metadata JSON file for a backend
func readBackendMetadata(backendPath string) (*BackendMetadata, error) {
	metadataPath := filepath.Join(backendPath, metadataFile)

	// If metadata file doesn't exist, return nil (for backward compatibility)
	if _, err := os.Stat(metadataPath); os.IsNotExist(err) {
		return nil, nil
	}

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata file %q: %v", metadataPath, err)
	}

	var metadata BackendMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata file %q: %v", metadataPath, err)
	}

	return &metadata, nil
}

// writeBackendMetadata writes the metadata JSON file for a backend
func writeBackendMetadata(backendPath string, metadata *BackendMetadata) error {
	metadataPath := filepath.Join(backendPath, metadataFile)

	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %v", err)
	}

	if err := os.WriteFile(metadataPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write metadata file %q: %v", metadataPath, err)
	}

	return nil
}

// InstallBackendFromGallery installs a backend from the gallery.
func InstallBackendFromGallery(ctx context.Context, galleries []config.Gallery, systemState *system.SystemState, modelLoader *model.ModelLoader, name string, downloadStatus func(string, string, string, float64), force bool) error {
	if !force {
		// check if we already have the backend installed
		backends, err := ListSystemBackends(systemState)
		if err != nil {
			return err
		}
		// Only short-circuit if the install is *actually usable*. An orphaned
		// meta entry whose concrete was removed still shows up in
		// ListSystemBackends with a RunFile pointing at a path that no longer
		// exists; returning early there leaves the caller with a broken
		// alias and the worker fails with "backend not found after install
		// attempt" on every retry. Re-install in that case.
		if existing, ok := backends.Get(name); ok && isBackendRunnable(existing) {
			return nil
		}
	}

	if name == "" {
		return fmt.Errorf("backend name is empty")
	}

	xlog.Debug("Installing backend from gallery", "galleries", galleries, "name", name)

	backends, err := AvailableBackends(galleries, systemState)
	if err != nil {
		return err
	}

	backend := FindGalleryElement(backends, name)
	if backend == nil {
		return fmt.Errorf("no backend found with name %q", name)
	}

	if backend.IsMeta() {
		xlog.Debug("Backend is a meta backend", "systemState", systemState, "name", name)

		// Then, let's try to find the best backend based on the capabilities map
		bestBackend := backend.FindBestBackendFromMeta(systemState, backends)
		if bestBackend == nil {
			return fmt.Errorf("no backend found with capabilities %q", backend.CapabilitiesMap)
		}

		xlog.Debug("Installing backend from meta backend", "name", name, "bestBackend", bestBackend.Name)

		// Then, let's install the best backend
		if err := InstallBackend(ctx, systemState, modelLoader, bestBackend, downloadStatus); err != nil {
			return err
		}

		// we need now to create a path for the meta backend, with the alias to the installed ones so it can be used to remove it
		metaBackendPath := filepath.Join(systemState.Backend.BackendsPath, name)
		if err := os.MkdirAll(metaBackendPath, 0750); err != nil {
			return fmt.Errorf("failed to create meta backend path %q: %v", metaBackendPath, err)
		}

		// Create metadata for the meta backend
		metaMetadata := &BackendMetadata{
			MetaBackendFor: bestBackend.Name,
			Name:           name,
			GalleryURL:     backend.Gallery.URL,
			InstalledAt:    time.Now().Format(time.RFC3339),
			Version:        bestBackend.Version,
		}

		if err := writeBackendMetadata(metaBackendPath, metaMetadata); err != nil {
			return fmt.Errorf("failed to write metadata for meta backend %q: %v", name, err)
		}

		return nil
	}

	return InstallBackend(ctx, systemState, modelLoader, backend, downloadStatus)
}

func InstallBackend(ctx context.Context, systemState *system.SystemState, modelLoader *model.ModelLoader, config *GalleryBackend, downloadStatus func(string, string, string, float64)) error {
	// Get configurable fallback tag values from SystemState
	latestTag, masterTag, devSuffix := getFallbackTagValues(systemState)

	// Create base path if it doesn't exist
	err := os.MkdirAll(systemState.Backend.BackendsPath, 0750)
	if err != nil {
		return fmt.Errorf("failed to create base path: %v", err)
	}

	if config.IsMeta() {
		return fmt.Errorf("meta backends cannot be installed directly")
	}

	name := config.Name
	backendPath := filepath.Join(systemState.Backend.BackendsPath, name)
	// Clean up legacy flat-layout artefacts: earlier dev builds of the
	// golang backends dropped the compiled binary directly at
	// `<backendsPath>/<name>` (a plain file) instead of
	// `<backendsPath>/<name>/<name>` (the nested layout the current code
	// expects). MkdirAll below returns ENOTDIR when such a stale file
	// exists, permanently blocking any reinstall or upgrade. Remove the
	// file first so the install can proceed; the new install will write
	// the correct nested layout, including metadata.json + run.sh.
	if fi, statErr := os.Lstat(backendPath); statErr == nil && !fi.IsDir() {
		xlog.Warn("removing stale non-directory backend artefact to make room for fresh install", "path", backendPath)
		if rmErr := os.Remove(backendPath); rmErr != nil {
			return fmt.Errorf("failed to remove stale backend artefact at %s: %w", backendPath, rmErr)
		}
	}
	err = os.MkdirAll(backendPath, 0750)
	if err != nil {
		return fmt.Errorf("failed to create base path: %v", err)
	}

	uri := downloader.URI(config.URI)
	// Check if it is a directory
	if uri.LooksLikeDir() {
		// It is a directory, we just copy it over in the backend folder
		if err := cp.Copy(config.URI, backendPath); err != nil {
			return fmt.Errorf("failed copying: %w", err)
		}
	} else {
		xlog.Debug("Downloading backend", "uri", config.URI, "backendPath", backendPath)
		if err := uri.DownloadFileWithContext(ctx, backendPath, "", 1, 1, downloadStatus); err != nil {
			xlog.Debug("Backend download failed, trying fallback", "backendPath", backendPath, "error", err)

			// resetBackendPath cleans up partial state from a failed OCI extraction
			// so the next download attempt starts fresh. The directory is re-created
			// because OCI image extractors need it to exist for writing files into.
			resetBackendPath := func() {
				os.RemoveAll(backendPath)
				os.MkdirAll(backendPath, 0750)
			}

			success := false
			// Try to download from mirrors
			for _, mirror := range config.Mirrors {
				// Check for cancellation before trying next mirror
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				resetBackendPath()
				if err := downloader.URI(mirror).DownloadFileWithContext(ctx, backendPath, "", 1, 1, downloadStatus); err == nil {
					success = true
					xlog.Debug("Downloaded backend from mirror", "uri", config.URI, "backendPath", backendPath)
					break
				}
			}

			if !success {
				// Try fallback: replace latestTag + "-" with masterTag + "-" in the URI
				fallbackURI := strings.Replace(string(config.URI), latestTag+"-", masterTag+"-", 1)
				if fallbackURI != string(config.URI) {
					resetBackendPath()
					xlog.Info("Trying fallback URI", "original", config.URI, "fallback", fallbackURI)
					if err := downloader.URI(fallbackURI).DownloadFileWithContext(ctx, backendPath, "", 1, 1, downloadStatus); err == nil {
						xlog.Info("Downloaded backend using fallback URI", "uri", fallbackURI, "backendPath", backendPath)
						success = true
					} else {
						xlog.Info("Fallback URI failed", "fallback", fallbackURI, "error", err)
						if !strings.Contains(fallbackURI, "-"+devSuffix) {
							resetBackendPath()
							devFallbackURI := fallbackURI + "-" + devSuffix
							xlog.Info("Trying development fallback URI", "fallback", devFallbackURI)
							if err := downloader.URI(devFallbackURI).DownloadFileWithContext(ctx, backendPath, "", 1, 1, downloadStatus); err == nil {
								xlog.Info("Downloaded backend using development fallback URI", "uri", devFallbackURI, "backendPath", backendPath)
								success = true
							} else {
								xlog.Info("Development fallback URI failed", "fallback", devFallbackURI, "error", err)
							}
						}
					}
				}
			}

			if !success {
				// Clean up backend directory only when all download attempts have failed
				if cleanupErr := os.RemoveAll(backendPath); cleanupErr != nil {
					xlog.Warn("Failed to clean up backend directory", "backendPath", backendPath, "error", cleanupErr)
				}
				xlog.Error("Failed to download backend", "uri", config.URI, "backendPath", backendPath, "error", err)
				return fmt.Errorf("failed to download backend %q: %v", config.URI, err)
			}
		} else {
			xlog.Debug("Downloaded backend", "uri", config.URI, "backendPath", backendPath)
		}
	}

	// sanity check - check if runfile is present
	runFile := filepath.Join(backendPath, runFile)
	if _, err := os.Stat(runFile); os.IsNotExist(err) {
		xlog.Error("Run file not found", "runFile", runFile)
		return fmt.Errorf("not a valid backend: run file not found %q", runFile)
	}

	// Create metadata for the backend
	metadata := &BackendMetadata{
		Name:        name,
		GalleryURL:  config.Gallery.URL,
		InstalledAt: time.Now().Format(time.RFC3339),
		Version:     config.Version,
		URI:         string(uri),
	}

	// Record the OCI digest for upgrade detection (non-fatal on failure)
	if uri.LooksLikeOCI() {
		digest, digestErr := oci.GetImageDigest(string(uri), "", nil, nil)
		if digestErr != nil {
			xlog.Warn("Failed to get OCI image digest for backend", "uri", string(uri), "error", digestErr)
		} else {
			metadata.Digest = digest
		}
	}

	if config.Alias != "" {
		metadata.Alias = config.Alias
	}

	if err := writeBackendMetadata(backendPath, metadata); err != nil {
		return fmt.Errorf("failed to write metadata for backend %q: %v", name, err)
	}

	return RegisterBackends(systemState, modelLoader)
}

func DeleteBackendFromSystem(systemState *system.SystemState, name string) error {
	backends, err := ListSystemBackends(systemState)
	if err != nil {
		return err
	}

	backend, ok := backends.Get(name)
	if !ok {
		// Not found by direct key — try matching by gallery name (metadata.Name)
		// The UI may send gallery-style names like "localai@llama-cpp" which
		// don't match the directory-based keys used in the backends map.
		for _, b := range backends {
			if b.Metadata != nil && b.Metadata.Name == name && !b.IsMeta {
				backend = b
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("backend %q: %w", name, ErrBackendNotFound)
		}
	}

	if backend.IsSystem {
		return fmt.Errorf("system backend %q cannot be deleted", name)
	}

	// Use the backend's actual Name (directory key) for path resolution,
	// not the caller-supplied name which may be a gallery-style name.
	dirName := backend.Name
	backendDirectory := filepath.Join(systemState.Backend.BackendsPath, dirName)

	// check if the backend dir exists
	if _, err := os.Stat(backendDirectory); os.IsNotExist(err) {
		// if doesn't exist, it might be an alias, so we need to check if we have a matching alias in
		// all the backends in the basePath
		backends, err := os.ReadDir(systemState.Backend.BackendsPath)
		if err != nil {
			return err
		}
		foundBackend := false

		for _, backend := range backends {
			if backend.IsDir() {
				metadata, err := readBackendMetadata(filepath.Join(systemState.Backend.BackendsPath, backend.Name()))
				if err != nil {
					return err
				}
				if metadata != nil && (metadata.Alias == name || metadata.Alias == dirName) {
					backendDirectory = filepath.Join(systemState.Backend.BackendsPath, backend.Name())
					foundBackend = true
					break
				}
			}
		}

		// If no backend found, return successfully (idempotent behavior)
		if !foundBackend {
			return fmt.Errorf("no backend found with name %q", name)
		}
	}

	// If it's a meta backend, delete also associated backend
	metadata, err := readBackendMetadata(backendDirectory)
	if err != nil {
		return err
	}

	if metadata != nil && metadata.MetaBackendFor != "" {
		concreteDirectory := filepath.Join(systemState.Backend.BackendsPath, metadata.MetaBackendFor)
		xlog.Debug("Deleting concrete backend referenced by meta", "concreteDirectory", concreteDirectory)
		// If the concrete the meta points to is already gone (earlier delete,
		// partial install, or manual cleanup), keep going and remove the
		// orphaned meta dir. Previously we returned an error here, which made
		// the orphaned meta impossible to uninstall from the UI — the delete
		// kept failing and every subsequent install short-circuited because
		// the stale meta metadata made ListSystemBackends.Exists(name) true.
		if _, statErr := os.Stat(concreteDirectory); statErr == nil {
			os.RemoveAll(concreteDirectory)
		} else if os.IsNotExist(statErr) {
			xlog.Warn("Concrete backend referenced by meta not found — removing orphaned meta only",
				"meta", name, "concrete", metadata.MetaBackendFor)
		} else {
			return statErr
		}
	}

	return os.RemoveAll(backendDirectory)
}

// isBackendRunnable reports whether the given backend entry can actually be
// invoked. A meta backend is runnable only if its concrete's run.sh still
// exists on disk; concrete backends are considered runnable as long as their
// RunFile is set (ListSystemBackends only emits them when the runfile is
// present). Used to guard the "already installed" short-circuit so an
// orphaned meta pointing at a missing concrete triggers a real reinstall
// rather than being silently skipped.
func isBackendRunnable(b SystemBackend) bool {
	if b.RunFile == "" {
		return false
	}
	if fi, err := os.Stat(b.RunFile); err != nil || fi.IsDir() {
		return false
	}
	return true
}

type SystemBackend struct {
	Name             string
	RunFile          string
	IsMeta           bool
	IsSystem         bool
	Metadata         *BackendMetadata
	UpgradeAvailable bool   `json:"upgrade_available,omitempty"`
	AvailableVersion string `json:"available_version,omitempty"`
	// Nodes holds per-node attribution in distributed mode. Empty in single-node.
	// Each entry describes a node that has this backend installed, with the
	// version/digest it reports. Lets the UI surface drift and per-node status.
	Nodes []NodeBackendRef `json:"nodes,omitempty"`
}

// NodeBackendRef describes one node's view of an installed backend. Used both
// for per-node attribution in the UI and for drift detection during upgrade
// checks (a cluster with mismatched versions/digests is flagged upgradeable).
type NodeBackendRef struct {
	NodeID      string `json:"node_id"`
	NodeName    string `json:"node_name"`
	NodeStatus  string `json:"node_status"` // healthy | unhealthy | offline | draining | pending
	Version     string `json:"version,omitempty"`
	Digest      string `json:"digest,omitempty"`
	URI         string `json:"uri,omitempty"`
	InstalledAt string `json:"installed_at,omitempty"`
}

type SystemBackends map[string]SystemBackend

func (b SystemBackends) Exists(name string) bool {
	_, ok := b[name]
	return ok
}

func (b SystemBackends) Get(name string) (SystemBackend, bool) {
	backend, ok := b[name]
	return backend, ok
}

func (b SystemBackends) GetAll() []SystemBackend {
	backends := make([]SystemBackend, 0)
	for _, backend := range b {
		backends = append(backends, backend)
	}
	return backends
}

func ListSystemBackends(systemState *system.SystemState) (SystemBackends, error) {
	// Gather backends from system and user paths, then resolve alias conflicts by capability.
	backends := make(SystemBackends)

	// System-provided backends
	if systemBackends, err := os.ReadDir(systemState.Backend.BackendsSystemPath); err == nil {
		for _, systemBackend := range systemBackends {
			if systemBackend.IsDir() {
				run := filepath.Join(systemState.Backend.BackendsSystemPath, systemBackend.Name(), runFile)
				if _, err := os.Stat(run); err == nil {
					backends[systemBackend.Name()] = SystemBackend{
						Name:     systemBackend.Name(),
						RunFile:  run,
						IsMeta:   false,
						IsSystem: true,
						Metadata: nil,
					}
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		xlog.Warn("Failed to read system backends, proceeding with user-managed backends", "error", err)
	} else if errors.Is(err, os.ErrNotExist) {
		xlog.Debug("No system backends found")
	}

	// User-managed backends and alias collection
	entries, err := os.ReadDir(systemState.Backend.BackendsPath)
	if err != nil {
		return nil, err
	}

	aliasGroups := make(map[string][]backendCandidate)
	metaMap := make(map[string]*BackendMetadata)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := e.Name()
		run := filepath.Join(systemState.Backend.BackendsPath, dir, runFile)

		var metadata *BackendMetadata
		metadataPath := filepath.Join(systemState.Backend.BackendsPath, dir, metadataFile)
		if _, err := os.Stat(metadataPath); os.IsNotExist(err) {
			metadata = &BackendMetadata{Name: dir}
		} else {
			m, rerr := readBackendMetadata(filepath.Join(systemState.Backend.BackendsPath, dir))
			if rerr != nil {
				return nil, rerr
			}
			if m == nil {
				metadata = &BackendMetadata{Name: dir}
			} else {
				metadata = m
			}
		}

		metaMap[dir] = metadata

		// Concrete-backend entry
		if _, err := os.Stat(run); err == nil {
			backends[dir] = SystemBackend{
				Name:     dir,
				RunFile:  run,
				IsMeta:   false,
				Metadata: metadata,
			}
		}

		// Alias candidates
		if metadata.Alias != "" {
			aliasGroups[metadata.Alias] = append(aliasGroups[metadata.Alias], backendCandidate{name: dir, runFile: run})
		}

		// Meta backends indirection
		if metadata.MetaBackendFor != "" {
			backends[metadata.Name] = SystemBackend{
				Name:     metadata.Name,
				RunFile:  filepath.Join(systemState.Backend.BackendsPath, metadata.MetaBackendFor, runFile),
				IsMeta:   true,
				Metadata: metadata,
			}
		}
	}

	// Resolve aliases using system capability preferences
	tokens := systemState.BackendPreferenceTokens()
	for alias, cands := range aliasGroups {
		chosen := backendCandidate{}
		// Try preference tokens
		for _, t := range tokens {
			for _, c := range cands {
				if strings.Contains(strings.ToLower(c.name), t) && c.runFile != "" {
					chosen = c
					break
				}
			}
			if chosen.runFile != "" {
				break
			}
		}
		// Fallback: first runnable
		if chosen.runFile == "" {
			for _, c := range cands {
				if c.runFile != "" {
					chosen = c
					break
				}
			}
		}
		if chosen.runFile == "" {
			continue
		}
		md := metaMap[chosen.name]
		backends[alias] = SystemBackend{
			Name:     alias,
			RunFile:  chosen.runFile,
			IsMeta:   false,
			Metadata: md,
		}
	}

	return backends, nil
}

func RegisterBackends(systemState *system.SystemState, modelLoader *model.ModelLoader) error {
	backends, err := ListSystemBackends(systemState)
	if err != nil {
		return err
	}

	for _, backend := range backends {
		xlog.Debug("Registering backend", "name", backend.Name, "runFile", backend.RunFile)
		modelLoader.SetExternalBackend(backend.Name, backend.RunFile)
	}

	return nil
}
