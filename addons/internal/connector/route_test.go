// SPDX-License-Identifier: MIT
package connector_test

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// script is a Platform whose answers a test sets.
type script struct {
	sent    []contract.ConnectorRequest
	edited  []contract.ConnectorRequest
	typed   int
	ups     []contract.ConnectorMessage
	err     error
	sendErr []error
}

func (*script) Kind() string  { return "chat" }
func (*script) MaxRunes() int { return 10 }
func (s *script) Poll(_ context.Context, _ string, _ int64, wait int) ([]contract.ConnectorMessage, error) {
	if wait > contract.MaxPollSeconds {
		return nil, errors.New("the wait was not capped")
	}
	return s.ups, s.err
}
func (s *script) Send(_ context.Context, req contract.ConnectorRequest) (string, error) {
	if len(s.sendErr) > 0 {
		err := s.sendErr[0]
		s.sendErr = s.sendErr[1:]
		if err != nil {
			return "", err
		}
	}
	s.sent = append(s.sent, req)
	return "m" + req.Text[:1], nil
}
func (s *script) Edit(_ context.Context, req contract.ConnectorRequest) error {
	s.edited = append(s.edited, req)
	return s.err
}
func (s *script) Typing(context.Context, contract.ConnectorRequest) error { s.typed++; return s.err }
func (s *script) Me(context.Context, string) (connector.Bot, error) {
	return connector.Bot{ID: "1", Username: "bot"}, s.err
}

// shouting formats and reads media.
type shouting struct{ script }

func (*shouting) Format(md string) string { return strings.ToUpper(md) }
func (*shouting) Media(_ context.Context, _, ref string) (contract.ConnectorFile, error) {
	return contract.ConnectorFile{Name: ref, Data: []byte("x")}, nil
}

var _ = Describe("Serve", func() {
	ctx := context.Background()
	req := func(op string) contract.ConnectorRequest {
		return contract.ConnectorRequest{Op: op, Kind: "chat", Token: "t", Chat: "c", Text: "alpha beta gamma", MessageID: "9"}
	}

	It("polls with a capped wait and names the next offset", func() {
		s := &script{ups: []contract.ConnectorMessage{{Cursor: 4}, {Cursor: 7, Skip: true}}}
		r := req("poll")
		r.Offset, r.TimeoutSeconds = 2, 999
		out, err := connector.Serve(ctx, s, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.NextOffset).To(Equal(int64(8)))
	})

	It("sends the parts of a text in order and edits with what fits", func() {
		s := &script{}
		out, err := connector.Serve(ctx, s, req("send"))
		Expect(err).NotTo(HaveOccurred())
		Expect(s.sent).To(HaveLen(2))
		Expect(out.MessageID).To(Equal("mb"))
		out, err = connector.Serve(ctx, s, req("edit"))
		Expect(err).NotTo(HaveOccurred())
		Expect(s.edited[0].Text).To(Equal("alpha"))
		Expect(out.MessageID).To(Equal("9"))
		r := req("edit")
		r.Text = " "
		_, err = connector.Serve(ctx, s, r)
		Expect(err).To(MatchError(contract.ErrRefused))
	})

	It("formats before it cuts, and reads media only on a platform that can", func() {
		s := &shouting{}
		_, err := connector.Serve(ctx, s, req("send"))
		Expect(err).NotTo(HaveOccurred())
		Expect(s.sent[0].Text).To(Equal("ALPHA"))
		r := req("media")
		r.Media = "F1"
		out, err := connector.Serve(ctx, s, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.File.Name).To(Equal("F1"))
		_, err = connector.Serve(ctx, &script{}, r)
		Expect(err).To(MatchError(contract.ErrRefused))
	})

	It("answers who the bot is and types", func() {
		s := &script{}
		out, err := connector.Serve(ctx, s, req("me"))
		Expect(err).NotTo(HaveOccurred())
		Expect(out.BotUsername).To(Equal("bot"))
		_, err = connector.Serve(ctx, s, req("typing"))
		Expect(err).NotTo(HaveOccurred())
		Expect(s.typed).To(Equal(1))
	})

	DescribeTable("types the errors of a platform",
		func(err error, code string) {
			out, rerr := connector.Serve(ctx, &script{err: err}, req("me"))
			Expect(rerr).NotTo(HaveOccurred())
			Expect(out.Error.Code).To(Equal(code))
		},
		Entry("a refused token", connector.ErrUnauthorized, contract.ConnectorUnauthorized),
		Entry("another reader", connector.ErrConflict, contract.ConnectorConflict),
		Entry("a wait", &connector.RetryAfter{Seconds: 3}, contract.ConnectorRateLimited),
		Entry("anything else", errors.New("boom"), contract.ConnectorFailed),
	)

	It("waits once when the platform asks, and gives up on a second wait or a cancel", func() {
		s := &script{sendErr: []error{&connector.RetryAfter{Seconds: 1}, nil}}
		r := req("send")
		r.Text = "short"
		start := time.Now()
		out, err := connector.Serve(ctx, s, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Error).To(BeNil())
		Expect(time.Since(start)).To(BeNumerically(">=", 900*time.Millisecond))
		s = &script{sendErr: []error{&connector.RetryAfter{Seconds: 1}, &connector.RetryAfter{Seconds: 2}}}
		out, _ = connector.Serve(ctx, s, r)
		Expect(out.Error.RetryAfter).To(Equal(2))
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		Expect(connector.Retry(cctx, func() error { return &connector.RetryAfter{Seconds: 5} })).To(MatchError(context.Canceled))
	})

	It("refuses another kind, no token and an unknown op", func() {
		for _, r := range []contract.ConnectorRequest{{Op: "poll", Kind: "x", Token: "t"}, {Op: "poll", Kind: "chat"}, {Op: "nope", Kind: "chat", Token: "t"}} {
			_, err := connector.Serve(ctx, &script{}, r)
			Expect(err).To(MatchError(contract.ErrRefused))
		}
	})
})
