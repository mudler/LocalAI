package apiv2

import "net/http"

func NewLocalAINetHTTPServer(configManager *ConfigManager) *LocalAIServer {
	localAI := LocalAIServer{
		configManager: configManager,
	}

	http.Handle("/", Handler(&localAI))

	http.ListenAndServe(":8085", nil)
	return &localAI
}
