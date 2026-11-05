// SPDX-License-Identifier: MIT
package connector_test

import (
	"context"
	"errors"
	"fmt"
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
	account string
	sendErr []error
	editErr error
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
	if s.editErr != nil {
		return s.editErr
	}
	return s.err
}
func (s *script) Typing(context.Context, contract.ConnectorRequest) error { s.typed++; return s.err }
func (s *script) Me(context.Context, string) (connector.Bot, error) {
	return connector.Bot{ID: "1", Username: "bot", AccountID: s.account}, s.err
}

// shouting formats.
type shouting struct{ script }

func (*shouting) Format(md string) string { return strings.ToUpper(md) }

// rich sends answers in its markup and shows drafts.
type rich struct {
	script
	richSent, richEdited, drafts []contract.ConnectorRequest
}

func (r *rich) SendRich(_ context.Context, req contract.ConnectorRequest) (string, error) {
	r.richSent = append(r.richSent, req)
	return "r" + req.Text[:1], nil
}
func (r *rich) EditRich(_ context.Context, req contract.ConnectorRequest) error {
	r.richEdited = append(r.richEdited, req)
	return nil
}
func (r *rich) Draft(_ context.Context, req contract.ConnectorRequest) error {
	r.drafts = append(r.drafts, req)
	return r.err
}

// reading also reads the history of a thread.
type reading struct {
	script
	msgs []contract.ConnectorHistoryMessage
	more bool
	got  []contract.ConnectorRequest
}

func (r *reading) History(_ context.Context, req contract.ConnectorRequest) ([]contract.ConnectorHistoryMessage, bool, error) {
	r.got = append(r.got, req)
	return r.msgs, r.more, r.err
}

var _ = Describe("Serve", func() {
	ctx := context.Background()
	req := func(op string) contract.ConnectorRequest {
		r := contract.ConnectorRequest{Op: op, Kind: "chat", Token: "t", Chat: "c", Text: "alpha beta gamma"}
		if op == "edit" {
			r.MessageID = "9"
		}
		return r
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

	It("formats before it cuts", func() {
		s := &shouting{}
		_, err := connector.Serve(ctx, s, req("send"))
		Expect(err).NotTo(HaveOccurred())
		Expect(s.sent[0].Text).To(Equal("ALPHA"))
	})

	It("refuses the ops media and send_file without a handle", func() {
		for _, op := range []string{"media", "send_file"} {
			r := req(op)
			r.Media = "F1"
			_, err := connector.Serve(ctx, &streaming{}, r)
			Expect(err).To(MatchError(contract.ErrRefused), op)
		}
	})

	It("answers who the bot is and types", func() {
		s := &script{}
		out, err := connector.Serve(ctx, s, req("me"))
		Expect(err).NotTo(HaveOccurred())
		Expect(out.BotUsername).To(Equal("bot"))
		Expect(out.AccountID).To(BeEmpty(), "a platform that cannot tell leaves it empty")
		_, err = connector.Serve(ctx, s, req("typing"))
		Expect(err).NotTo(HaveOccurred())
		Expect(s.typed).To(Equal(1))
	})

	It("passes the account of the token, and drops one that is too long", func() {
		out, err := connector.Serve(ctx, &script{account: "T1/U1"}, req("me"))
		Expect(err).NotTo(HaveOccurred())
		Expect(out.AccountID).To(Equal("T1/U1"))
		out, err = connector.Serve(ctx, &script{account: strings.Repeat("x", contract.MaxAccountIDBytes+1)}, req("me"))
		Expect(err).NotTo(HaveOccurred())
		Expect(out.AccountID).To(BeEmpty())
	})

	DescribeTable("types the errors of a platform",
		func(err error, code string) {
			out, rerr := connector.Serve(ctx, &script{err: err}, req("me"))
			Expect(rerr).NotTo(HaveOccurred())
			Expect(out.Error.Code).To(Equal(code))
		},
		Entry("a refused token", connector.ErrUnauthorized, contract.ConnectorUnauthorized),
		Entry("another reader", connector.ErrConflict, contract.ConnectorConflict),
		Entry("a file too large", fmt.Errorf("telegram: %w", connector.ErrTooLarge), contract.ConnectorTooLarge),
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

	It("replaces the working message with the first part of a send and sends the rest", func() {
		s := &script{}
		r := req("send")
		r.MessageID = "9"
		out, err := connector.Serve(ctx, s, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.edited).To(HaveLen(1))
		Expect(s.edited[0].MessageID).To(Equal("9"))
		Expect(s.edited[0].Text).To(Equal("alpha"))
		Expect(s.sent).To(HaveLen(1))
		Expect(s.sent[0].Text).To(Equal("beta gamma"))
		Expect(s.sent[0].MessageID).To(BeEmpty())
		Expect(out.MessageID).To(Equal("mb"))
	})

	It("sends the first part as a new message when the working message cannot be edited", func() {
		s := &script{editErr: errors.New("message to edit not found")}
		r := req("send")
		r.MessageID = "9"
		out, err := connector.Serve(ctx, s, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Error).To(BeNil())
		Expect(s.sent).To(HaveLen(2))
		Expect(s.sent[0].Text).To(Equal("alpha"))
	})

	It("answers an edit that the platform rate-limits at once, without waiting", func() {
		s := &script{editErr: &connector.RetryAfter{Seconds: 5}}
		start := time.Now()
		out, err := connector.Serve(ctx, s, req("edit"))
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Error.Code).To(Equal(contract.ConnectorRateLimited))
		Expect(time.Since(start)).To(BeNumerically("<", time.Second))
	})

	It("sends an answer through the markup of a rich platform, and a preview edit as plain text", func() {
		p := &rich{}
		r := req("send")
		r.MessageID = "9"
		out, err := connector.Serve(ctx, p, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.MessageID).To(Equal("rb"))
		Expect(p.richEdited).To(HaveLen(1))
		Expect(p.richEdited[0].MessageID).To(Equal("9"))
		Expect(p.richSent).To(HaveLen(1))
		Expect(p.sent).To(BeEmpty(), "no part of an answer goes as plain text")
		_, err = connector.Serve(ctx, p, req("edit"))
		Expect(err).NotTo(HaveOccurred())
		Expect(p.edited).To(HaveLen(1), "an edit is a preview")
		Expect(p.richEdited).To(HaveLen(1))
	})

	It("shows a draft only on a platform that can, with a draft ID and what fits", func() {
		p := &rich{}
		r := req("draft")
		r.DraftID = 7
		_, err := connector.Serve(ctx, p, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(p.drafts).To(HaveLen(1))
		Expect(p.drafts[0].Text).To(Equal("alpha"))
		Expect(p.drafts[0].DraftID).To(Equal(int64(7)))
		_, err = connector.Serve(ctx, &script{}, r)
		Expect(err).To(MatchError(contract.ErrRefused), "a platform without drafts")
		r.DraftID = 0
		_, err = connector.Serve(ctx, p, r)
		Expect(err).To(MatchError(contract.ErrRefused), "a draft without an ID")
		r.DraftID, p.err = 7, &connector.RetryAfter{Seconds: 5}
		start := time.Now()
		out, err := connector.Serve(ctx, p, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Error.Code).To(Equal(contract.ConnectorRateLimited), "a draft is not retried after a wait")
		Expect(time.Since(start)).To(BeNumerically("<", time.Second))
	})

	Describe("the op history", func() {
		hist := func() contract.ConnectorRequest {
			r := req("history")
			r.Thread, r.Limit, r.Before = "t1", 2, "9"
			return r
		}
		It("is refused on a platform that cannot read a thread, and without a thread", func() {
			_, err := connector.Serve(ctx, &script{}, hist())
			Expect(err).To(MatchError(contract.ErrRefused))
			r := hist()
			r.Thread = ""
			_, err = connector.Serve(ctx, &reading{}, r)
			Expect(err).To(MatchError(contract.ErrRefused))
		})
		It("returns the newest messages within the limit and says that older ones exist", func() {
			s := &reading{msgs: []contract.ConnectorHistoryMessage{{Text: "a"}, {Text: "b"}, {Text: "c"}}}
			out, err := connector.Serve(ctx, s, hist())
			Expect(err).NotTo(HaveOccurred())
			Expect(out.Messages).To(HaveLen(2))
			Expect(out.Messages[0].Text).To(Equal("b"))
			Expect(out.Truncated).To(BeTrue())
			Expect(s.got[0].Before).To(Equal("9"))
		})
		It("uses the default limit when the host asks for none or too many", func() {
			s := &reading{}
			r := hist()
			r.Limit = contract.MaxHistoryMessages + 1
			_, err := connector.Serve(ctx, s, r)
			Expect(err).NotTo(HaveOccurred())
			Expect(s.got[0].Limit).To(Equal(contract.DefaultHistoryMessages))
		})
		It("types the error of the platform and does not wait for a rate limit", func() {
			s := &reading{script: script{err: &connector.RetryAfter{Seconds: 30}}}
			out, err := connector.Serve(ctx, s, hist())
			Expect(err).NotTo(HaveOccurred())
			Expect(out.Error.Code).To(Equal(contract.ConnectorRateLimited))
			Expect(s.got).To(HaveLen(1))
		})
	})
})

// deleting is a Platform that can delete a message.
type deleting struct {
	script
	deleted []contract.ConnectorRequest
	delErr  error
}

func (d *deleting) Delete(_ context.Context, req contract.ConnectorRequest) error {
	d.deleted = append(d.deleted, req)
	return d.delErr
}

var _ = Describe("deleting the preview of an answer", func() {
	ctx := context.Background()
	req := func(op string) contract.ConnectorRequest {
		return contract.ConnectorRequest{Op: op, Kind: "chat", Token: "t", Chat: "c", Text: "alpha beta gamma"}
	}

	BeforeEach(func() {
		old := connector.Sleep
		connector.Sleep = func(context.Context, time.Duration) error { return nil }
		DeferCleanup(func() { connector.Sleep = old })
	})

	It("deletes the message of the op delete, and refuses it without a message or a platform that can", func() {
		d := &deleting{}
		r := req("delete")
		r.MessageID = "9"
		out, err := connector.Serve(ctx, d, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Error).To(BeNil())
		Expect(d.deleted).To(HaveLen(1))
		_, err = connector.Serve(ctx, d, req("delete"))
		Expect(err).To(MatchError(contract.ErrRefused))
		r.MessageID = "9"
		_, err = connector.Serve(ctx, &script{}, r)
		Expect(err).To(MatchError(contract.ErrRefused))
	})

	It("deletes the preview after it sends the answer in its place", func() {
		d := &deleting{script: script{editErr: errors.New("message to edit not found")}}
		r := req("send")
		r.MessageID = "9"
		out, err := connector.Serve(ctx, d, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Error).To(BeNil())
		Expect(d.sent).To(HaveLen(2))
		Expect(d.deleted).To(HaveLen(1))
		Expect(d.deleted[0].MessageID).To(Equal("9"))
	})

	It("tries an edit again after each wait, up to a bound, before it falls back", func() {
		var waits []time.Duration
		connector.Sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
		d := &deleting{script: script{editErr: &connector.RetryAfter{Seconds: 2}}}
		r := req("send")
		r.MessageID = "9"
		_, err := connector.Serve(ctx, d, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(d.edited).To(HaveLen(3))
		Expect(waits).To(Equal([]time.Duration{2 * time.Second, 2 * time.Second}))
		Expect(d.deleted).To(HaveLen(1))
	})

	It("keeps the answer when the platform cannot delete the preview", func() {
		d := &deleting{script: script{editErr: errors.New("gone")}, delErr: errors.New("no")}
		r := req("send")
		r.MessageID = "9"
		out, err := connector.Serve(ctx, d, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Error).To(BeNil())
		Expect(d.sent).To(HaveLen(2))
	})
})
