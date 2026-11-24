// SPDX-License-Identifier: MIT
package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/ssh/internal/sshclient"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// run sends one non-interactive command. It has no terminal, no standard
// input, no environment variables chosen by the model, no shell session, no
// file transfer and no forwarding. The command goes to the login shell of the
// user, as `ssh host command` does.

type runArgs struct {
	Host           string `json:"host"`
	Command        string `json:"command"`
	TimeoutSeconds *int   `json:"timeoutSeconds"`
}

// plan is a checked command with its preview.
type plan struct {
	call    *call
	command string
	timeout time.Duration
	rev     uint64
	summary string
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

func plainJSON(v any) json.RawMessage {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v)
	return bytes.TrimSpace(b.Bytes())
}

// checkCommand refuses what cannot be one command to show a person.
func checkCommand(cmd string) error {
	if strings.TrimSpace(cmd) == "" {
		return bad("command is empty")
	}
	if len(cmd) > MaxCommandLength || !utf8.ValidString(cmd) {
		return bad("command is longer than %d bytes or is not valid text", MaxCommandLength)
	}
	for _, r := range cmd {
		if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f {
			return bad("command holds a control character")
		}
	}
	return nil
}

func (r *Runtime) prepare(ctx context.Context, env contract.Envelope) (*plan, error) {
	tool, args, err := splitArgs(env)
	if err != nil {
		return nil, err
	}
	if tool != ToolRun {
		return nil, bad("unknown tool")
	}
	var a runArgs
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if err := checkCommand(a.Command); err != nil {
		return nil, err
	}
	secs := DefaultTimeout
	if a.TimeoutSeconds != nil {
		secs = *a.TimeoutSeconds
	}
	if secs < 1 || secs > MaxTimeout {
		return nil, bad("timeoutSeconds is from 1 to %d", MaxTimeout)
	}
	c, err := r.newCall(ctx, env, a.Host)
	if err != nil {
		return nil, err
	}
	if c.target.Pin == "" {
		return nil, bad("%s", sshclient.ErrNoPin)
	}
	if c.cred.Appears(a.Command) {
		return nil, bad("the command holds the credential of the connection; never put a secret in a command")
	}
	fp := sshclient.FingerprintOfPin(c.target.Pin)
	p := &plan{call: c, command: a.Command, timeout: time.Duration(secs) * time.Second,
		rev: sum64("run", c.target.Key(), c.target.Pin, a.Command, strconv.Itoa(secs))}
	sample := map[string]any{
		"tool": ToolRun, "host": c.target.Key(), "user": c.target.User, "command": a.Command, "timeoutSeconds": secs,
		"hostKey": fp, "login": string(c.cred.Kind),
		"effect": "Run this exact command once on the host, as this user, with no terminal and no input. Its output goes back to the chat. A command can change the machine.",
	}
	p.summary = string(plainJSON(map[string]any{"sample": sample}))
	return p, nil
}

// Preview checks a command and describes it. It connects to nothing.
func (r *Runtime) Preview(ctx context.Context, env contract.Envelope) (contract.Preview, error) {
	p, err := r.prepare(ctx, env)
	if err != nil {
		return contract.Preview{}, translate(err)
	}
	if len(p.summary) > previewBudget {
		return contract.Preview{}, bad("the preview is too large")
	}
	return contract.Preview{OperationID: env.Request.OperationID, Revision: contract.Revision{Schema: 1, Data: p.rev},
		ExpiresAt: r.now().Add(runExpiry), Summary: json.RawMessage(p.summary)}, nil
}

// Receipt answers from the journal. A command that was started and not
// finished is unknown: the add-on cannot tell whether it ran.
func (r *Runtime) Receipt(_ context.Context, op string) (contract.Receipt, error) {
	if rc, ok := r.journal().Receipt(op); ok {
		return rc, nil
	}
	return contract.Receipt{OperationID: op, State: contract.ReceiptUnknown}, nil
}

// Apply runs the command once. It checks the arguments again and rejects the
// operation as stale when the host key or the command changed since the
// preview. It never sends a command twice: the journal notes the start before
// the command leaves, and a repeat of an operation answers from the journal.
func (r *Runtime) Apply(ctx context.Context, env contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	op := env.Request.OperationID
	p, err := r.prepare(ctx, env)
	if err != nil {
		return contract.Receipt{}, translate(err)
	}
	// The connection lock comes before the journal check, so two calls of the
	// same operation cannot both pass the check and both send the command.
	unlock, err := p.call.lock()
	if err != nil {
		return contract.Receipt{}, translate(err)
	}
	defer unlock()
	j := r.journal()
	if rc, ok := j.Receipt(op); ok {
		return rc, nil
	}
	if _, started := j.Started(op); started {
		return contract.Receipt{OperationID: op, State: contract.ReceiptUnknown, Revision: rev}, nil
	}
	if p.rev != rev.Data {
		rc := contract.Receipt{OperationID: op, State: contract.ReceiptRejected, Revision: rev,
			Result: contract.Result{Payload: plainJSON(map[string]any{"source": SourceName, "untrusted": true,
				"data": map[string]any{"stale": true, "note": "The host key or the command changed after the preview. Nothing was run."}})}}
		return rc, j.Commit(rc)
	}
	conn, err := p.call.connect()
	if err != nil {
		return contract.Receipt{}, translate(err)
	}
	defer conn.Close()
	res, err := conn.Run(ctx, p.command, p.timeout, func() error { return j.Begin(op, r.now()) }, p.call.cred.Scrub)
	switch {
	case errors.Is(err, sshclient.ErrNoSession):
		j.Forget(op)
		return contract.Receipt{}, translate(err)
	case errors.Is(err, sshclient.ErrLost):
		// The command may or may not have run. The host asks for the receipt,
		// gets "unknown", and leaves the decision to the person.
		return contract.Receipt{OperationID: op, State: contract.ReceiptUnknown, Revision: rev}, nil
	case err != nil:
		j.Forget(op)
		return contract.Receipt{}, err
	}
	data := map[string]any{
		"host": p.call.target.Key(), "command": p.call.cred.Scrub(p.command), "exitCode": res.ExitCode,
		"stdout": res.Stdout, "stderr": res.Stderr, "stdoutTruncated": res.StdoutTruncated, "stderrTruncated": res.StderrTruncated,
		"timedOut": res.TimedOut, "durationMs": res.Duration.Milliseconds(),
	}
	if res.Signal != "" {
		data["signal"] = res.Signal
	}
	if res.TimedOut {
		data["note"] = "The command did not finish in time and the connection was closed. It may still be running on the host."
	}
	b, err := json.Marshal(wrapped{Source: SourceName, Untrusted: true, Notice: noticeText, Data: data})
	if err != nil {
		return contract.Receipt{}, err
	}
	full := contract.Receipt{OperationID: op, State: contract.ReceiptCommitted, Revision: rev, Result: contract.Result{Payload: b}}
	// The journal keeps the fact and the exit code, not the output: output is
	// text of the machine and can be large.
	kept := plainJSON(map[string]any{"source": SourceName, "untrusted": true, "data": map[string]any{
		"exitCode": res.ExitCode, "timedOut": res.TimedOut, "note": "The command ran. Its output is not kept after the answer was given."}})
	slim := full
	slim.Result = contract.Result{Payload: kept}
	if len(b) > MaxPayload {
		// Not reachable with the output caps; kept so a limit change cannot
		// turn a command that ran into an error.
		full.Result = slim.Result
	}
	return full, j.Commit(slim)
}
