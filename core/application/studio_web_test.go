package application

import (
	"context"
	"encoding/json"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/web/safehttp"
	"github.com/mudler/LocalAI/pkg/web/webtools"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the chats' built-in web tools", func() {
	It("follows the runtime settings at each call", func() {
		cfg := &config.ApplicationConfig{}
		w := newStudioWeb(cfg)
		Expect(w.Enabled(webtools.SearchName)).To(BeTrue())
		Expect(w.Enabled(webtools.FetchName)).To(BeTrue())
		Expect(w.Enabled("bash")).To(BeFalse())

		cfg.DisableChatsWebFetch = true
		Expect(w.Enabled(webtools.FetchName)).To(BeFalse())
		_, err := w.Fetch(context.Background(), json.RawMessage(`{"url":"https://example.com/"}`))
		Expect(err).To(MatchError(ContainSubstring("switched off")))
	})

	It("searches with DuckDuckGo by default and with SearXNG when one is set", func() {
		cfg := &config.ApplicationConfig{}
		w := newStudioWeb(cfg)
		Expect(w.current().Search.Provider().Name()).To(Equal("duckduckgo"))
		cfg.ChatsWebSearXNGURL = "https://searx.example.org"
		Expect(w.current().Search.Provider().Name()).To(Equal("searxng"))
		Expect(w.current().Search.Provider().Host()).To(Equal("searx.example.org"))
	})

	It("refuses private addresses unless allowed, and keeps the ports of the setting", func() {
		cfg := &config.ApplicationConfig{}
		w := newStudioWeb(cfg)
		_, err := w.Fetch(context.Background(), json.RawMessage(`{"url":"http://127.0.0.1/"}`))
		Expect(err).To(MatchError(ContainSubstring("private network")))
		_, err = w.Fetch(context.Background(), json.RawMessage(`{"url":"http://example.com:8080/"}`))
		Expect(err).To(MatchError(ContainSubstring("port")))

		cfg.ChatsWebPorts = "80,443,8080"
		cfg.ChatsWebAllowPrivate = true
		c, ok := w.current().Fetch.(*safehttp.Client)
		Expect(ok).To(BeTrue())
		_, err = c.CheckURL("http://example.com:8080/")
		Expect(err).NotTo(HaveOccurred())
		_, err = c.CheckURL("http://10.0.0.1/")
		Expect(err).NotTo(HaveOccurred())
		_, err = c.CheckURL("http://169.254.169.254/")
		Expect(err).To(MatchError(safehttp.ErrBlockedAddress))
	})
})
