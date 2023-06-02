package apiv2

import "net/http"

func NewLocalAINetHTTPServer(configManager *ConfigManager) *LocalAIServer {
	localAI := LocalAIServer{
		configManager: configManager,
	}

	var middlewares []StrictMiddlewareFunc

	http.Handle("/", Handler(NewStrictHandler(&localAI, middlewares)))

	http.ListenAndServe(":8085", nil)
	return &localAI
}
