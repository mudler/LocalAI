package services

import (
	"context"
	"fmt"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
)

type GalleryService struct {
	appConfig *config.ApplicationConfig
	sync.Mutex
	ModelGalleryChannel   chan GalleryOp[gallery.GalleryModel, gallery.ModelConfig]
	BackendGalleryChannel chan GalleryOp[gallery.GalleryBackend, any]

	modelLoader   *model.ModelLoader
	statuses      map[string]*GalleryOpStatus
	cancellations map[string]context.CancelFunc
}

func NewGalleryService(appConfig *config.ApplicationConfig, ml *model.ModelLoader) *GalleryService {
	return &GalleryService{
		appConfig:             appConfig,
		ModelGalleryChannel:   make(chan GalleryOp[gallery.GalleryModel, gallery.ModelConfig]),
		BackendGalleryChannel: make(chan GalleryOp[gallery.GalleryBackend, any]),
		modelLoader:           ml,
		statuses:              make(map[string]*GalleryOpStatus),
		cancellations:         make(map[string]context.CancelFunc),
	}
}

func (g *GalleryService) UpdateStatus(s string, op *GalleryOpStatus) {
	g.Lock()
	defer g.Unlock()
	g.statuses[s] = op
}

func (g *GalleryService) GetStatus(s string) *GalleryOpStatus {
	g.Lock()
	defer g.Unlock()

	return g.statuses[s]
}

func (g *GalleryService) GetAllStatus() map[string]*GalleryOpStatus {
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

	// Update status to reflect cancellation
	if status, ok := g.statuses[id]; ok {
		status.Cancelled = true
		status.Processed = true
		status.Message = "cancelled"
	} else {
		// Create status for queued operations that haven't started yet
		g.statuses[id] = &GalleryOpStatus{
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
			g.UpdateStatus(id, &GalleryOpStatus{Error: e, Processed: true, Message: "error: " + e.Error()})
		}
	} else {
		updateError = func(id string, _ error) {
			g.UpdateStatus(id, &GalleryOpStatus{Error: fmt.Errorf("an error occurred"), Processed: true})
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
				err := g.backendHandler(&op, systemState)
				if err != nil {
					updateError(op.ID, err)
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
