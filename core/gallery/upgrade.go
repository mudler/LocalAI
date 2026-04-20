package gallery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/oci"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
	cp "github.com/otiai10/copy"
)

// UpgradeInfo holds details about an available backend upgrade.
type UpgradeInfo struct {
	BackendName      string `json:"backend_name"`
	InstalledVersion string `json:"installed_version"`
	AvailableVersion string `json:"available_version"`
	InstalledDigest  string `json:"installed_digest,omitempty"`
	AvailableDigest  string `json:"available_digest,omitempty"`
	// NodeDrift lists nodes whose installed version or digest differs from
	// the cluster majority. Non-empty means the cluster has diverged and an
	// upgrade will realign it. Empty in single-node mode.
	NodeDrift []NodeDriftInfo `json:"node_drift,omitempty"`
}

// NodeDriftInfo describes one node that disagrees with the cluster majority
// on which version/digest of a backend is installed.
type NodeDriftInfo struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	Version  string `json:"version,omitempty"`
	Digest   string `json:"digest,omitempty"`
}

// CheckBackendUpgrades is the single-node entrypoint. Distributed callers use
// CheckUpgradesAgainst directly with their aggregated SystemBackends.
func CheckBackendUpgrades(ctx context.Context, galleries []config.Gallery, systemState *system.SystemState) (map[string]UpgradeInfo, error) {
	installed, err := ListSystemBackends(systemState)
	if err != nil {
		return nil, fmt.Errorf("failed to list installed backends: %w", err)
	}
	return CheckUpgradesAgainst(ctx, galleries, systemState, installed)
}

// CheckUpgradesAgainst compares a caller-supplied SystemBackends set against
// the gallery. Fixes the distributed-mode bug where the old code passed the
// frontend's (empty) local filesystem through ListSystemBackends and so never
// surfaced any upgrades.
//
// Cluster drift policy: if a backend's per-node versions/digests disagree, the
// row is flagged upgradeable regardless of whether any node matches the gallery
// — next Upgrade All realigns the cluster. NodeDrift lists the outliers.
func CheckUpgradesAgainst(ctx context.Context, galleries []config.Gallery, systemState *system.SystemState, installedBackends SystemBackends) (map[string]UpgradeInfo, error) {
	galleryBackends, err := AvailableBackends(galleries, systemState)
	if err != nil {
		return nil, fmt.Errorf("failed to list available backends: %w", err)
	}

	result := make(map[string]UpgradeInfo)

	for _, installed := range installedBackends {
		// Skip system backends — they are managed outside the gallery
		if installed.IsSystem {
			continue
		}
		if installed.Metadata == nil {
			continue
		}

		// Find matching gallery entry by metadata name
		galleryEntry := FindGalleryElement(galleryBackends, installed.Metadata.Name)
		if galleryEntry == nil {
			continue
		}

		installedVersion := installed.Metadata.Version
		installedDigest := installed.Metadata.Digest
		galleryVersion := galleryEntry.Version

		// Detect cluster drift: does every node report the same version+digest?
		// In single-node mode this stays empty (Nodes is nil).
		majority, drift := summarizeNodeDrift(installed.Nodes)
		if majority.version != "" {
			installedVersion = majority.version
		}
		if majority.digest != "" {
			installedDigest = majority.digest
		}

		makeInfo := func(info UpgradeInfo) UpgradeInfo {
			info.NodeDrift = drift
			return info
		}

		// If versions are available on both sides, they're the source of truth.
		if galleryVersion != "" && installedVersion != "" {
			if galleryVersion != installedVersion || len(drift) > 0 {
				result[installed.Metadata.Name] = makeInfo(UpgradeInfo{
					BackendName:      installed.Metadata.Name,
					InstalledVersion: installedVersion,
					AvailableVersion: galleryVersion,
				})
			}
			continue
		}

		// Gallery has a version but installed doesn't — backends installed before
		// version tracking was added. Flag as upgradeable to pick up metadata.
		if galleryVersion != "" && installedVersion == "" {
			result[installed.Metadata.Name] = makeInfo(UpgradeInfo{
				BackendName:      installed.Metadata.Name,
				InstalledVersion: "",
				AvailableVersion: galleryVersion,
			})
			continue
		}

		// Fall back to OCI digest comparison when versions are unavailable.
		if downloader.URI(galleryEntry.URI).LooksLikeOCI() {
			remoteDigest, err := oci.GetImageDigest(galleryEntry.URI, "", nil, nil)
			if err != nil {
				xlog.Warn("Failed to get remote OCI digest for upgrade check", "backend", installed.Metadata.Name, "error", err)
				continue
			}
			// If we have a stored digest, compare; otherwise any remote digest
			// means we can't confirm we're up to date — flag as upgradeable.
			if installedDigest == "" || remoteDigest != installedDigest || len(drift) > 0 {
				result[installed.Metadata.Name] = makeInfo(UpgradeInfo{
					BackendName:     installed.Metadata.Name,
					InstalledDigest: installedDigest,
					AvailableDigest: remoteDigest,
				})
			}
		} else if len(drift) > 0 {
			// No version/digest path but nodes disagree — still worth flagging.
			result[installed.Metadata.Name] = makeInfo(UpgradeInfo{
				BackendName:      installed.Metadata.Name,
				InstalledVersion: installedVersion,
				InstalledDigest:  installedDigest,
			})
		}
	}

	return result, nil
}

// summarizeNodeDrift collapses per-node version/digest tuples to a majority
// pair and returns the outliers. In single-node mode (empty nodes slice) this
// returns zero values and a nil drift list.
func summarizeNodeDrift(nodes []NodeBackendRef) (majority struct{ version, digest string }, drift []NodeDriftInfo) {
	if len(nodes) == 0 {
		return majority, nil
	}

	type key struct{ version, digest string }
	counts := map[key]int{}
	var topKey key
	var topCount int
	for _, n := range nodes {
		k := key{n.Version, n.Digest}
		counts[k]++
		if counts[k] > topCount {
			topCount = counts[k]
			topKey = k
		}
	}

	majority.version = topKey.version
	majority.digest = topKey.digest

	if len(counts) == 1 {
		return majority, nil // unanimous — no drift
	}
	for _, n := range nodes {
		if n.Version == majority.version && n.Digest == majority.digest {
			continue
		}
		drift = append(drift, NodeDriftInfo{
			NodeID:   n.NodeID,
			NodeName: n.NodeName,
			Version:  n.Version,
			Digest:   n.Digest,
		})
	}
	return majority, drift
}

// UpgradeBackend upgrades a single backend to the latest gallery version using
// an atomic swap with backup-based rollback on failure.
func UpgradeBackend(ctx context.Context, systemState *system.SystemState, modelLoader *model.ModelLoader, galleries []config.Gallery, backendName string, downloadStatus func(string, string, string, float64)) error {
	// Look up the installed backend
	installedBackends, err := ListSystemBackends(systemState)
	if err != nil {
		return fmt.Errorf("failed to list installed backends: %w", err)
	}

	installed, ok := installedBackends.Get(backendName)
	if !ok {
		return fmt.Errorf("backend %q: %w", backendName, ErrBackendNotFound)
	}

	if installed.IsSystem {
		return fmt.Errorf("system backend %q cannot be upgraded via gallery", backendName)
	}

	// If this is a meta backend, recursively upgrade the concrete backend it points to
	if installed.Metadata != nil && installed.Metadata.MetaBackendFor != "" {
		xlog.Info("Meta backend detected, upgrading concrete backend", "meta", backendName, "concrete", installed.Metadata.MetaBackendFor)
		return UpgradeBackend(ctx, systemState, modelLoader, galleries, installed.Metadata.MetaBackendFor, downloadStatus)
	}

	// Find the gallery entry
	galleryBackends, err := AvailableBackends(galleries, systemState)
	if err != nil {
		return fmt.Errorf("failed to list available backends: %w", err)
	}

	galleryEntry := FindGalleryElement(galleryBackends, backendName)
	if galleryEntry == nil {
		return fmt.Errorf("no gallery entry found for backend %q", backendName)
	}

	backendPath := filepath.Join(systemState.Backend.BackendsPath, backendName)
	tmpPath := backendPath + ".upgrade-tmp"
	backupPath := backendPath + ".backup"

	// Clean up any stale tmp/backup dirs from prior attempts
	os.RemoveAll(tmpPath)
	os.RemoveAll(backupPath)

	// Step 1: Download the new backend into the tmp directory
	if err := os.MkdirAll(tmpPath, 0750); err != nil {
		return fmt.Errorf("failed to create upgrade tmp dir: %w", err)
	}

	uri := downloader.URI(galleryEntry.URI)
	if uri.LooksLikeDir() {
		if err := cp.Copy(string(uri), tmpPath); err != nil {
			os.RemoveAll(tmpPath)
			return fmt.Errorf("failed to copy backend from directory: %w", err)
		}
	} else {
		if err := uri.DownloadFileWithContext(ctx, tmpPath, "", 1, 1, downloadStatus); err != nil {
			os.RemoveAll(tmpPath)
			return fmt.Errorf("failed to download backend: %w", err)
		}
	}

	// Step 2: Validate — check that run.sh exists in the new content
	newRunFile := filepath.Join(tmpPath, runFile)
	if _, err := os.Stat(newRunFile); os.IsNotExist(err) {
		os.RemoveAll(tmpPath)
		return fmt.Errorf("upgrade validation failed: run.sh not found in new backend")
	}

	// Step 3: Atomic swap — rename current to backup, then tmp to current
	if err := os.Rename(backendPath, backupPath); err != nil {
		os.RemoveAll(tmpPath)
		return fmt.Errorf("failed to move current backend to backup: %w", err)
	}

	if err := os.Rename(tmpPath, backendPath); err != nil {
		// Restore backup on failure
		xlog.Error("Failed to move new backend into place, restoring backup", "error", err)
		if restoreErr := os.Rename(backupPath, backendPath); restoreErr != nil {
			xlog.Error("Failed to restore backup", "error", restoreErr)
		}
		os.RemoveAll(tmpPath)
		return fmt.Errorf("failed to move new backend into place: %w", err)
	}

	// Step 4: Write updated metadata, preserving alias from old metadata
	var oldAlias string
	if installed.Metadata != nil {
		oldAlias = installed.Metadata.Alias
	}

	newMetadata := &BackendMetadata{
		Name:        backendName,
		Version:     galleryEntry.Version,
		URI:         galleryEntry.URI,
		InstalledAt: time.Now().Format(time.RFC3339),
		Alias:       oldAlias,
	}

	if galleryEntry.Gallery.URL != "" {
		newMetadata.GalleryURL = galleryEntry.Gallery.URL
	}

	// Record OCI digest if applicable (non-fatal on failure)
	if uri.LooksLikeOCI() {
		digest, digestErr := oci.GetImageDigest(galleryEntry.URI, "", nil, nil)
		if digestErr != nil {
			xlog.Warn("Failed to get OCI image digest after upgrade", "uri", galleryEntry.URI, "error", digestErr)
		} else {
			newMetadata.Digest = digest
		}
	}

	if err := writeBackendMetadata(backendPath, newMetadata); err != nil {
		// Metadata write failure is not worth rolling back the entire upgrade
		xlog.Error("Failed to write metadata after upgrade", "error", err)
	}

	// Step 5: Re-register backends so the model loader picks up any changes
	if err := RegisterBackends(systemState, modelLoader); err != nil {
		xlog.Warn("Failed to re-register backends after upgrade", "error", err)
	}

	// Step 6: Remove backup
	os.RemoveAll(backupPath)

	xlog.Info("Backend upgraded successfully", "backend", backendName, "version", galleryEntry.Version)
	return nil
}
