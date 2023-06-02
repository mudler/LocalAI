package apiv2

import (
	"net/http"

	"github.com/go-skynet/LocalAI/pkg/model"
)

func NewLocalAINetHTTPServer(configManager *ConfigManager, loader *model.ModelLoader, address string) *LocalAIServer {
	localAI := LocalAIServer{
		configManager: configManager,
		loader:        loader,
	}

	var middlewares []StrictMiddlewareFunc

	http.Handle("/", Handler(NewStrictHandler(&localAI, middlewares)))

	http.ListenAndServe(address, nil)
	return &localAI
}
