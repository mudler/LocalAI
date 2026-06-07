package cli

import (
	"context"
	"os"

	chatcli "github.com/mudler/LocalAI/core/cli/chat"
	cliContext "github.com/mudler/LocalAI/core/cli/context"
)

type ChatCMD struct {
	Model    string `short:"m" help:"Model name to use. Defaults to the only model returned by the server when exactly one is available"`
	Endpoint string `env:"LOCALAI_CHAT_ENDPOINT" default:"http://127.0.0.1:8080" help:"LocalAI server endpoint. The /v1 path is added automatically when omitted"`
	APIKey   string `env:"LOCALAI_API_KEY,API_KEY" help:"API key to use when the LocalAI server requires authentication"`
}

func (c *ChatCMD) Run(ctx *cliContext.Context) error {
	return chatcli.Run(context.Background(), chatcli.Options{
		Model:   c.Model,
		BaseURL: chatAPIBaseURL(c.Endpoint),
		APIKey:  c.APIKey,
		In:      os.Stdin,
		Out:     os.Stdout,
	})
}
