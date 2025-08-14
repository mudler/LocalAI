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
	ModelGalleryChannel   chan GalleryOp[gallery.GalleryModel]
	BackendGalleryChannel chan GalleryOp[gallery.GalleryBackend]

	modelLoader *model.ModelLoader
	statuses    map[string]*GalleryOpStatus
}

func NewGalleryService(appConfig *config.ApplicationConfig, ml *model.ModelLoader) *GalleryService {
	return &GalleryService{
		appConfig:             appConfig,
		ModelGalleryChannel:   make(chan GalleryOp[gallery.GalleryModel]),
		BackendGalleryChannel: make(chan GalleryOp[gallery.GalleryBackend]),
		modelLoader:           ml,
		statuses:              make(map[string]*GalleryOpStatus),
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
				err := g.backendHandler(&op, systemState)
				if err != nil {
					updateError(op.ID, err)
				}

			case op := <-g.ModelGalleryChannel:
				err := g.modelHandler(&op, cl, systemState)
				if err != nil {
					updateError(op.ID, err)
				}
			}
		}
	}()

	return nil
}
