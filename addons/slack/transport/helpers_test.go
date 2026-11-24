// SPDX-License-Identifier: MIT
package transport_test

import (
	"bytes"
	"context"
	"time"

	"github.com/mudler/LocalAI/addons/slack/slacktest"
	"github.com/mudler/LocalAI/addons/slack/transport"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
)

const (
	botToken = "xoxb-SECRET-bot"
	appToken = "xapp-SECRET-app"
	secret   = botToken + " " + appToken
)

// fake starts a fake Slack with one app and one person.
func fake() *slacktest.Server {
	f := slacktest.New()
	DeferCleanup(f.Close)
	f.AddApp(slacktest.App{Bot: botToken, AppToken: appToken, UserID: "UBOT", User: "helper", Team: "Acme"})
	f.AddUser("U1", "ada", "Ada Lovelace")
	return f
}

func apiOf(f *slacktest.Server) *transport.API {
	return &transport.API{BaseURL: f.URL, FilesHost: f.FilesHost}
}

// runtime returns a runtime over the fake, closed when the test ends.
func runtimeOf(f *slacktest.Server) *transport.Runtime {
	r := transport.New(apiOf(f))
	r.Socket.IdleAfter = time.Minute
	DeferCleanup(r.Close)
	return r
}

// memHost is the host side of a file handle in memory: it keeps the chunks that
// the add-on puts, and serves the bytes of src to its gets.
type memHost struct {
	got bytes.Buffer
	src []byte
}

func (h *memHost) Do(_ context.Context, _, _ string, args, out any) error {
	switch a := args.(type) {
	case contract.ConnectorFilePut:
		if a.Offset == int64(h.got.Len()) {
			h.got.Write(a.Data)
		}
		*(out.(*contract.ConnectorFilePutResult)) = contract.ConnectorFilePutResult{Received: int64(h.got.Len())}
	case contract.ConnectorFileGet:
		end := min(a.Offset+a.Length, int64(len(h.src)))
		*(out.(*contract.ConnectorFileGetResult)) = contract.ConnectorFileGetResult{Data: h.src[a.Offset:end], EOF: end == int64(len(h.src))}
	}
	return nil
}
