package mqtt

import (
	"github.com/go-skynet/LocalAI/core/services"
	"github.com/go-skynet/LocalAI/pkg/datamodel"
	"github.com/go-skynet/LocalAI/pkg/model"
)

// PLACEHOLDER DURING PART 1 OF THE REFACTOR

type Manager struct {
	configLoader *services.ConfigLoader
	modelLoader  *model.ModelLoader
}

func NewManager(cl *services.ConfigLoader, ml *model.ModelLoader, options *datamodel.StartupOptions) (*Manager, error) {

	return &Manager{
		configLoader: cl,
		modelLoader:  ml,
	}, nil
}
