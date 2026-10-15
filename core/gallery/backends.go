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
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/oci"
	"github.com/mudler/LocalAI/pkg/oci/cosignverify"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
	cp "github.com/otiai10/copy"
)

// ErrBackendNotFound is returned when a backend is not found in the system.
var ErrBackendNotFound = errors.New("backend not found")

// lookupImageDigest asks a registry for the digest of an image reference. It
// is what install and upgrade record for upgrade detection.
//
// A var so specs can see which references reach a registry client without
// running one.
var lookupImageDigest = func(ref string) (string, error) {
	return oci.GetImageDigest(ref, "", nil, nil)
}

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

// developmentURI returns the development image URI for a released backend URI by
// swapping the released tag for the branch tag (e.g.
// latest-metal-darwin-arm64-llama-cpp -> master-metal-darwin-arm64-llama-cpp).
// The branch image tracks development. ok is false when uri has no released tag
// to swap or already uses the branch tag.
func developmentURI(uri, latestTag, masterTag string) (string, bool) {
	if strings.Contains(uri, masterTag+"-") {
		return "", false
	}
	branchURI := strings.Replace(uri, latestTag+"-", masterTag+"-", 1)
	if branchURI == uri {
		return "", false
	}
	return branchURI, true
}

// backendCandidate represents an installed concrete backend option for a given alias
type backendCandidate struct {
	name     string
	runFile  string
	isSystem bool
	metadata *BackendMetadata
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

// backendDownloadOptions translates the gallery's verification policy into
// downloader options, and gates the call on strict-integrity mode. Both
// InstallBackend and UpgradeBackend MUST route their download through these
// options — without them, the corresponding code path silently downloads
// and activates unverified backend bytes even when the gallery has a
// verification: policy configured.
//
// For OCI URIs with a verification policy, returns a slice containing
// downloader.WithImageVerifier(v) — the downloader will then run cosign
// signature verification between fetching the manifest and extracting
// layers (see pkg/downloader/uri.go OCI branch).
//
// For OCI URIs without a verification policy, or non-OCI URIs without a
// SHA256, the function either returns a non-fatal warning (requireIntegrity
// false) or fails the install (requireIntegrity true).
func backendDownloadOptions(config *GalleryBackend, requireIntegrity bool) ([]downloader.DownloadOption, error) {
	uri := downloader.URI(config.URI)
	hasVerification := config.Gallery.Verification != nil
	hasSHA := config.SHA256 != ""

	switch {
	case uri.LooksLikeOCI():
		if !hasVerification {
			if requireIntegrity {
				return nil, fmt.Errorf("strict integrity: gallery %q has no verification policy for OCI backend %q (set verification: in the gallery YAML or disable --require-backend-integrity)",
					config.Gallery.Name, config.Name)
			}
			xlog.Warn("installing OCI backend without signature verification",
				"backend", config.Name, "gallery", config.Gallery.Name, "uri", config.URI)
			return nil, nil
		}
		v, err := newGalleryVerifier(config.Gallery.Verification)
		if err != nil {
			return nil, fmt.Errorf("gallery %q verification policy: %w", config.Gallery.Name, err)
		}
		return []downloader.DownloadOption{downloader.WithImageVerifier(v)}, nil

	case uri.LooksLikeDir():
		// Local directory — out of scope for integrity checks.
		return nil, nil

	default:
		if !hasSHA && requireIntegrity {
			return nil, fmt.Errorf("strict integrity: backend %q has no SHA256 (gallery %q)",
				config.Name, config.Gallery.Name)
		}
		// Non-strict: pkg/downloader already emits a warning when sha is empty.
		return nil, nil
	}
}

// newGalleryVerifier constructs a cosignverify.Verifier from the gallery
// policy. Parses NotBefore (RFC3339) here so YAML errors surface at install
// time rather than during signature verification.
func newGalleryVerifier(p *config.GalleryVerification) (*cosignverify.Verifier, error) {
	pol := cosignverify.Policy{
		Issuer:           p.Issuer,
		IssuerRegex:      p.IssuerRegex,
		Identity:         p.Identity,
		IdentityRegex:    p.IdentityRegex,
		SourceRepository: p.SourceRepository,
	}
	if p.NotBefore != "" {
		t, err := time.Parse(time.RFC3339, p.NotBefore)
		if err != nil {
			// A refusal, not an outage: no fetch can make this policy usable.
			return nil, fmt.Errorf("%w: not_before %q: %w", cosignverify.ErrPolicyRejected, p.NotBefore, err)
		}
		pol.NotBefore = t
	}
	return cosignverify.NewVerifier(pol, nil, nil)
}

// InstallBackendFromGallery installs a backend from the gallery.
// requireIntegrity escalates a missing SHA256 / verification policy from a
// warning to a hard failure (see backendDownloadOptions).
func InstallBackendFromGallery(ctx context.Context, galleries []config.Gallery, systemState *system.SystemState, modelLoader *model.ModelLoader, name string, downloadStatus func(string, string, string, float64), force, requireIntegrity bool) error {
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
		if err := InstallBackend(ctx, systemState, modelLoader, bestBackend, downloadStatus, requireIntegrity); err != nil {
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

	return InstallBackend(ctx, systemState, modelLoader, backend, downloadStatus, requireIntegrity)
}

func InstallBackend(ctx context.Context, systemState *system.SystemState, modelLoader *model.ModelLoader, config *GalleryBackend, downloadStatus func(string, string, string, float64), requireIntegrity bool) error {
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
	// Stage the download into a temp directory and atomically swap it into
	// place once it's complete and validated. This makes a reinstall a clean
	// replace rather than an overlay: files from a previous version that are
	// absent in the new artifact (a stale .so, an orphaned package dir) are
	// dropped instead of lingering to shadow the new build at import time.
	// Mirrors the atomic-swap UpgradeBackend already performs.
	stagingPath := backendPath + ".install-tmp"
	backupPath := backendPath + ".install-backup"
	// Clean any stale staging/backup dirs from a prior interrupted attempt
	// (best-effort; a missing dir is the common case).
	_ = os.RemoveAll(stagingPath)
	_ = os.RemoveAll(backupPath)
	if err := os.MkdirAll(stagingPath, 0750); err != nil {
		return fmt.Errorf("failed to create staging path: %v", err)
	}
	// Unless we successfully swap the staging dir into place below, never leave
	// it behind. After a successful rename it no longer exists, so this is a
	// no-op on the happy path.
	defer func() { _ = os.RemoveAll(stagingPath) }()

	// Build the download options once and reuse for every retry path —
	// mirrors and tag fallbacks must verify against the same gallery
	// policy or we open a hole where a non-default URI bypasses the check.
	downloadOpts, optsErr := backendDownloadOptions(config, requireIntegrity)
	if optsErr != nil {
		return fmt.Errorf("backend %q: %w", config.Name, optsErr)
	}
	downloadOpts = append(downloadOpts, downloader.WithStagingDir(systemState.StagingPath))

	// PreferDevelopmentBackends installs the development image as the primary URI,
	// keeping the released image reachable as the first fallback — instead of only
	// reaching development when the released image is missing.
	primaryURI := string(config.URI)
	mirrors := config.Mirrors
	if systemState.PreferDevelopmentBackends {
		if devURI, ok := developmentURI(string(config.URI), latestTag, masterTag); ok {
			xlog.Info("PreferDevelopmentBackends: installing development image first", "development", devURI, "released", config.URI)
			primaryURI = devURI
			mirrors = append([]string{string(config.URI)}, config.Mirrors...)
		}
	}

	uri := downloader.URI(primaryURI)
	// Check if it is a directory
	if uri.LooksLikeDir() {
		// It is a directory, we just copy it over into the staging folder
		if err := cp.Copy(string(uri), stagingPath); err != nil {
			return fmt.Errorf("failed copying: %w", err)
		}
	} else {
		xlog.Debug("Downloading backend", "uri", primaryURI, "backendPath", stagingPath)
		if err := uri.DownloadFileWithContext(ctx, stagingPath, config.SHA256, 1, 1, downloadStatus, downloadOpts...); err != nil {
			xlog.Debug("Backend download failed, trying fallback", "backendPath", stagingPath, "error", err)

			// resetStagingPath cleans up partial state from a failed OCI extraction
			// so the next download attempt starts fresh. The directory is re-created
			// because OCI image extractors need it to exist for writing files into.
			resetStagingPath := func() {
				_ = os.RemoveAll(stagingPath)
				_ = os.MkdirAll(stagingPath, 0750)
			}

			success := false
			// Try to download from mirrors (when development is preferred, the
			// released image is prepended here as the first fallback).
			for _, mirror := range mirrors {
				// Check for cancellation before trying next mirror
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				resetStagingPath()
				if err := downloader.URI(mirror).DownloadFileWithContext(ctx, stagingPath, config.SHA256, 1, 1, downloadStatus, downloadOpts...); err == nil {
					success = true
					xlog.Debug("Downloaded backend from mirror", "uri", config.URI, "backendPath", stagingPath)
					break
				}
			}

			if !success {
				// Try fallback: replace latestTag + "-" with masterTag + "-" in the URI
				fallbackURI := strings.Replace(string(config.URI), latestTag+"-", masterTag+"-", 1)
				if fallbackURI != string(config.URI) {
					resetStagingPath()
					xlog.Info("Trying fallback URI", "original", config.URI, "fallback", fallbackURI)
					if err := downloader.URI(fallbackURI).DownloadFileWithContext(ctx, stagingPath, config.SHA256, 1, 1, downloadStatus, downloadOpts...); err == nil {
						xlog.Info("Downloaded backend using fallback URI", "uri", fallbackURI, "backendPath", stagingPath)
						success = true
					} else {
						xlog.Info("Fallback URI failed", "fallback", fallbackURI, "error", err)
						if !strings.Contains(fallbackURI, "-"+devSuffix) {
							resetStagingPath()
							devFallbackURI := fallbackURI + "-" + devSuffix
							xlog.Info("Trying development fallback URI", "fallback", devFallbackURI)
							if err := downloader.URI(devFallbackURI).DownloadFileWithContext(ctx, stagingPath, config.SHA256, 1, 1, downloadStatus, downloadOpts...); err == nil {
								xlog.Info("Downloaded backend using development fallback URI", "uri", devFallbackURI, "backendPath", stagingPath)
								success = true
							} else {
								xlog.Info("Development fallback URI failed", "fallback", devFallbackURI, "error", err)
							}
						}
					}
				}
			}

			if !success {
				// The deferred RemoveAll(stagingPath) cleans up the partial
				// download; the previously installed backend (if any) is left
				// untouched because we never touched backendPath.
				xlog.Error("Failed to download backend", "uri", config.URI, "backendPath", stagingPath, "error", err)
				return fmt.Errorf("failed to download backend %q: %v", config.URI, err)
			}
		} else {
			xlog.Debug("Downloaded backend", "uri", config.URI, "backendPath", stagingPath)
		}
	}

	// sanity check - check if runfile is present in the staged content. This
	// doubles as the validation gate before we swap it into place.
	stagedRunFile := filepath.Join(stagingPath, runFile)
	if _, err := os.Stat(stagedRunFile); os.IsNotExist(err) {
		xlog.Error("Run file not found", "runFile", stagedRunFile)
		return fmt.Errorf("not a valid backend: run file not found %q", stagedRunFile)
	}

	// Create metadata for the backend (written into the staged dir so the
	// atomic swap brings a complete, self-describing backend into place).
	metadata := &BackendMetadata{
		Name:        name,
		GalleryURL:  config.Gallery.URL,
		InstalledAt: time.Now().Format(time.RFC3339),
		Version:     config.Version,
		URI:         string(uri),
	}

	// Record the OCI digest for upgrade detection (non-fatal on failure)
	if uri.LooksLikeRegistryOCI() {
		digest, digestErr := lookupImageDigest(uri.OCIReference())
		if digestErr != nil {
			xlog.Warn("Failed to get OCI image digest for backend", "uri", string(uri), "error", digestErr)
		} else {
			metadata.Digest = digest
		}
	}

	if config.Alias != "" {
		metadata.Alias = config.Alias
	}

	if err := writeBackendMetadata(stagingPath, metadata); err != nil {
		return fmt.Errorf("failed to write metadata for backend %q: %v", name, err)
	}

	// Atomic swap: move any existing install aside, move the staged dir into
	// place, and roll the old one back if the final rename fails.
	if _, statErr := os.Stat(backendPath); statErr == nil {
		if err := os.Rename(backendPath, backupPath); err != nil {
			return fmt.Errorf("failed to move current backend to backup: %w", err)
		}
	}
	if err := os.Rename(stagingPath, backendPath); err != nil {
		xlog.Error("Failed to move new backend into place, restoring backup", "error", err)
		if restoreErr := os.Rename(backupPath, backendPath); restoreErr != nil {
			xlog.Error("Failed to restore backup", "error", restoreErr)
		}
		return fmt.Errorf("failed to move new backend into place: %w", err)
	}
	_ = os.RemoveAll(backupPath)

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

// collectedBackend is one backend directory as scanned from its root,
// before cross-root cleaning.
type collectedBackend struct {
	basePath string
	metadata *BackendMetadata
	isSystem bool
}

// collectBackendDir reads one backend directory's optional metadata.json
// into the root's collection.
func collectBackendDir(basePath, dir string, isSystem bool, entries map[string]collectedBackend) error {
	metadata := &BackendMetadata{Name: dir}
	if _, err := os.Stat(filepath.Join(basePath, dir, metadataFile)); err == nil {
		m, rerr := readBackendMetadata(filepath.Join(basePath, dir))
		if rerr != nil {
			return rerr
		}
		if m != nil {
			metadata = m
		}
	}
	entries[dir] = collectedBackend{basePath: basePath, metadata: metadata, isSystem: isSystem}
	return nil
}

// collectRoot scans one backends root into its own collection. Metadata
// errors: warn-and-skip when lenient (system root), hard error otherwise
// (user-managed root).
func collectRoot(basePath string, isSystem, lenient bool) (map[string]collectedBackend, error) {
	entries := make(map[string]collectedBackend)
	dirEntries, err := os.ReadDir(basePath)
	if err != nil {
		return entries, err
	}
	for _, e := range dirEntries {
		if !e.IsDir() {
			continue
		}
		if cerr := collectBackendDir(basePath, e.Name(), isSystem, entries); cerr != nil {
			if !lenient {
				return nil, cerr
			}
			xlog.Warn("Skipping backend with unreadable metadata", "dir", e.Name(), "error", cerr)
		}
	}
	return entries, nil
}

func ListSystemBackends(systemState *system.SystemState) (SystemBackends, error) {
	backends := make(SystemBackends)

	// 1. Scan each root separately.
	systemEntries, err := collectRoot(systemState.Backend.BackendsSystemPath, true, true)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			xlog.Debug("No system backends found")
		} else {
			xlog.Warn("Failed to read system backends, proceeding with user-managed backends", "error", err)
		}
	}
	managedEntries, err := collectRoot(systemState.Backend.BackendsPath, false, false)
	if err != nil {
		return nil, err
	}

	// 2. Clean the system collection against the user-managed one:
	//    - a user-managed backend shadows a same-named system backend
	//    - a user-managed alias member takes over its whole alias group:
	//      resolution must never mix the roots (and their possibly
	//      different versions) within one backend family, and a stale
	//      system variant may not work with newer models, so the whole
	//      system family becomes invisible, concrete names included
	managedAliases := make(map[string]bool)
	for _, e := range managedEntries {
		if e.metadata.Alias != "" {
			managedAliases[e.metadata.Alias] = true
		}
	}
	for name, e := range systemEntries {
		if _, shadowed := managedEntries[name]; shadowed {
			delete(systemEntries, name)
		} else if e.metadata.Alias != "" && managedAliases[e.metadata.Alias] {
			delete(systemEntries, name)
		}
	}

	// 3. Merge — disjoint by construction after cleaning.
	entriesByDir := managedEntries
	for name, e := range systemEntries {
		entriesByDir[name] = e
	}

	// 4. Build concrete entries, alias candidacies and meta indirection
	//    from the merged collection, in sorted order.
	dirs := make([]string, 0, len(entriesByDir))
	for dir := range entriesByDir {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)

	aliasGroups := make(map[string][]backendCandidate)
	for _, dir := range dirs {
		entry := entriesByDir[dir]
		run := filepath.Join(entry.basePath, dir, runFile)

		// Concrete-backend entry
		if _, err := os.Stat(run); err == nil {
			backends[dir] = SystemBackend{
				Name:     dir,
				RunFile:  run,
				IsMeta:   false,
				IsSystem: entry.isSystem,
				Metadata: entry.metadata,
			}
		}

		// Alias candidates
		if entry.metadata.Alias != "" {
			aliasGroups[entry.metadata.Alias] = append(aliasGroups[entry.metadata.Alias],
				backendCandidate{name: dir, runFile: run, isSystem: entry.isSystem, metadata: entry.metadata})
		}

		// Meta backends indirection
		if entry.metadata.MetaBackendFor != "" {
			backends[entry.metadata.Name] = SystemBackend{
				Name:     entry.metadata.Name,
				RunFile:  filepath.Join(entry.basePath, entry.metadata.MetaBackendFor, runFile),
				IsMeta:   true,
				IsSystem: entry.isSystem,
				Metadata: entry.metadata,
			}
		}
	}

	// Resolve aliases using system capability preferences
	tokens := systemState.BackendPreferenceTokens()
	for alias, cands := range aliasGroups {
		// First-token-match depends on candidate order: sort by name so
		// resolution is deterministic by construction, not by scan order.
		slices.SortFunc(cands, func(a, b backendCandidate) int {
			return strings.Compare(a.name, b.name)
		})
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
		if existing, ok := backends[alias]; ok && !existing.IsSystem && chosen.isSystem {
			// A system-derived alias never hijacks a user-managed
			// backend of the same name, including meta indirection.
			continue
		}
		backends[alias] = SystemBackend{
			Name:     alias,
			RunFile:  chosen.runFile,
			IsMeta:   false,
			IsSystem: chosen.isSystem,
			Metadata: chosen.metadata,
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
