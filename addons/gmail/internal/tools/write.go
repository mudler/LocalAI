// SPDX-License-Identifier: MIT
package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/gmail/internal/gmailapi"
	"github.com/mudler/LocalAI/addons/internal/argutil"
	"github.com/mudler/LocalAI/addons/internal/writeflow"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The write tools of the manifest. Both are write tools: the host decides,
// from the write mode of the person's binding, whether each one is refused or
// stored as a proposal for a person, and the manifest sets approval always.
// Mail is sent in two steps a person sees: create-draft saves a draft, and
// send-draft sends a draft that exists, after the person read its recipients
// and text in the approval. The add-on cannot delete, trash, label, forward
// or change settings, and asks for no scope that could.
const (
	ToolCreateDraft = "create-draft"
	ToolSendDraft   = "send-draft"

	previewRunes = 800
	// MaxRecipients bounds To, Cc and Bcc together.
	MaxRecipients = 20
	// MaxWriteBody is the most bytes of text in one draft.
	MaxWriteBody = 64 << 10
	maxSubject   = 200
	maxRefs      = 10
)

// WriteTools lists the write tools.
func WriteTools() []string { return []string{ToolCreateDraft, ToolSendDraft} }

func isWrite(tool string) bool { return slices.Contains(WriteTools(), tool) }

type writeArgs struct {
	Label            string   `json:"label"`
	To               []string `json:"to"`
	Cc               []string `json:"cc"`
	Subject          string   `json:"subject"`
	Body             *string  `json:"body"`
	ReplyToMessageID string   `json:"replyToMessageId"`
	DraftID          string   `json:"draftId"`
}

type write struct {
	tool string
	writeArgs
}

// requireScope refuses a write when the login does not hold the optional
// scope. The sentence names the scope, not the service's own refusal.
func (c *call) requireScope() error {
	if !c.granted(ScopeWrite) {
		return bad("writing needs the %s scope. Reconnect and allow it", ScopeWriteName)
	}
	return nil
}

// addrShape is the shape of the address of a recipient that the add-on
// builds a header from: plain ASCII, no name, no angle brackets.
var addrShape = regexp.MustCompile(`^[A-Za-z0-9.!#$%&'*+/=?^_{|}~-]{1,64}@[A-Za-z0-9.-]{1,255}$`)

// checkAddress accepts one bare address. A header is built only from a value
// that passes: no line break, no comma, no name, no second address.
func checkAddress(a string) error {
	if len(a) > 254 || !addrShape.MatchString(a) {
		return bad("a recipient is not a plain e-mail address")
	}
	p, err := mail.ParseAddress(a)
	if err != nil || p.Address != a || p.Name != "" {
		return bad("a recipient is not a plain e-mail address")
	}
	return nil
}

func checkRecipients(to, cc []string, needTo bool) error {
	if needTo && len(to) == 0 {
		return bad("to needs at least one address")
	}
	if len(to)+len(cc) > MaxRecipients {
		return bad("at most %d recipients", MaxRecipients)
	}
	for _, a := range slices.Concat(to, cc) {
		if err := checkAddress(a); err != nil {
			return err
		}
	}
	return nil
}

func checkBody(s *string, token string) error {
	if s == nil || strings.TrimSpace(*s) == "" {
		return bad("body is required")
	}
	if len(*s) > MaxWriteBody || !argutil.Plain(*s) {
		return bad("body is not text or is longer than %d bytes", MaxWriteBody)
	}
	if token != "" && strings.Contains(*s, token) {
		return bad("body looks like a secret so it was not sent")
	}
	return nil
}

func (c *call) parseWrite(tool string, raw map[string]json.RawMessage) (write, error) {
	var a writeArgs
	w := write{tool: tool}
	if err := argutil.DecodeArgs(raw, &a); err != nil {
		return w, err
	}
	w.writeArgs = a
	if err := c.checkLabel(a.Label); err != nil {
		return w, err
	}
	switch tool {
	case ToolCreateDraft:
		if a.DraftID != "" {
			return w, bad("draftId is not an argument of this tool")
		}
		if err := checkRecipients(a.To, a.Cc, true); err != nil {
			return w, err
		}
		if err := argutil.CheckLine("subject", a.Subject, maxSubject, a.ReplyToMessageID == ""); err != nil {
			return w, err
		}
		if a.ReplyToMessageID != "" {
			if err := checkID("replyToMessageId", a.ReplyToMessageID); err != nil {
				return w, err
			}
		}
		return w, checkBody(a.Body, c.token)
	case ToolSendDraft:
		if len(a.To)+len(a.Cc) > 0 || a.Subject != "" || a.Body != nil || a.ReplyToMessageID != "" {
			return w, bad("send-draft takes only draftId")
		}
		return w, checkID("draftId", a.DraftID)
	}
	return w, bad("unknown tool")
}

// target is what a write works on, as it is now.
type target struct {
	// reply is the message a draft answers.
	reply *gMessage
	// draft is the draft a send sends.
	draft *gDraft
	// text is the text of the draft, as the person will read it.
	text body
}

type gDraft struct {
	ID      string   `json:"id"`
	Message gMessage `json:"message"`
}

var msgIDShape = regexp.MustCompile(`^<[^<>\s]{1,300}>$`)

// inspect reads the target of a write and checks the tool fits it. It
// returns the revision the preview records: a small hash of what makes the
// approval wrong if it changed. It is how a stale approval is noticed.
func (c *call) inspect(w write) (target, uint64, error) {
	var t target
	if err := c.requireScope(); err != nil {
		return t, 0, err
	}
	switch w.tool {
	case ToolCreateDraft:
		rev := []string{"create", strings.Join(w.To, ","), strings.Join(w.Cc, ","), w.Subject, *w.Body, w.ReplyToMessageID}
		if w.ReplyToMessageID != "" {
			m, err := c.fetchMessage(w.ReplyToMessageID, "metadata", "Message-ID", "References", "Subject", "From", "Reply-To")
			if err != nil {
				return t, 0, err
			}
			if !msgIDShape.MatchString(strings.TrimSpace(m.Payload.header("Message-ID"))) {
				return t, 0, bad("the message has no usable Message-ID to answer")
			}
			t.reply = m
			rev = append(rev, m.ThreadID, m.Payload.header("Message-ID"))
		}
		return t, argutil.Sum64(rev...), nil
	case ToolSendDraft:
		var d gDraft
		if err := c.r.API.GetJSON(c.ctx, c.token, "/gmail/v1/users/me/drafts/"+argutil.IDPath(w.DraftID), url.Values{"format": {"full"}}, &d); err != nil {
			return t, 0, err
		}
		t.draft = &d
		t.text = parseBody(d.Message.Payload)
		n := 0
		for _, h := range []string{"To", "Cc", "Bcc"} {
			if v := d.Message.Payload.header(h); strings.TrimSpace(v) != "" {
				list, err := mail.ParseAddressList(v)
				if err != nil {
					return t, 0, bad("a recipient of the draft cannot be read")
				}
				n += len(list)
			}
		}
		if n == 0 {
			return t, 0, bad("the draft has no recipient")
		}
		if n > MaxRecipients {
			return t, 0, bad("the draft has more than %d recipients", MaxRecipients)
		}
		h := d.Message.Payload
		names := make([]string, 0, len(t.text.Attachments))
		for _, a := range t.text.Attachments {
			names = append(names, a.Filename)
		}
		return t, argutil.Sum64("send", d.ID, d.Message.ID, h.header("To"), h.header("Cc"), h.header("Bcc"), h.header("Subject"), t.text.Text, strings.Join(names, ",")), nil
	}
	return t, 0, bad("unknown tool")
}

// sample is what the person sees before a change: facts built from the
// arguments and the target, never from model prose. It stays small, since a
// chat keeps only a short tool result; the full arguments are in the proposal.
func sample(w write, t target) map[string]any {
	s := map[string]any{"tool": w.tool, "label": w.Label}
	switch w.tool {
	case ToolCreateDraft:
		s["action"] = "Save a draft"
		s["notice"] = "This saves a draft in the person's Gmail. It does not send it."
		s["to"], s["cc"], s["subject"] = w.To, w.Cc, w.Subject
		if t.reply != nil {
			s["replyToMessageId"] = t.reply.ID
			s["replyToFrom"] = headerText(t.reply.Payload.header("From"), 120)
			s["replyToSubject"] = headerText(t.reply.Payload.header("Subject"), 120)
			if w.Subject == "" {
				s["subject"] = replySubject(headerText(t.reply.Payload.header("Subject"), maxSubject))
			}
		}
		b, cutB := argutil.Runes(*w.Body, previewRunes)
		s["body"], s["bodyBytes"] = b, len(*w.Body)
		if cutB {
			s["bodyCut"] = true
		}
	case ToolSendDraft:
		p := t.draft.Message.Payload
		s["action"] = "Send an e-mail"
		s["notice"] = "This sends an e-mail from the person's Gmail. A sent e-mail cannot be taken back."
		s["draftId"] = t.draft.ID
		s["to"], s["cc"], s["bcc"] = headerText(p.header("To"), 600), headerText(p.header("Cc"), 600), headerText(p.header("Bcc"), 600)
		s["subject"] = headerText(p.header("Subject"), maxSubject)
		b, cutB := argutil.Runes(t.text.Text, previewRunes)
		s["body"], s["bodyBytes"] = b, len(t.text.Text)
		if cutB {
			s["bodyCut"] = true
		}
		if len(t.text.Attachments) > 0 {
			var names []string
			for _, a := range t.text.Attachments {
				names = append(names, a.Filename)
			}
			s["attachments"] = names
		}
	}
	return s
}

func replySubject(orig string) string {
	o := strings.TrimSpace(orig)
	if len(o) >= 3 && strings.EqualFold(o[:3], "re:") {
		return o
	}
	return "Re: " + o
}

// prepare reads the call of a write envelope.
func (r *Runtime) prepare(ctx context.Context, env contract.Envelope) (write, *call, error) {
	tool, raw, err := argutil.SplitArgs(env.Request.Arguments)
	if err != nil || !isWrite(tool) {
		return write{}, nil, bad("unknown tool")
	}
	c := r.newCall(ctx, env)
	if c.token == "" {
		return write{}, nil, bad("no access token for this call")
	}
	// The scope comes first: a model that cannot write learns it from any
	// write tool, before the service is asked anything.
	if err := c.requireScope(); err != nil {
		return write{}, nil, err
	}
	w, err := c.parseWrite(tool, raw)
	return w, c, err
}

// Preview checks a write and describes it. It changes nothing.
func (r *Runtime) Preview(ctx context.Context, env contract.Envelope) (contract.Preview, error) {
	w, c, err := r.prepare(ctx, env)
	if err != nil {
		return contract.Preview{}, err
	}
	t, rev, err := c.inspect(w)
	if err != nil {
		return contract.Preview{}, r.translateWrite(err, false)
	}
	summary := argutil.PlainJSON(map[string]any{"count": 1, "sample": sample(w, t)})
	if len(summary) > 3<<10 {
		// The summary must stay small. Cut the text, which the stored
		// arguments hold whole for a draft; a draft that is sent shows less.
		s := sample(w, t)
		s["body"], _ = argutil.Runes(fmt.Sprint(s["body"]), 300)
		s["bodyCut"] = true
		summary = argutil.PlainJSON(map[string]any{"count": 1, "sample": s})
		if len(summary) > 3<<10 {
			return contract.Preview{}, bad("the preview is too large")
		}
	}
	return contract.Preview{OperationID: env.Request.OperationID, Revision: contract.Revision{Schema: 1, Data: rev},
		ExpiresAt: r.Time().Add(24 * time.Hour), Summary: summary}, nil
}

// errMaybeSent is what a retry of an operation answers when an earlier try
// started and left no receipt. Neither tool can find out whether Gmail took
// the first request, and a second one could save or send twice, so the person
// decides.
var errMaybeSent = errors.New("an earlier attempt may have gone through. Check Drafts and Sent")

// Apply makes the change once. Neither tool is idempotent at Gmail, so the
// add-on writes a note before the request leaves and never repeats a request
// whose outcome it does not know. The one exception is a send whose draft is
// gone after an unknown attempt: sending a draft removes it, so the draft is
// the evidence that the attempt went through.
func (r *Runtime) Apply(ctx context.Context, env contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	return r.Runtime.Apply(env.Request.OperationID, rev, func() (writeflow.Session, error) {
		w, c, err := r.prepare(ctx, env)
		if err != nil {
			return nil, err
		}
		return &session{c: c, w: w}, nil
	})
}

// session is one checked write of the Gmail add-on.
type session struct {
	c *call
	w write
	t target
}

func (s *session) Inspect() (uint64, error) {
	t, rev, err := s.c.inspect(s.w)
	s.t = t
	return rev, err
}

// InspectFailed settles a send whose draft is gone after an earlier attempt.
func (s *session) InspectFailed(started bool, err error) (map[string]any, bool) {
	if started && s.w.tool == ToolSendDraft && errors.Is(err, gmailapi.ErrNotFound) {
		return map[string]any{"note": "The draft is gone, so an earlier attempt sent it."}, true
	}
	return nil, false
}

// Resume refuses to go on after an attempt of unknown outcome.
func (s *session) Resume(started bool) (map[string]any, bool, error) {
	if started {
		return nil, false, errMaybeSent
	}
	return nil, false, nil
}

func (s *session) Send() (map[string]any, error) { return s.c.send(s.w, s.t) }

// translateWrite is translate for a definite failure of a write: a limit
// becomes a refusal that says when to try again, since nothing was changed.
func (r *Runtime) translateWrite(err error, sent bool) error {
	var limited *contract.ErrRateLimited
	var server *gmailapi.ServerError
	if errors.As(err, &limited) {
		return bad("Gmail is limiting calls. Nothing sent. Retry in %d s", limited.Seconds())
	}
	if !sent && errors.As(err, &server) {
		return bad("Gmail failed to answer. Nothing sent. Retry in %d s", server.RetryAfter)
	}
	if errors.Is(err, gmailapi.ErrNotFound) {
		return bad("the draft or the message no longer exists")
	}
	if sent && (errors.Is(err, gmailapi.ErrForbidden) || errors.Is(err, gmailapi.ErrScope)) {
		return bad("Gmail refused it. Reconnect and allow %s", ScopeWriteName)
	}
	return r.Translate(err)
}

// rfc822 builds the message of a draft. Every header value was checked: an
// address is plain, the subject is one line, and the references are IDs of
// the shape <x>. The subject is encoded when it is not ASCII, and the body is
// base64, so no line of the text can end the header block or be read as one.
func rfc822(w write, reply *gMessage) ([]byte, error) {
	var b bytes.Buffer
	line := func(name, value string) { b.WriteString(name + ": " + value + "\r\n") }
	line("To", strings.Join(w.To, ", "))
	if len(w.Cc) > 0 {
		line("Cc", strings.Join(w.Cc, ", "))
	}
	subject := w.Subject
	if reply != nil {
		if subject == "" {
			subject = replySubject(headerText(reply.Payload.header("Subject"), maxSubject))
		}
		id := strings.TrimSpace(reply.Payload.header("Message-ID"))
		if !msgIDShape.MatchString(id) {
			return nil, bad("the message has no usable Message-ID to answer")
		}
		var refs []string
		for _, r := range strings.Fields(reply.Payload.header("References")) {
			if msgIDShape.MatchString(r) {
				refs = append(refs, r)
			}
		}
		refs = append(refs, id)
		if len(refs) > maxRefs {
			refs = refs[len(refs)-maxRefs:]
		}
		line("In-Reply-To", id)
		line("References", strings.Join(refs, " "))
	}
	if strings.ContainsAny(subject, "\r\n") || !utf8.ValidString(subject) {
		return nil, bad("subject must be one line")
	}
	line("Subject", mime.BEncoding.Encode("UTF-8", subject))
	line("MIME-Version", "1.0")
	line("Content-Type", "text/plain; charset=UTF-8")
	line("Content-Transfer-Encoding", "base64")
	b.WriteString("\r\n")
	enc := base64.StdEncoding.EncodeToString([]byte(strings.ReplaceAll(strings.ReplaceAll(*w.Body, "\r\n", "\n"), "\n", "\r\n")))
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
	return b.Bytes(), nil
}

// send makes the request for a checked write.
func (c *call) send(w write, t target) (map[string]any, error) {
	api := c.r.API
	switch w.tool {
	case ToolCreateDraft:
		raw, err := rfc822(w, t.reply)
		if err != nil {
			return nil, err
		}
		msg := map[string]any{"raw": base64.RawURLEncoding.EncodeToString(raw)}
		if t.reply != nil {
			msg["threadId"] = t.reply.ThreadID
		}
		var d gDraft
		if err := api.SendJSON(c.ctx, http.MethodPost, c.token, "/gmail/v1/users/me/drafts", nil, map[string]any{"message": msg}, &d); err != nil {
			return nil, err
		}
		out := map[string]any{"draftId": d.ID}
		if d.Message.ID != "" {
			out["messageId"] = d.Message.ID
		}
		if d.Message.ThreadID != "" {
			out["threadId"] = d.Message.ThreadID
		}
		return out, nil
	case ToolSendDraft:
		var m gMessage
		if err := api.SendJSON(c.ctx, http.MethodPost, c.token, "/gmail/v1/users/me/drafts/send", nil, map[string]any{"id": t.draft.ID}, &m); err != nil {
			return nil, err
		}
		out := map[string]any{"sent": true}
		if m.ID != "" {
			out["messageId"] = m.ID
		}
		if m.ThreadID != "" {
			out["threadId"] = m.ThreadID
		}
		return out, nil
	}
	return nil, bad("unknown tool")
}
