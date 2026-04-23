package galleryop

import (
	"context"
	"fmt"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
)

type GalleryService struct {
	appConfig *config.ApplicationConfig
	sync.Mutex
	ModelGalleryChannel   chan ManagementOp[gallery.GalleryModel, gallery.ModelConfig]
	BackendGalleryChannel chan ManagementOp[gallery.GalleryBackend, any]

	modelLoader    *model.ModelLoader
	modelManager   ModelManager
	backendManager BackendManager
	statuses       map[string]*OpStatus
	cancellations  map[string]context.CancelFunc

	// Distributed mode (nil when not in distributed mode)
	natsClient   messaging.Publisher
	galleryStore *distributed.GalleryStore

	// OnBackendOpCompleted is fired after every successful install/upgrade/delete
	// on the backend channel. The Application wires this to UpgradeChecker.TriggerCheck
	// so `/api/backends/upgrades` stops surfacing a backend as upgradeable the moment
	// the worker finishes — previously the cache only refreshed on the 6-hour tick,
	// making manual upgrades look like they failed even when they hadn't.
	OnBackendOpCompleted func()
}

func NewGalleryService(appConfig *config.ApplicationConfig, ml *model.ModelLoader) *GalleryService {
	return &GalleryService{
		appConfig:             appConfig,
		ModelGalleryChannel:   make(chan ManagementOp[gallery.GalleryModel, gallery.ModelConfig]),
		BackendGalleryChannel: make(chan ManagementOp[gallery.GalleryBackend, any]),
		modelLoader:           ml,
		modelManager:          NewLocalModelManager(appConfig, ml),
		backendManager:        NewLocalBackendManager(appConfig, ml),
		statuses:              make(map[string]*OpStatus),
		cancellations:         make(map[string]context.CancelFunc),
	}
}

// SetModelManager replaces the model manager (e.g. with a distributed implementation).
func (g *GalleryService) SetModelManager(m ModelManager) {
	g.Lock()
	defer g.Unlock()
	g.modelManager = m
}

// SetBackendManager replaces the backend manager (e.g. with a distributed implementation).
func (g *GalleryService) SetBackendManager(b BackendManager) {
	g.Lock()
	defer g.Unlock()
	g.backendManager = b
}

// BackendManager returns the current backend manager. Callers like the
// periodic upgrade checker need this so they run CheckUpgrades through the
// distributed implementation (which asks workers) instead of the frontend's
// local filesystem — the latter is always empty in distributed deployments.
func (g *GalleryService) BackendManager() BackendManager {
	g.Lock()
	defer g.Unlock()
	return g.backendManager
}

// SetNATSClient sets the NATS client for distributed progress publishing.
func (g *GalleryService) SetNATSClient(nc messaging.Publisher) {
	g.Lock()
	defer g.Unlock()
	g.natsClient = nc
}

// SetGalleryStore sets the PostgreSQL gallery store for distributed persistence.
func (g *GalleryService) SetGalleryStore(s *distributed.GalleryStore) {
	g.Lock()
	defer g.Unlock()
	g.galleryStore = s
}

// ListBackends returns installed backends via the backend manager.
// In standalone mode this checks the local filesystem; in distributed mode
// it aggregates from all healthy worker nodes.
func (g *GalleryService) ListBackends() (gallery.SystemBackends, error) {
	g.Lock()
	mgr := g.backendManager
	g.Unlock()
	return mgr.ListBackends()
}

// DeleteBackend delegates backend deletion to the backend manager, which in distributed
// mode fans out the deletion to worker nodes via NATS.
func (g *GalleryService) DeleteBackend(name string) error {
	g.Lock()
	mgr := g.backendManager
	g.Unlock()
	return mgr.DeleteBackend(name)
}

func (g *GalleryService) UpdateStatus(s string, op *OpStatus) {
	g.Lock()
	defer g.Unlock()
	g.statuses[s] = op

	// Persist to PostgreSQL in distributed mode
	if g.galleryStore != nil {
		if op.Processed {
			status, errMsg := "completed", ""
			if op.Error != nil {
				status = "failed"
				errMsg = op.Error.Error()
			}
			if op.Cancelled {
				status = "cancelled"
			}
			g.galleryStore.UpdateStatus(s, status, errMsg)
		} else {
			g.galleryStore.UpdateProgress(s, op.Progress, op.Message, op.DownloadedFileSize)
		}
	}

	// Publish progress to NATS in distributed mode
	if g.natsClient != nil {
		g.natsClient.Publish(messaging.SubjectGalleryProgress(s), op)
	}
}

func (g *GalleryService) GetStatus(s string) *OpStatus {
	g.Lock()
	defer g.Unlock()

	return g.statuses[s]
}

func (g *GalleryService) GetAllStatus() map[string]*OpStatus {
	g.Lock()
	defer g.Unlock()

	return g.statuses
}

// CancelOperation cancels an in-progress operation by its ID
func (g *GalleryService) CancelOperation(id string) error {
	g.Lock()
	defer g.Unlock()

	// Check if operation is already cancelled
	if status, ok := g.statuses[id]; ok && status.Cancelled {
		return fmt.Errorf("operation %q is already cancelled", id)
	}

	cancelFunc, exists := g.cancellations[id]
	if !exists {
		return fmt.Errorf("operation %q not found or already completed", id)
	}

	// Cancel the operation
	cancelFunc()

	// Publish cancellation to NATS in distributed mode
	if g.natsClient != nil {
		g.natsClient.Publish(messaging.SubjectGalleryCancel(id), map[string]string{"id": id})
	}

	// Update status to reflect cancellation
	if status, ok := g.statuses[id]; ok {
		status.Cancelled = true
		status.Processed = true
		status.Message = "cancelled"
	} else {
		// Create status for queued operations that haven't started yet
		g.statuses[id] = &OpStatus{
			Cancelled:   true,
			Processed:   true,
			Message:     "cancelled",
			Cancellable: false,
		}
	}

	// Clean up cancellation function
	delete(g.cancellations, id)

	return nil
}

// storeCancellation stores a cancellation function for an operation
func (g *GalleryService) storeCancellation(id string, cancelFunc context.CancelFunc) {
	g.Lock()
	defer g.Unlock()
	g.cancellations[id] = cancelFunc
}

// StoreCancellation is a public method to store a cancellation function for an operation
// This allows cancellation functions to be stored immediately when operations are created,
// enabling cancellation of queued operations that haven't started processing yet.
func (g *GalleryService) StoreCancellation(id string, cancelFunc context.CancelFunc) {
	g.storeCancellation(id, cancelFunc)
}

// removeCancellation removes a cancellation function when operation completes
func (g *GalleryService) removeCancellation(id string) {
	g.Lock()
	defer g.Unlock()
	delete(g.cancellations, id)
}

func (g *GalleryService) Start(c context.Context, cl *config.ModelConfigLoader, systemState *system.SystemState) error {
	// updates the status with an error
	var updateError func(id string, e error)
	if !g.appConfig.OpaqueErrors {
		updateError = func(id string, e error) {
			g.UpdateStatus(id, &OpStatus{Error: e, Processed: true, Message: "error: " + e.Error()})
		}
	} else {
		updateError = func(id string, _ error) {
			g.UpdateStatus(id, &OpStatus{Error: fmt.Errorf("an error occurred"), Processed: true})
		}
	}

	go func() {
		for {
			select {
			case <-c.Done():
				return
			case op := <-g.BackendGalleryChannel:
				// Create context if not provided
				if op.Context == nil {
					op.Context, op.CancelFunc = context.WithCancel(c)
					g.storeCancellation(op.ID, op.CancelFunc)
				} else if op.CancelFunc != nil {
					g.storeCancellation(op.ID, op.CancelFunc)
				}
				// Create DB record for distributed tracking
				if g.galleryStore != nil {
					g.galleryStore.Create(&distributed.GalleryOperationRecord{
						ID:                 op.ID,
						GalleryElementName: op.GalleryElementName,
						OpType:             "backend_install",
						Status:             "pending",
					})
				}
				err := g.backendHandler(&op, systemState)
				if err != nil {
					updateError(op.ID, err)
				} else if g.OnBackendOpCompleted != nil {
					// Let listeners (e.g. UpgradeChecker) refresh their view of
					// installed state. Run off the worker goroutine so a slow
					// callback doesn't stall the next queued operation.
					go g.OnBackendOpCompleted()
				}
				g.removeCancellation(op.ID)

			case op := <-g.ModelGalleryChannel:
				// Create context if not provided
				if op.Context == nil {
					op.Context, op.CancelFunc = context.WithCancel(c)
					g.storeCancellation(op.ID, op.CancelFunc)
				} else if op.CancelFunc != nil {
					g.storeCancellation(op.ID, op.CancelFunc)
				}
				// Create DB record for distributed tracking
				if g.galleryStore != nil {
					opType := "model_install"
					if op.Delete {
						opType = "model_delete"
					}
					g.galleryStore.Create(&distributed.GalleryOperationRecord{
						ID:                 op.ID,
						GalleryElementName: op.GalleryElementName,
						OpType:             opType,
						Status:             "pending",
					})
				}
				err := g.modelHandler(&op, cl, systemState)
				if err != nil {
					updateError(op.ID, err)
				}
				g.removeCancellation(op.ID)
			}
		}
	}()

	return nil
}
