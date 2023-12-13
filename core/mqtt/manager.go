package mqtt

import (
	"github.com/go-skynet/LocalAI/core/services"
	"github.com/go-skynet/LocalAI/pkg/datamodel"
	"github.com/go-skynet/LocalAI/pkg/model"
)

// PLACEHOLDER DURING PART 1 OF THE REFACTOR

type MQTTManager struct {
	configLoader   *services.ConfigLoader
	modelLoader    *model.ModelLoader
	startupOptions *datamodel.StartupOptions
}

func NewMQTTManager(cl *services.ConfigLoader, ml *model.ModelLoader, options *datamodel.StartupOptions) (*MQTTManager, error) {

	return &MQTTManager{
		configLoader:   cl,
		modelLoader:    ml,
		startupOptions: options,
	}, nil
}
