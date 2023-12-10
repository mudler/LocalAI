package mqtt

import (
	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/pkg/datamodel"
	"github.com/go-skynet/LocalAI/pkg/model"
)

// PLACEHOLDER DURING PART 1 OF THE REFACTOR

type Manager struct {
	configLoader *backend.ConfigLoader
	modelLoader  *model.ModelLoader
}

func NewManager(cl *backend.ConfigLoader, ml *model.ModelLoader, options *datamodel.StartupOptions) (*Manager, error) {

	return &Manager{
		configLoader: cl,
		modelLoader:  ml,
	}, nil
}
