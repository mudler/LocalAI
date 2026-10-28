// SPDX-License-Identifier: MIT
package transport_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/addons/slack/transport"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the Web API client", func() {
	ctx := context.Background()

	It("names the bot, posts, edits and reads people and files", func() {
		f := fake()
		api := apiOf(f)
		id, err := api.AuthTest(ctx, botToken)
		Expect(err).NotTo(HaveOccurred())
		Expect([]string{id.UserID, id.User, id.Team}).To(Equal([]string{"UBOT", "helper", "Acme"}))
		ts, err := api.PostMessage(ctx, botToken, "C1", "1.1", "hi")
		Expect(err).NotTo(HaveOccurred())
		Expect(api.Update(ctx, botToken, "C1", ts, "edited")).To(Succeed())
		posts := f.Posts(botToken)
		Expect(posts).To(HaveLen(2))
		Expect(posts[0]).To(MatchPost("C1", "1.1", "hi", false))
		Expect(posts[1].TS).To(Equal(ts))
		Expect(posts[1].Edited).To(BeTrue())
		u, err := api.UserInfo(ctx, botToken, "U1")
		Expect(err).NotTo(HaveOccurred())
		Expect([]string{u.Name, u.Display()}).To(Equal([]string{"ada", "Ada Lovelace"}))
		f.AddFile("F1", "notes.txt", "text/plain", []byte("file body"))
		fi, err := api.FileInfo(ctx, botToken, "F1")
		Expect(err).NotTo(HaveOccurred())
		data, err := api.Download(ctx, botToken, fi.Download, 100)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("file body"))
		_, err = api.Download(ctx, botToken, fi.Download, 3)
		Expect(err).To(MatchError(transport.ErrTooLarge))
	})

	It("types a refused token, a wait and any other error, and never repeats the token", func() {
		f := fake()
		api := apiOf(f)
		_, err := api.AuthTest(ctx, "xoxb-SECRET-unknown")
		Expect(errors.Is(err, connector.ErrUnauthorized)).To(BeTrue())
		Expect(err.Error()).NotTo(ContainSubstring("SECRET"))
		f.RateLimitOnce("chat.postMessage", 7)
		_, err = api.PostMessage(ctx, botToken, "C1", "", "x")
		var ra *connector.RetryAfter
		Expect(errors.As(err, &ra)).To(BeTrue())
		Expect(ra.Seconds).To(Equal(7))
		_, err = api.UserInfo(ctx, botToken, "U404")
		Expect(err).To(MatchError(ContainSubstring("user_not_found")))
		_, err = api.OpenConnection(ctx, botToken)
		Expect(errors.Is(err, connector.ErrUnauthorized)).To(BeTrue(), "a bot token cannot open a link")
		dead := &transport.API{BaseURL: "http://127.0.0.1:1"}
		_, err = dead.AuthTest(ctx, botToken)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("SECRET"))
	})

	It("downloads only from the files host, and refuses the sign-in page", func() {
		f := fake()
		api := apiOf(f)
		for _, bad := range []string{"https://evil.example/files/F1", "ftp://" + f.FilesHost + "/files/F1", "http://user@" + f.FilesHost + "/files/F1"} {
			_, err := api.Download(ctx, botToken, bad, 10)
			Expect(err).To(MatchError(ContainSubstring("files host")), bad)
		}
		_, err := api.Download(ctx, appToken, "http://"+f.FilesHost+"/files/F9", 100)
		Expect(err).To(MatchError(ContainSubstring("files:read")))
		_, err = api.Download(ctx, botToken, "http://"+f.FilesHost+"/files/F9", 100)
		Expect(err).To(MatchError(ContainSubstring("404")))
		real := &transport.API{}
		_, err = real.Download(ctx, botToken, "http://files.slack.com/x", 10)
		Expect(err).To(MatchError(ContainSubstring("files host")), "plain http is refused for the real host")
	})

	It("reads a non-JSON answer and a 429 without Retry-After", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/auth.test" {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			if r.URL.Path == "/api/apps.connections.open" {
				_, _ = w.Write([]byte(`{"ok":true,"url":"https://not-a-socket"}`))
				return
			}
			_, _ = w.Write([]byte("<html>"))
		}))
		DeferCleanup(srv.Close)
		api := &transport.API{BaseURL: srv.URL + "/api"}
		_, err := api.AuthTest(ctx, botToken)
		var ra *connector.RetryAfter
		Expect(errors.As(err, &ra)).To(BeTrue())
		Expect(ra.Seconds).To(Equal(1))
		_, err = api.UserInfo(ctx, botToken, "U1")
		Expect(err).To(MatchError(ContainSubstring("not JSON")))
		_, err = api.OpenConnection(ctx, appToken)
		Expect(err).To(MatchError(ContainSubstring("no Socket Mode address")))
	})
})
