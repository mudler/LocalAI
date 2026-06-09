package chat

import (
	"context"
	"io"
	"strings"
)

type Options struct {
	Model   string
	BaseURL string
	APIKey  string
	In      io.Reader
	Out     io.Writer
}

func Run(ctx context.Context, opts Options) error {
	if opts.In == nil {
		opts.In = strings.NewReader("")
	}
	if opts.Out == nil {
		opts.Out = io.Discard
	}

	session, err := newChatSession(ctx, newLocalAIChatClient(opts.BaseURL, opts.APIKey), opts.Model)
	if err != nil {
		return err
	}
	return runTerminalChat(ctx, session, opts.In, opts.Out)
}
