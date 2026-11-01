// SPDX-License-Identifier: MIT
package transport_test

import (
	"time"

	"github.com/mudler/LocalAI/addons/slack/slacktest"
	"github.com/mudler/LocalAI/addons/slack/transport"
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
