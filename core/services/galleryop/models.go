package galleryop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/safefile"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
	"gopkg.in/yaml.v3"
)

const (
	processingMessage = "processing file: %s. Total: %s. Current: %s"
)

func (g *GalleryService) modelHandler(op *ManagementOp[gallery.GalleryModel, gallery.ModelConfig], cl *config.ModelConfigLoader, systemState *system.SystemState) error {
	if op.Delete && cl != nil {
		return cl.WithModelConfigMutation(func() error {
			return g.modelHandlerLocked(op, cl, systemState)
		})
	}
	return g.modelHandlerLocked(op, cl, systemState)
}

func (g *GalleryService) modelHandlerLocked(op *ManagementOp[gallery.GalleryModel, gallery.ModelConfig], cl *config.ModelConfigLoader, systemState *system.SystemState) (returnErr error) {
	utils.ResetDownloadTimers()
	var deleteSnapshot *modelConfigFilesSnapshot
	deleteStarted := false
	deleteCommitted := false
	var priorConfigs []config.ModelConfig
	if op.Delete && cl != nil && systemState != nil {
		var err error
		deleteSnapshot, err = snapshotModelConfigFiles(systemState.Model.ModelsPath)
		if err != nil {
			return err
		}
		priorConfigs = cl.GetAllModelsConfigs()
		defer func() {
			if !deleteStarted || deleteCommitted {
				return
			}
			if err := deleteSnapshot.restore(); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("restore model configuration after failed deletion: %w", err))
			}
			cl.ReplaceModelConfigs(priorConfigs)
		}()
	}

	// Dedup check in distributed mode — skip if another instance is already processing this element
	if g.galleryStore != nil && op.GalleryElementName != "" && !op.Delete {
		dup, err := g.galleryStore.FindDuplicate(op.GalleryElementName)
		if err == nil && dup != nil && dup.ID != op.ID {
			g.UpdateStatus(op.ID, &OpStatus{
				Processed: true,
				Message:   fmt.Sprintf("already being processed by another instance (op %s)", dup.ID),
			})
			return nil
		}
	}

	// Check if already cancelled
	if op.Context != nil {
		select {
		case <-op.Context.Done():
			g.UpdateStatus(op.ID, &OpStatus{
				Cancelled:          true,
				Processed:          true,
				Message:            "cancelled",
				GalleryElementName: op.GalleryElementName,
			})
			return op.Context.Err()
		default:
		}
	}

	// Starting the operation NARROWS what can be cancelled, which is the reverse
	// of the usual shape: markQueued reports every queued op as cancellable
	// because abandoning it before the worker takes it leaves no trace. From
	// here on that only holds for an install, whose download watches
	// operationCtx. DeleteModel takes no context and cannot be interrupted, so a
	// Cancel button on a running removal is one the server cannot honour.
	g.UpdateStatus(op.ID, &OpStatus{Message: fmt.Sprintf("processing model: %s", op.GalleryElementName), Progress: 0, Cancellable: !op.Delete})

	bridge := newArtifactProgressBridge(func(status *OpStatus) {
		status.GalleryElementName = op.GalleryElementName
		g.UpdateStatus(op.ID, status)
	})
	coalescer := newArtifactProgressCoalescer(250*time.Millisecond, bridge.Sink)
	defer coalescer.Close()
	operationCtx := op.Context
	if operationCtx == nil {
		operationCtx = context.Background()
	}
	operationCtx = modelartifacts.WithProgressSink(operationCtx, coalescer.Sink)
	legacyCoalescer := newLegacyProgressCoalescer(250*time.Millisecond, func(update legacyProgressUpdate) {
		percentage := bridge.ClampLegacy(update.percentage)
		status := &OpStatus{Message: fmt.Sprintf(processingMessage, update.fileName, update.total, update.current), FileName: update.fileName, Progress: percentage, TotalFileSize: update.total, DownloadedFileSize: update.current, Cancellable: true}
		if currentBytes, ok := parseDisplayedBytes(update.current); ok {
			if totalBytes, totalOK := parseDisplayedBytes(update.total); totalOK {
				status.CurrentBytes = currentBytes
				status.TotalBytes = totalBytes
			}
		}
		status.GalleryElementName = op.GalleryElementName
		g.UpdateStatus(op.ID, status)
	})
	defer legacyCoalescer.Close()

	// displayDownload displays the download progress
	progressCallback := func(fileName string, current string, total string, percentage float64) {
		// Check for cancellation during progress updates
		if op.Context != nil {
			select {
			case <-op.Context.Done():
				return
			default:
			}
		}
		legacyCoalescer.Sink(fileName, current, total, percentage)
		utils.DisplayDownloadFunction(fileName, current, total, percentage)
	}

	var err error
	configRevision := ""
	if op.Delete {
		configRevision = fmt.Sprintf("%x", sha256.Sum256([]byte("deleted\x00"+op.GalleryElementName)))
		deleteStarted = true
		err = g.modelManager.DeleteModel(op.GalleryElementName)
	} else {
		err = g.modelManager.InstallModel(operationCtx, op, progressCallback)
	}
	if err != nil {
		legacyCoalescer.Close()
		// Check if error is due to cancellation
		if op.Context != nil && errors.Is(err, op.Context.Err()) {
			g.UpdateStatus(op.ID, &OpStatus{
				Cancelled:          true,
				Processed:          true,
				Message:            "cancelled",
				GalleryElementName: op.GalleryElementName,
			})
			return err
		}
		// Check if the download was paused — the .partial is preserved for
		// later resume, so this is not a terminal failure.
		if errors.Is(err, downloader.ErrUserPaused) {
			g.UpdateStatus(op.ID, &OpStatus{
				Paused:             true,
				Message:            "paused",
				GalleryElementName: op.GalleryElementName,
				Cancellable:        true,
			})
			// Store the operation metadata so ResumeOperation can re-queue it.
			g.storePausedOp(op.ID, &PausedModelOp{
				Galleries:          op.Galleries,
				BackendGalleries:   op.BackendGalleries,
				Req:                op.Req,
				GalleryElementName: op.GalleryElementName,
			})
			// Return nil so Start() does not call updateError — this is not a
			// failure, it's a deliberate pause.
			return nil
		}
		return err
	}

	// Check for cancellation before final steps
	if op.Context != nil {
		select {
		case <-op.Context.Done():
			legacyCoalescer.Close()
			g.UpdateStatus(op.ID, &OpStatus{
				Cancelled:          true,
				Processed:          true,
				Message:            "cancelled",
				GalleryElementName: op.GalleryElementName,
			})
			return op.Context.Err()
		default:
		}
	}

	// Parse a complete disk snapshot. LoadModelConfigsFromPath is additive on
	// an existing loader, so using it directly would retain a just-deleted
	// model and could preload artifacts for a config that no longer exists.
	authoritative := config.NewModelConfigLoader(systemState.Model.ModelsPath)
	err = authoritative.LoadModelConfigsFromPathStrict(systemState.Model.ModelsPath, g.appConfig.ToConfigLoaderOptions()...)
	if err != nil {
		return err
	}
	cl.ReplaceModelConfigs(authoritative.GetAllModelsConfigs())
	err = cl.PreloadWithContext(operationCtx, systemState.Model.ModelsPath)
	if err != nil {
		return err
	}

	// Lifecycle publication is the irreversible boundary. File mutation,
	// authoritative parsing, loader replacement, and preload have all completed,
	// so no later failure can roll local configuration back behind an accepted
	// registry revision.
	if op.Delete && g.modelRevisionLifecycle != nil {
		pending, lifecycleErr := g.modelRevisionLifecycle.ApplyConfigRevisions(operationCtx, []config.ModelConfigRevisionTransition{{
			ModelName: op.GalleryElementName, ConfigRevision: configRevision, Disabled: true,
		}})
		if lifecycleErr != nil {
			return lifecycleErr
		}
		if pending > 0 {
			xlog.Warn("Model deletion continuing with exact cleanup pending", "model", op.GalleryElementName, "configRevision", configRevision, "pendingCleanup", pending)
		}
	}
	deleteCommitted = true

	// Tell peer replicas to refresh their own ModelConfigLoader. The
	// authoritative replacement above already covered THIS replica; without
	// this broadcast a chat completion routed by the load balancer to a peer
	// would fail to find a model just installed.
	op2 := "install"
	if op.Delete {
		op2 = "delete"
	}
	g.publishCacheInvalidate(messaging.SubjectCacheInvalidateModels, messaging.CacheInvalidateEvent{
		Element:        op.GalleryElementName,
		Op:             op2,
		ConfigRevision: configRevision,
	})

	legacyCoalescer.Close()
	g.UpdateStatus(op.ID,
		&OpStatus{
			Deletion:           op.Delete,
			Processed:          true,
			GalleryElementName: op.GalleryElementName,
			Message:            "completed",
			Progress:           100,
			Cancellable:        false})

	return nil
}

type savedModelConfigFile struct {
	data []byte
	mode os.FileMode
}

type modelConfigFilesSnapshot struct {
	dir   string
	files map[string]savedModelConfigFile
}

func isModelConfigMetadata(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")
}

func snapshotModelConfigFiles(dir string) (*modelConfigFilesSnapshot, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("snapshot model configuration: %w", err)
	}
	snapshot := &modelConfigFilesSnapshot{dir: dir, files: map[string]savedModelConfigFile{}}
	for _, entry := range entries {
		if entry.IsDir() || !isModelConfigMetadata(entry.Name()) {
			continue
		}
		data, mode, err := safefile.ReadRegularAt(dir, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("snapshot model configuration metadata %q: %w", entry.Name(), err)
		}
		snapshot.files[entry.Name()] = savedModelConfigFile{data: data, mode: mode}
	}
	return snapshot, nil
}

func (s *modelConfigFilesSnapshot) restore() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	var restoreErr error
	for _, entry := range entries {
		if entry.IsDir() || !isModelConfigMetadata(entry.Name()) {
			continue
		}
		if _, exists := s.files[entry.Name()]; exists {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			restoreErr = errors.Join(restoreErr, err)
		}
	}
	for name, file := range s.files {
		if err := writeRestoredConfigFile(filepath.Join(s.dir, name), file.data, file.mode); err != nil {
			restoreErr = errors.Join(restoreErr, err)
		}
	}
	return restoreErr
}

func writeRestoredConfigFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".model-config-restore-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func installModelFromRemoteConfig(ctx context.Context, systemState *system.SystemState, modelLoader *model.ModelLoader, req gallery.GalleryModel, downloadStatus func(string, string, string, float64), enforceScan, automaticallyInstallBackend bool, backendGalleries []config.Gallery, requireBackendIntegrity bool, options ...gallery.InstallOption) error {
	config, err := gallery.GetGalleryConfigFromURLWithContext[gallery.ModelConfig](ctx, req.URL, systemState.Model.ModelsPath)
	if err != nil {
		return err
	}

	config.Files = append(config.Files, req.AdditionalFiles...)

	installedModel, err := gallery.InstallModel(ctx, systemState, req.Name, &config, req.Overrides, downloadStatus, enforceScan, options...)
	if err != nil {
		return err
	}

	if automaticallyInstallBackend && installedModel.Backend != "" {
		if err := gallery.InstallBackendFromGallery(ctx, backendGalleries, systemState, modelLoader, installedModel.Backend, downloadStatus, false, requireBackendIntegrity); err != nil {
			return err
		}
	}

	return nil
}

type galleryModel struct {
	gallery.GalleryModel `yaml:",inline"` // https://github.com/go-yaml/yaml/issues/63
	ID                   string           `json:"id"`
	// Variant pins the install to one of the entry's declared variants. Empty
	// means auto-select.
	Variant string `json:"variant,omitempty" yaml:"variant,omitempty"`
}

func processRequests(systemState *system.SystemState, modelLoader *model.ModelLoader, enforceScan, automaticallyInstallBackend bool, galleries []config.Gallery, backendGalleries []config.Gallery, requests []galleryModel, requireBackendIntegrity bool, options ...gallery.InstallOption) error {
	ctx := context.Background()
	var err error
	for _, r := range requests {
		utils.ResetDownloadTimers()
		if r.ID == "" {
			err = installModelFromRemoteConfig(ctx, systemState, modelLoader, r.GalleryModel, utils.DisplayDownloadFunction, enforceScan, automaticallyInstallBackend, backendGalleries, requireBackendIntegrity, options...)

		} else {
			// Cloned rather than appended to in place: `options` is shared by
			// every request in the batch, so appending would leak one request's
			// pin onto the next request that reuses the same backing array.
			requestOptions := options
			if r.Variant != "" {
				requestOptions = append(slices.Clone(options), gallery.WithVariant(r.Variant))
			}
			err = gallery.InstallModelFromGallery(
				ctx, galleries, backendGalleries, systemState, modelLoader, r.ID, r.GalleryModel, utils.DisplayDownloadFunction, enforceScan, automaticallyInstallBackend, requireBackendIntegrity, requestOptions...)
		}
	}
	return err
}

func ApplyGalleryFromFile(systemState *system.SystemState, modelLoader *model.ModelLoader, enforceScan, automaticallyInstallBackend bool, galleries []config.Gallery, backendGalleries []config.Gallery, s string, requireBackendIntegrity bool, options ...gallery.InstallOption) error {
	dat, err := os.ReadFile(s)
	if err != nil {
		return err
	}
	var requests []galleryModel

	if err := yaml.Unmarshal(dat, &requests); err != nil {
		return err
	}

	return processRequests(systemState, modelLoader, enforceScan, automaticallyInstallBackend, galleries, backendGalleries, requests, requireBackendIntegrity, options...)
}

func ApplyGalleryFromString(systemState *system.SystemState, modelLoader *model.ModelLoader, enforceScan, automaticallyInstallBackend bool, galleries []config.Gallery, backendGalleries []config.Gallery, s string, requireBackendIntegrity bool, options ...gallery.InstallOption) error {
	var requests []galleryModel
	err := json.Unmarshal([]byte(s), &requests)
	if err != nil {
		return fmt.Errorf("invalid PRELOAD_MODELS/--preload-models value: expected a JSON array of model requests: %w", err)
	}
	if requests == nil {
		return fmt.Errorf("invalid PRELOAD_MODELS/--preload-models value: expected a JSON array of model requests")
	}

	return processRequests(systemState, modelLoader, enforceScan, automaticallyInstallBackend, galleries, backendGalleries, requests, requireBackendIntegrity, options...)
}
