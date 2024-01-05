package mqtt

import (
	"github.com/go-skynet/LocalAI/core/services"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/schema"
)

// PLACEHOLDER DURING PART 1 OF THE REFACTOR

type MQTTManager struct {
	configLoader   *services.ConfigLoader
	modelLoader    *model.ModelLoader
	startupOptions *schema.StartupOptions
}

func NewMQTTManager(cl *services.ConfigLoader, ml *model.ModelLoader, options *schema.StartupOptions) (*MQTTManager, error) {

	return &MQTTManager{
		configLoader:   cl,
		modelLoader:    ml,
		startupOptions: options,
	}, nil
}
