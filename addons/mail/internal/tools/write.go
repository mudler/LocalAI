// SPDX-License-Identifier: MIT
package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/internal/journal"
	"github.com/mudler/LocalAI/addons/mail/internal/account"
	"github.com/mudler/LocalAI/addons/mail/internal/compose"
	"github.com/mudler/LocalAI/addons/mail/internal/dial"
	"github.com/mudler/LocalAI/addons/mail/internal/imapc"
	"github.com/mudler/LocalAI/addons/mail/internal/smtpc"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

const (
	previewBodyRunes = 800
)

func isWrite(tool string) bool { return tool == ToolSendMessage || tool == ToolSaveDraft }

// write is one checked write request.
type write struct {
	op   string
	tool string
	acct account.Account
	msg  compose.Message
}

func sum64(parts ...string) uint64 {
	h := sha256.New()
	for _, p := range parts {
		// The length is written as text, so each part is delimited and no
		// integer conversion is needed.
		h.Write([]byte(strconv.Itoa(len(p)) + ":"))
		h.Write([]byte(p))
	}
	return binary.BigEndian.Uint64(h.Sum(nil)[:8]) | 1
}

// messageID is the Message-ID of the mail of one operation. It is derived from
// the operation, so a retry builds the same message and a draft that was
// saved before can be found again.
func messageID(op, address string) string {
	h := sha256.Sum256([]byte(op))
	domain := address[strings.LastIndexByte(address, '@')+1:]
	return "localai-" + hex.EncodeToString(h[:10]) + "@" + domain
}

// prepare reads the call of a write envelope and checks the message.
func (r *Runtime) prepare(env contract.Envelope) (write, error) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(env.Request.Arguments, &raw) != nil {
		return write{}, bad("the arguments are not an object")
	}
	var tool string
	if json.Unmarshal(raw["tool"], &tool) != nil || !isWrite(tool) {
		return write{}, bad("unknown tool")
	}
	delete(raw, "tool")
	var a writeArgs
	if err := decodeArgs(raw, &a); err != nil {
		return write{}, err
	}
	acct, err := accountOf(env, a.Mailbox)
	if err != nil {
		return write{}, err
	}
	m := compose.Message{FromName: acct.Name, FromAddress: acct.Address, To: a.To, Cc: a.Cc, Subject: a.Subject, Body: a.Body,
		InReplyTo: strings.Trim(a.InReplyTo, "<>"), MessageID: messageID(env.Request.OperationID, acct.Address)}
	for _, ref := range a.References {
		m.References = append(m.References, strings.Trim(ref, "<>"))
	}
	if env.Request.OperationID == "" {
		return write{}, bad("no operation")
	}
	if err := m.Check(); err != nil {
		return write{}, translate(err)
	}
	// A secret the model could have copied into the text is not sent.
	if len(acct.Password) >= 6 && (strings.Contains(m.Body, acct.Password) || strings.Contains(m.Subject, acct.Password)) {
		return write{}, bad("the text looks like it holds the password of the account, so it was not used")
	}
	return write{op: env.Request.OperationID, tool: tool, acct: acct, msg: m}, nil
}

func (w write) revision() uint64 {
	return sum64(w.tool, w.acct.Address, strings.Join(w.msg.To, ","), strings.Join(w.msg.Cc, ","), w.msg.Subject, w.msg.Body)
}

func runes(s string, n int) (string, bool) {
	if utf8.RuneCountInString(s) <= n {
		return s, false
	}
	return string([]rune(s)[:n]), true
}

func plainJSON(v any) json.RawMessage {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v)
	return bytes.TrimSpace(b.Bytes())
}

// sample is what the person sees before the change: facts built from the
// arguments, never model prose about them.
func (w write) sample() map[string]any {
	s := map[string]any{"tool": w.tool, "from": w.acct.Address, "to": w.msg.To, "subject": w.msg.Subject}
	if w.tool == ToolSendMessage {
		s["action"] = "Send an email"
		s["notice"] = "This sends an email from your address. Everyone in To and Cc will read it, and it cannot be taken back."
	} else {
		s["action"] = "Save an email draft"
		s["notice"] = "This saves a draft in your Drafts folder. Nothing is sent."
	}
	if len(w.msg.Cc) > 0 {
		s["cc"] = w.msg.Cc
	}
	if w.msg.InReplyTo != "" {
		s["inReplyTo"] = w.msg.InReplyTo
	}
	b, cutB := runes(w.msg.Body, previewBodyRunes)
	s["body"], s["bodyBytes"] = b, len(w.msg.Body)
	if cutB {
		s["bodyCut"] = true
	}
	return s
}

// Preview checks a write and describes it. It changes nothing and needs no
// connection.
func (r *Runtime) Preview(_ context.Context, env contract.Envelope) (contract.Preview, error) {
	w, err := r.prepare(env)
	if err != nil {
		return contract.Preview{}, err
	}
	summary := plainJSON(map[string]any{"count": 1, "sample": w.sample()})
	if len(summary) > 3<<10 {
		return contract.Preview{}, bad("the preview is too large")
	}
	return contract.Preview{OperationID: env.Request.OperationID, Revision: contract.Revision{Schema: 1, Data: w.revision()},
		ExpiresAt: r.now().Add(24 * time.Hour), Summary: summary}, nil
}

// Receipt answers from the journal. An operation that was started and not
// finished is unknown: the add-on cannot tell whether the server took it.
func (r *Runtime) Receipt(_ context.Context, op string) (contract.Receipt, error) {
	if rc, ok := r.journal().Receipt(op); ok {
		return rc, nil
	}
	return contract.Receipt{OperationID: op, State: contract.ReceiptUnknown}, nil
}

// errUnknown is what Apply returns when the outcome is not known. It is not a
// refusal, so the host reads it as uncertain and asks for the receipt.
var errUnknown = errors.New("the outcome of the send is not known")

// Apply makes the change once. SMTP has no idempotency key, so a send that was
// started and did not finish is never repeated: the person decides.
func (r *Runtime) Apply(ctx context.Context, env contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	op := env.Request.OperationID
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	j := r.journal()
	if rc, ok := j.Receipt(op); ok {
		return rc, nil
	}
	w, err := r.prepare(env)
	if err != nil {
		return contract.Receipt{}, err
	}
	if w.revision() != rev.Data {
		rc := contract.Receipt{OperationID: op, State: contract.ReceiptRejected, Revision: rev,
			Result: contract.Result{Payload: smallResult(map[string]any{"stale": true, "note": "The message changed after the preview. Nothing was sent."})}}
		return rc, j.Commit(rc)
	}
	ctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	raw, err := compose.Build(w.msg)
	if err != nil {
		return contract.Receipt{}, translate(err)
	}
	_, started := j.Started(op)
	if w.tool == ToolSendMessage {
		return r.applySend(ctx, j, w, raw, rev, started)
	}
	return r.applyDraft(ctx, j, w, raw, rev, started)
}

func (r *Runtime) applySend(ctx context.Context, j *journal.Journal, w write, raw []byte, rev contract.Revision, started bool) (contract.Receipt, error) {
	if started {
		// An earlier attempt may have reached the server. Sending again could
		// deliver the message twice.
		return contract.Receipt{}, errUnknown
	}
	id := w.opID()
	if err := j.Begin(id, r.now()); err != nil {
		return contract.Receipt{}, err
	}
	rcpts := append(append([]string{}, w.msg.To...), w.msg.Cc...)
	err := smtpc.Send(ctx, r.Dialer, w.acct, w.acct.Address, rcpts, raw)
	switch {
	case err == nil:
	case smtpc.Definite(err):
		j.Forget(id)
		return contract.Receipt{}, translateWrite(err)
	default:
		return contract.Receipt{}, errUnknown
	}
	rc := committed(id, rev, map[string]any{"sent": true, "messageId": w.msg.MessageID, "recipients": len(rcpts)})
	return rc, j.Commit(rc)
}

func (r *Runtime) applyDraft(ctx context.Context, j *journal.Journal, w write, raw []byte, rev contract.Revision, started bool) (contract.Receipt, error) {
	id := w.opID()
	s, err := imapc.Open(ctx, r.Dialer, w.acct)
	if err != nil {
		return contract.Receipt{}, translateWrite(err)
	}
	defer s.Close()
	folder, err := s.DraftsFolder(ctx)
	if err != nil {
		return contract.Receipt{}, translateWrite(err)
	}
	if started {
		// Look for the draft of the earlier attempt by its Message-ID.
		if uid, ferr := s.FindByMessageID(ctx, folder, w.msg.MessageID); ferr == nil && uid != 0 {
			rc := committed(id, rev, map[string]any{"saved": true, "folder": folder, "uid": uid})
			return rc, j.Commit(rc)
		}
	}
	if err := j.Begin(id, r.now()); err != nil {
		return contract.Receipt{}, err
	}
	uid, err := s.AppendDraft(ctx, folder, raw)
	switch {
	case err == nil:
	case errors.Is(err, imapc.ErrServer), errors.Is(err, imapc.ErrNoFolder), errors.Is(err, imapc.ErrNotAllowed):
		j.Forget(id)
		return contract.Receipt{}, translateWrite(err)
	default:
		return contract.Receipt{}, errUnknown
	}
	out := map[string]any{"saved": true, "folder": folder}
	if uid != 0 {
		out["uid"] = uid
	}
	rc := committed(id, rev, out)
	return rc, j.Commit(rc)
}

// opID is the journal key: the operation the host knows the change by.
func (w write) opID() string { return w.op }

func committed(op string, rev contract.Revision, out map[string]any) contract.Receipt {
	return contract.Receipt{OperationID: op, State: contract.ReceiptCommitted, Revision: rev, Result: contract.Result{Payload: smallResult(out)}}
}

func smallResult(data map[string]any) json.RawMessage {
	return plainJSON(map[string]any{"source": "mail", "untrusted": true, "data": data})
}

// translateWrite is translate for a failure that proves nothing was changed.
func translateWrite(err error) error {
	if errors.Is(err, dial.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return bad("%s", dial.ErrTimeout.Error())
	}
	return translate(err)
}
