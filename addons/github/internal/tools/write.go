// SPDX-License-Identifier: MIT
package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/github/internal/ghapi"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The write tools of the manifest. All of them are write tools: the host
// decides, from the write mode of the person's binding, whether each one is
// refused, stored as a proposal for a person, or applied at once.
const (
	ToolCommentIssue     = "comment-issue"
	ToolCommentPR        = "comment-pr"
	ToolCreateIssue      = "create-issue"
	ToolEditIssue        = "edit-issue"
	ToolAddLabels        = "add-labels"
	ToolReviewCommentPR  = "review-comment-pr"
	maxLabels            = 10
	maxLabelRunes        = 50
	maxTitleRunes        = 256
	previewBodyRunes     = 800
	previewTargetRunes   = 120
	operationMarkerStart = "<!-- localai-op:"
)

// MaxWriteBody is the most bytes of text the model may put on GitHub in one
// call. What the add-on writes is public to everyone who can see the
// repository, so it stays small enough for a person to read before approving.
const MaxWriteBody = 16 << 10

// WriteTools lists the write tools.
func WriteTools() []string {
	return []string{ToolCommentIssue, ToolCommentPR, ToolCreateIssue, ToolEditIssue, ToolAddLabels, ToolReviewCommentPR}
}

func isWrite(tool string) bool {
	for _, t := range WriteTools() {
		if t == tool {
			return true
		}
	}
	return false
}

// writeArgs holds the arguments of every write tool; a tool uses its own.
type writeArgs struct {
	Repo   string   `json:"repo"`
	Number int      `json:"number"`
	Body   string   `json:"body"`
	Title  string   `json:"title"`
	Labels []string `json:"labels"`
	State  string   `json:"state"`
	Reason string   `json:"reason"`
	Path   string   `json:"path"`
	Line   int      `json:"line"`
	Side   string   `json:"side"`
}

// write is one checked write request.
type write struct {
	tool string
	writeArgs
}

// secretShapes are texts that must never be published: an access token of
// GitHub in any of its formats, a private key block and an authorization
// header. The exact token of the call is checked apart.
var secretShapes = regexp.MustCompile(`(?i)(\bgh[pousr]_[A-Za-z0-9]{20,}|\bgithub_pat_[A-Za-z0-9_]{20,}|-----BEGIN [A-Z ]*PRIVATE KEY-----|authorization:\s*(bearer|token|basic)\s+\S+)`)

// checkPublicText refuses text that is empty (when required), too long, not
// plain text, or that holds the credential of the call or something shaped
// like one. Everything written to GitHub is public to those who see the repo.
func checkPublicText(what, s, token string, required bool) error {
	if strings.TrimSpace(s) == "" {
		if required {
			return bad("%s is required", what)
		}
		return nil
	}
	if len(s) > MaxWriteBody || !utf8.ValidString(s) {
		return bad("%s is longer than %d bytes or not text", what, MaxWriteBody)
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return bad("%s has a control character", what)
		}
	}
	if (token != "" && strings.Contains(s, token)) || secretShapes.MatchString(s) {
		return bad("%s looks like it holds a secret, so it was not sent", what)
	}
	return nil
}

func checkLabels(ls []string, token string) error {
	if len(ls) == 0 || len(ls) > maxLabels {
		return bad("labels has 1 to %d names", maxLabels)
	}
	for _, l := range ls {
		if strings.TrimSpace(l) == "" || utf8.RuneCountInString(l) > maxLabelRunes || strings.ContainsAny(l, ",\n\r") {
			return bad("a label has 1 to %d characters and no comma", maxLabelRunes)
		}
		if err := checkPublicText("a label", l, token, true); err != nil {
			return err
		}
	}
	return nil
}

// parseWrite checks the arguments of a write tool.
func parseWrite(tool string, raw map[string]json.RawMessage, token, key string) (write, error) {
	var a writeArgs
	if err := decodeArgs(raw, &a); err != nil {
		return write{}, err
	}
	w := write{tool: tool, writeArgs: a}
	if err := checkRepo(a.Repo); err != nil {
		return w, err
	}
	if key == "" || !strings.EqualFold(key, a.Repo) {
		return w, bad("repo is not the one the host allowed")
	}
	needNumber := tool != ToolCreateIssue
	if needNumber && a.Number < 1 {
		return w, bad("number is required")
	}
	if !needNumber && a.Number != 0 {
		return w, bad("number is not an argument of this tool")
	}
	switch tool {
	case ToolCommentIssue, ToolCommentPR:
		return w, checkPublicText("body", a.Body, token, true)
	case ToolCreateIssue:
		if strings.TrimSpace(a.Title) == "" || utf8.RuneCountInString(a.Title) > maxTitleRunes || strings.ContainsAny(a.Title, "\n\r") {
			return w, bad("title has 1 to %d characters on one line", maxTitleRunes)
		}
		if err := checkPublicText("title", a.Title, token, true); err != nil {
			return w, err
		}
		if err := checkPublicText("body", a.Body, token, false); err != nil {
			return w, err
		}
		if len(a.Labels) > 0 {
			return w, checkLabels(a.Labels, token)
		}
	case ToolEditIssue:
		if a.Title == "" && a.State == "" {
			return w, bad("give a new title, a new state, or both")
		}
		if a.Title != "" {
			if utf8.RuneCountInString(a.Title) > maxTitleRunes || strings.ContainsAny(a.Title, "\n\r") {
				return w, bad("title has 1 to %d characters on one line", maxTitleRunes)
			}
			if err := checkPublicText("title", a.Title, token, true); err != nil {
				return w, err
			}
		}
		switch a.State {
		case "", "open", "closed":
		default:
			return w, bad("state is open or closed")
		}
		switch a.Reason {
		case "":
		case "completed", "not_planned":
			if a.State != "closed" {
				return w, bad("reason goes with state closed")
			}
		default:
			return w, bad("reason is completed or not_planned")
		}
	case ToolAddLabels:
		return w, checkLabels(a.Labels, token)
	case ToolReviewCommentPR:
		if err := checkPath(a.Path, false); err != nil {
			return w, err
		}
		if a.Line < 1 || a.Line > 1_000_000 {
			return w, bad("line is a line number from 1")
		}
		switch a.Side {
		case "", "RIGHT", "LEFT":
		default:
			return w, bad("side is RIGHT or LEFT")
		}
		return w, checkPublicText("body", a.Body, token, true)
	default:
		return w, bad("unknown tool")
	}
	return w, nil
}

// target is the issue or pull request a tool works on.
type target struct {
	Number      int         `json:"number"`
	Title       string      `json:"title"`
	State       string      `json:"state"`
	Locked      bool        `json:"locked"`
	PullRequest *struct{}   `json:"pull_request"`
	Labels      []labelJSON `json:"labels"`
	HTMLURL     string      `json:"html_url"`
}

func (c call) issue(repo string, n int) (target, error) {
	var t target
	_, err := c.r.API.GetJSON(c.ctx, c.token, "/repos/"+repo+"/issues/"+strconv.Itoa(n), nil, &t)
	return t, err
}

// pullHead returns the commit at the head of a pull request.
func (c call) pullHead(repo string, n int) (string, string, error) {
	var p struct {
		State string `json:"state"`
		Head  struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	_, err := c.r.API.GetJSON(c.ctx, c.token, "/repos/"+repo+"/pulls/"+strconv.Itoa(n), nil, &p)
	return p.Head.SHA, p.State, err
}

func sum64(parts ...string) uint64 {
	h := sha256.New()
	for _, p := range parts {
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], uint32(len(p)))
		h.Write(l[:])
		h.Write([]byte(p))
	}
	return binary.BigEndian.Uint64(h.Sum(nil)[:8]) | 1
}

// inspect reads the target of a write and checks the tool fits it. It returns
// the revision the preview records: a small hash of what makes the write
// wrong if it changed. It is how a stale approval is noticed.
func (c call) inspect(w write) (target, uint64, error) {
	switch w.tool {
	case ToolCreateIssue:
		return target{}, 1, nil
	case ToolReviewCommentPR:
		t, err := c.issue(w.Repo, w.Number)
		if err != nil {
			return t, 0, err
		}
		if t.PullRequest == nil {
			return t, 0, bad("number is an issue, not a pull request")
		}
		if t.State != "open" {
			return t, 0, bad("the pull request is not open")
		}
		sha, _, err := c.pullHead(w.Repo, w.Number)
		if err != nil {
			return t, 0, err
		}
		return t, sum64("head", sha), nil
	}
	t, err := c.issue(w.Repo, w.Number)
	if err != nil {
		return t, 0, err
	}
	switch w.tool {
	case ToolCommentIssue:
		if t.PullRequest != nil {
			return t, 0, bad("number is a pull request: use comment-pr")
		}
	case ToolCommentPR:
		if t.PullRequest == nil {
			return t, 0, bad("number is an issue: use comment-issue")
		}
	}
	if t.Locked && w.tool != ToolEditIssue {
		return t, 0, bad("the conversation is locked")
	}
	switch w.tool {
	case ToolEditIssue:
		if w.State != "" && w.State == t.State && w.Title == "" {
			return t, 0, bad("it is already %s", t.State)
		}
		return t, sum64("edit", t.Title, t.State), nil
	case ToolAddLabels:
		return t, sum64("labels", strconv.FormatBool(t.Locked)), nil
	}
	return t, sum64("comment", strconv.FormatBool(t.Locked), t.State), nil
}

func plainJSON(v any) json.RawMessage {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v)
	return bytes.TrimSpace(b.Bytes())
}

func runes(s string, n int) (string, bool) {
	if utf8.RuneCountInString(s) <= n {
		return s, false
	}
	r := []rune(s)
	return string(r[:n]), true
}

// sample is what the person sees before a change: facts built from the
// arguments and the target, never from model prose. It stays small, since a
// chat keeps only a short tool result; the full arguments are in the proposal.
func sample(w write, t target) map[string]any {
	s := map[string]any{"tool": w.tool, "repo": w.Repo, "public": true,
		"notice": "This text becomes public to everyone who can see the repository."}
	switch w.tool {
	case ToolCommentIssue:
		s["action"] = "Comment on issue"
	case ToolCommentPR:
		s["action"] = "Comment on pull request"
	case ToolCreateIssue:
		s["action"] = "Open an issue"
	case ToolEditIssue:
		s["action"] = "Edit issue"
	case ToolAddLabels:
		s["action"] = "Add labels"
	case ToolReviewCommentPR:
		s["action"] = "Comment on a line of a pull request"
	}
	if w.Number > 0 {
		s["number"] = w.Number
		title, cutT := runes(t.Title, previewTargetRunes)
		s["targetTitle"], s["targetState"] = title, t.State
		if cutT {
			s["targetTitleCut"] = true
		}
	}
	if w.Title != "" {
		s["title"] = w.Title
	}
	if w.State != "" {
		s["state"] = w.State
		if w.Reason != "" {
			s["reason"] = w.Reason
		}
	}
	if len(w.Labels) > 0 {
		s["labels"] = w.Labels
	}
	if w.Path != "" {
		s["path"], s["line"] = w.Path, w.Line
	}
	if w.Body != "" {
		b, cutB := runes(w.Body, previewBodyRunes)
		s["body"], s["bodyBytes"] = b, len(w.Body)
		if cutB {
			s["bodyCut"] = true
		}
	}
	return s
}

// prepare reads the call of a write envelope.
func (r *Runtime) prepare(ctx context.Context, env contract.Envelope) (write, call, error) {
	var args map[string]json.RawMessage
	if json.Unmarshal(env.Request.Arguments, &args) != nil {
		return write{}, call{}, bad("the arguments are not an object")
	}
	var tool string
	if json.Unmarshal(args["tool"], &tool) != nil || !isWrite(tool) {
		return write{}, call{}, bad("unknown tool")
	}
	delete(args, "tool")
	if env.Request.Credential == "" {
		return write{}, call{}, bad("no token for this call")
	}
	w, err := parseWrite(tool, args, env.Request.Credential, env.Request.ResourceKey)
	if err != nil {
		return w, call{}, err
	}
	return w, call{r: r, ctx: ctx, token: env.Request.Credential, key: env.Request.ResourceKey}, nil
}

// Preview checks a write and describes it. It changes nothing.
func (r *Runtime) Preview(ctx context.Context, env contract.Envelope) (contract.Preview, error) {
	w, c, err := r.prepare(ctx, env)
	if err != nil {
		return contract.Preview{}, err
	}
	t, rev, err := c.inspect(w)
	if err != nil {
		return contract.Preview{}, translate(err)
	}
	summary := plainJSON(map[string]any{"count": 1, "sample": sample(w, t)})
	if len(summary) > 3<<10 {
		return contract.Preview{}, bad("the preview is too large")
	}
	return contract.Preview{OperationID: env.Request.OperationID, Revision: contract.Revision{Schema: 1, Data: rev},
		ExpiresAt: r.now().Add(24 * time.Hour), Summary: summary}, nil
}

func (r *Runtime) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Receipt answers from the journal. An operation that was started and not
// finished is unknown: the add-on cannot tell whether GitHub got it.
func (r *Runtime) Receipt(_ context.Context, op string) (contract.Receipt, error) {
	if rc, ok := r.journal().receipt(op); ok {
		return rc, nil
	}
	return contract.Receipt{OperationID: op, State: contract.ReceiptUnknown}, nil
}

// Apply makes the change once. GitHub has no idempotency key, so the add-on
// keeps its own: a receipt per operation, a "started" note written before the
// request, and a marker in the text that lets it find an earlier attempt.
func (r *Runtime) Apply(ctx context.Context, env contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	op := env.Request.OperationID
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	j := r.journal()
	if rc, ok := j.receipt(op); ok {
		return rc, nil
	}
	w, c, err := r.prepare(ctx, env)
	if err != nil {
		return contract.Receipt{}, err
	}
	_, now, err := c.inspect(w)
	if err != nil {
		// Nothing was sent yet, so a limit is a refusal here, not an unknown.
		return contract.Receipt{}, translateWrite(err)
	}
	if now != rev.Data {
		rc := contract.Receipt{OperationID: op, State: contract.ReceiptRejected, Revision: rev,
			Result: contract.Result{Payload: smallResult(map[string]any{"stale": true, "note": "The target changed after the preview. Nothing was sent."})}}
		return rc, j.commit(rc)
	}
	if at, started := j.started(op); started {
		// An earlier attempt may have reached GitHub. Look for its marker
		// before sending anything again.
		if found, ok := c.find(w, op, at); ok {
			rc := committed(op, rev, found)
			return rc, j.commit(rc)
		}
	}
	if err := j.begin(op, r.now()); err != nil {
		return contract.Receipt{}, err
	}
	out, err := c.send(w, op)
	switch {
	case err == nil:
	case errors.Is(err, ghapi.ErrUnreadable):
		// GitHub took the change; only its answer is lost.
		out = map[string]any{"note": "sent; GitHub's answer was not readable"}
	case definite(err):
		j.forget(op)
		return contract.Receipt{}, translateWrite(err)
	default:
		// The request may or may not have arrived. The host will ask for the
		// receipt, get "unknown", and leave the decision to the person.
		return contract.Receipt{}, err
	}
	rc := committed(op, rev, out)
	return rc, j.commit(rc)
}

func committed(op string, rev contract.Revision, out map[string]any) contract.Receipt {
	return contract.Receipt{OperationID: op, State: contract.ReceiptCommitted, Revision: rev, Result: contract.Result{Payload: smallResult(out)}}
}

// smallResult wraps what the receipt echoes. The host copies only a short
// result into a chat, so it holds a link and a number, no GitHub text.
func smallResult(data map[string]any) json.RawMessage {
	return plainJSON(map[string]any{"source": "github", "untrusted": true, "data": data})
}

// definite reports an error that proves GitHub did not make the change.
func definite(err error) bool {
	var limited *contract.ErrRateLimited
	return errors.As(err, &limited) || errors.Is(err, ghapi.ErrUnauthorized) || errors.Is(err, ghapi.ErrForbidden) ||
		errors.Is(err, ghapi.ErrNotFound) || errors.Is(err, ghapi.ErrInvalid)
}

// translateWrite is translate for a definite failure of a write: a limit
// becomes a refusal that says when to try again, since nothing was changed.
func translateWrite(err error) error {
	var limited *contract.ErrRateLimited
	if errors.As(err, &limited) {
		return bad("github is limiting calls; nothing was sent. Propose it again in %d seconds", limited.Seconds())
	}
	return translate(err)
}

// marker is the note that ties text on GitHub to one operation.
func marker(op string) string { return operationMarkerStart + op + " -->" }

func withMarker(body, op string) string {
	if strings.TrimSpace(body) == "" {
		return marker(op)
	}
	return body + "\n\n" + marker(op)
}

// link keeps a URL only when it points at github.com over https.
func link(u string) string {
	if strings.HasPrefix(u, "https://github.com/") && len(u) <= 300 {
		return u
	}
	return ""
}

type created struct {
	ID      int64  `json:"id"`
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
}

func outOf(cr created) map[string]any {
	out := map[string]any{}
	if cr.ID != 0 {
		out["id"] = cr.ID
	}
	if cr.Number != 0 {
		out["number"] = cr.Number
	}
	if u := link(cr.HTMLURL); u != "" {
		out["url"] = u
	}
	return out
}

// send makes the request for a checked write.
func (c call) send(w write, op string) (map[string]any, error) {
	api, base := c.r.API, "/repos/"+w.Repo
	var cr created
	var err error
	switch w.tool {
	case ToolCommentIssue, ToolCommentPR:
		_, err = api.SendJSON(c.ctx, "POST", c.token, base+"/issues/"+strconv.Itoa(w.Number)+"/comments", map[string]any{"body": withMarker(w.Body, op)}, &cr)
	case ToolCreateIssue:
		in := map[string]any{"title": w.Title, "body": withMarker(w.Body, op)}
		if len(w.Labels) > 0 {
			in["labels"] = w.Labels
		}
		_, err = api.SendJSON(c.ctx, "POST", c.token, base+"/issues", in, &cr)
	case ToolEditIssue:
		in := map[string]any{}
		if w.Title != "" {
			in["title"] = w.Title
		}
		if w.State != "" {
			in["state"] = w.State
			if w.Reason != "" {
				in["state_reason"] = w.Reason
			}
		}
		_, err = api.SendJSON(c.ctx, "PATCH", c.token, base+"/issues/"+strconv.Itoa(w.Number), in, &cr)
	case ToolAddLabels:
		_, err = api.SendJSON(c.ctx, "POST", c.token, base+"/issues/"+strconv.Itoa(w.Number)+"/labels", map[string]any{"labels": w.Labels}, nil)
		cr.Number = w.Number
	case ToolReviewCommentPR:
		sha, _, e := c.pullHead(w.Repo, w.Number)
		if e != nil {
			return nil, e
		}
		side := w.Side
		if side == "" {
			side = "RIGHT"
		}
		_, err = api.SendJSON(c.ctx, "POST", c.token, base+"/pulls/"+strconv.Itoa(w.Number)+"/comments",
			map[string]any{"body": withMarker(w.Body, op), "commit_id": sha, "path": w.Path, "line": w.Line, "side": side}, &cr)
	}
	if err != nil {
		return nil, err
	}
	return outOf(cr), nil
}

// find looks on GitHub for text carrying the marker of an operation, written
// since the operation was started. It is read-only.
func (c call) find(w write, op string, since time.Time) (map[string]any, bool) {
	mark := marker(op)
	q := url.Values{"since": {since.Add(-time.Minute).UTC().Format(time.RFC3339)}, "per_page": {"100"}}
	var path string
	switch w.tool {
	case ToolCommentIssue, ToolCommentPR:
		path = "/repos/" + w.Repo + "/issues/" + strconv.Itoa(w.Number) + "/comments"
	case ToolReviewCommentPR:
		path = "/repos/" + w.Repo + "/pulls/" + strconv.Itoa(w.Number) + "/comments"
	case ToolCreateIssue:
		path = "/repos/" + w.Repo + "/issues"
		q.Set("state", "all")
		q.Set("sort", "created")
		q.Set("direction", "desc")
	default:
		return nil, false
	}
	var list []struct {
		ID      int64  `json:"id"`
		Number  int    `json:"number"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
	}
	if _, err := c.r.API.GetJSON(c.ctx, c.token, path, q, &list); err != nil {
		return nil, false
	}
	for _, e := range list {
		if strings.Contains(e.Body, mark) {
			return outOf(created{ID: e.ID, Number: e.Number, HTMLURL: e.HTMLURL}), true
		}
	}
	return nil, false
}

var _ = protocol.ErrBadRequest
