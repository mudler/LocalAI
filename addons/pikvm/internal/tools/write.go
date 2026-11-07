// SPDX-License-Identifier: MIT
package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/pikvm/internal/kvmd"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The three write tools have physical effects on a real machine. The manifest
// declares approval "always" for each: a person confirms every call, whatever
// the write mode of the connection is. power-action and send-key are also
// approvalOverridable: the owner can choose "Don't ask" for them, and only the
// host honors that, for the owner's own chat and for paired senders. type-text
// is never overridable. The kvmd API has no idempotency key, so
// the add-on notes an operation before it sends it and never sends one twice.

const (
	previewBudget = 8 << 10
	powerExpiry   = 10 * time.Minute
	typeExpiry    = 5 * time.Minute
)

// Power actions. The hard ones need the setting of the device.
const (
	ActOn        = "on"
	ActOff       = "off"
	ActReset     = "reset"
	ActOffHard   = "off_hard"
	ActResetHard = "reset_hard"
	ActLongPress = "long_press"
)

var powerActions = map[string]struct {
	hard   bool
	effect string
}{
	ActOn:        {false, "Press the power button briefly: the machine starts if it is off."},
	ActOff:       {false, "Press the power button briefly (soft power off): the operating system decides, usually a clean shutdown."},
	ActReset:     {false, "Press the reset button: the machine restarts at once and unsaved work is lost."},
	ActOffHard:   {true, "Force the power off (hold the power button for several seconds): unsaved work is lost and disks can be damaged."},
	ActResetHard: {true, "Reset the hardware: the machine restarts at once and unsaved work is lost."},
	ActLongPress: {true, "Hold the power button for several seconds: a forced power off, or a start if the machine is off."},
}

type writeArgs struct {
	Device string   `json:"device"`
	Action string   `json:"command"`
	Text   string   `json:"text"`
	Keys   []string `json:"keys"`
}

// plan is a checked write with its preview and the request to send.
type plan struct {
	tool    string
	sample  map[string]any
	rev     uint64
	expiry  time.Duration
	reqs    []kvmd.Request
	typed   int
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

func post(path []string, q url.Values, text string) kvmd.Request {
	return kvmd.Request{Method: http.MethodPost, Path: path, Query: q, Text: text}
}

// plan checks the arguments against the settings of the device and the state
// the device reports now. It changes nothing on the device.
func (c *call) plan(tool string, raw map[string]json.RawMessage) (plan, error) {
	var a writeArgs
	if err := decodeArgs(raw, &a); err != nil {
		return plan{}, err
	}
	switch tool {
	case ToolPowerAction:
		return c.planPower(a)
	case ToolTypeText:
		return c.planType(a)
	case ToolSendKey:
		return c.planKeys(a)
	}
	return plan{}, bad("unknown tool")
}

func (c *call) planPower(a writeArgs) (plan, error) {
	act, ok := powerActions[a.Action]
	if !ok || a.Text != "" || a.Keys != nil {
		return plan{}, bad("command is one of on, off, reset, off_hard, reset_hard, long_press")
	}
	if act.hard && !c.set.AllowHardPower {
		return plan{}, bad("%s is a hard power action: an administrator must switch on hard power actions for this device on the PiKVM page", a.Action)
	}
	s, err := c.atx()
	if err != nil {
		return plan{}, err
	}
	switch {
	case !s.Enabled:
		return plan{}, bad("ATX power control is not enabled on this device")
	case s.Busy:
		return plan{}, bad("the device is busy with another power action; ask again in a few seconds")
	case a.Action == ActOn && s.Leds.Power:
		return plan{}, bad("the machine is already on; nothing to do")
	case (a.Action == ActOff || a.Action == ActOffHard) && !s.Leds.Power:
		return plan{}, bad("the machine is already off; nothing to do")
	case (a.Action == ActReset || a.Action == ActResetHard) && !s.Leds.Power:
		return plan{}, bad("the machine is off; a reset does nothing")
	}
	p := plan{tool: ToolPowerAction, expiry: powerExpiry, rev: sum64("power", c.dev.Key(), a.Action, s.power(), boolStr(act.hard && c.set.AllowHardPower))}
	switch a.Action {
	case ActOn, ActOff, ActOffHard, ActResetHard:
		p.reqs = []kvmd.Request{post([]string{"api", "atx", "power"}, url.Values{"action": {a.Action}}, "")}
	case ActReset:
		p.reqs = []kvmd.Request{post([]string{"api", "atx", "click"}, url.Values{"button": {"reset"}}, "")}
	case ActLongPress:
		p.reqs = []kvmd.Request{post([]string{"api", "atx", "click"}, url.Values{"button": {"power_long"}}, "")}
	}
	p.sample = map[string]any{"tool": p.tool, "device": c.dev.Key(), "action": a.Action, "hard": act.hard, "effect": act.effect,
		"powerNow": s.power(), "diskActivity": s.Leds.HDD}
	return p, nil
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// needTyping checks the typing switch and that the device has a keyboard.
func (c *call) needTyping() (hidState, error) {
	if !c.set.AllowTyping {
		return hidState{}, bad("keyboard input is off for this device: an administrator must switch on typing on the PiKVM page")
	}
	h, err := c.hid()
	if err != nil {
		return h, err
	}
	if !h.Enabled || !h.Keyboard.Online {
		return h, bad("the keyboard of the device is not connected to the target machine; nothing can be typed")
	}
	return h, nil
}

func (c *call) planType(a writeArgs) (plan, error) {
	if a.Action != "" || a.Keys != nil {
		return plan{}, bad("type-text takes only text")
	}
	if !c.set.AllowTyping {
		return plan{}, bad("keyboard input is off for this device: an administrator must switch on typing on the PiKVM page")
	}
	if err := checkTypedText(a.Text, c.set.MaxTypeChars); err != nil {
		return plan{}, err
	}
	if _, err := c.needTyping(); err != nil {
		return plan{}, err
	}
	n := utf8.RuneCountInString(a.Text)
	p := plan{tool: ToolTypeText, expiry: typeExpiry, typed: n, rev: sum64("type", c.dev.Key(), a.Text)}
	p.reqs = []kvmd.Request{post([]string{"api", "hid", "print"}, url.Values{"limit": {"1000"}}, a.Text)}
	p.sample = map[string]any{"tool": p.tool, "device": c.dev.Key(), "text": a.Text, "chars": n, "lines": strings.Count(a.Text, "\n") + 1,
		"effect": "Type this exact text on the keyboard of the target machine, into whatever has the focus. A newline presses Enter."}
	return p, nil
}

func (c *call) planKeys(a writeArgs) (plan, error) {
	if a.Action != "" || a.Text != "" {
		return plan{}, bad("send-key takes only keys")
	}
	if n := len(a.Keys); n < 1 || n > MaxKeys {
		return plan{}, bad("name 1 to %d keys", MaxKeys)
	}
	if !c.set.AllowTyping {
		return plan{}, bad("keyboard input is off for this device: an administrator must switch on typing on the PiKVM page")
	}
	var labels []string
	var reqs []kvmd.Request
	for _, name := range a.Keys {
		k, ok := lookupKey(name)
		if !ok {
			return plan{}, bad("a key is not allowed. Allowed: %s", strings.Join(AllowedKeys(), ", "))
		}
		labels = append(labels, k.label)
		if len(k.kvmd) == 1 {
			reqs = append(reqs, post([]string{"api", "hid", "events", "send_key"}, url.Values{"key": {k.kvmd[0]}}, ""))
		} else {
			reqs = append(reqs, post([]string{"api", "hid", "events", "send_shortcut"}, url.Values{"keys": {strings.Join(k.kvmd, ",")}}, ""))
		}
	}
	if _, err := c.needTyping(); err != nil {
		return plan{}, err
	}
	p := plan{tool: ToolSendKey, expiry: typeExpiry, reqs: reqs, rev: sum64(append([]string{"keys", c.dev.Key()}, labels...)...)}
	p.sample = map[string]any{"tool": p.tool, "device": c.dev.Key(), "keys": labels,
		"effect": "Press these keys, in this order, on the keyboard of the target machine."}
	return p, nil
}

func (r *Runtime) prepare(ctx context.Context, env contract.Envelope) (plan, error) {
	tool, args, err := splitArgs(env)
	if err != nil {
		return plan{}, err
	}
	if !isWrite(tool) {
		return plan{}, bad("unknown tool")
	}
	var d struct {
		Device string `json:"device"`
	}
	b, _ := json.Marshal(args)
	_ = json.Unmarshal(b, &d)
	c, err := r.newCall(ctx, env, d.Device)
	if err != nil {
		return plan{}, err
	}
	p, err := c.plan(tool, args)
	if err != nil {
		return plan{}, err
	}
	p.summary = string(plainJSON(map[string]any{"sample": p.sample}))
	return p, nil
}

// Preview checks a write and describes it. It changes nothing.
func (r *Runtime) Preview(ctx context.Context, env contract.Envelope) (contract.Preview, error) {
	p, err := r.prepare(ctx, env)
	if err != nil {
		return contract.Preview{}, translate(err)
	}
	if len(p.summary) > previewBudget {
		return contract.Preview{}, bad("the preview is too large")
	}
	return contract.Preview{OperationID: env.Request.OperationID, Revision: contract.Revision{Schema: 1, Data: p.rev},
		ExpiresAt: r.now().Add(p.expiry), Summary: json.RawMessage(p.summary)}, nil
}

// Receipt answers from the journal. An operation that was started and not
// finished is unknown: the add-on cannot tell whether the device got it.
func (r *Runtime) Receipt(_ context.Context, op string) (contract.Receipt, error) {
	if rc, ok := r.journal().receipt(op); ok {
		return rc, nil
	}
	return contract.Receipt{OperationID: op, State: contract.ReceiptUnknown}, nil
}

// Apply sends the request once. It reads the device again first and rejects
// the operation as stale when the power state, the text, the keys or the
// settings are not those of the preview.
func (r *Runtime) Apply(ctx context.Context, env contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	op := env.Request.OperationID
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	j := r.journal()
	if rc, ok := j.receipt(op); ok {
		return rc, nil
	}
	if _, started := j.started(op); started {
		return contract.Receipt{OperationID: op, State: contract.ReceiptUnknown, Revision: rev}, nil
	}
	p, err := r.prepare(ctx, env)
	if err != nil {
		return contract.Receipt{}, translateWrite(err)
	}
	if p.rev != rev.Data {
		rc := contract.Receipt{OperationID: op, State: contract.ReceiptRejected, Revision: rev,
			Result: contract.Result{Payload: smallResult(map[string]any{"stale": true, "note": "The device or its settings changed after the preview. Nothing was sent."})}}
		return rc, j.commit(rc)
	}
	if err := j.begin(op, r.now()); err != nil {
		return contract.Receipt{}, err
	}
	tool, args, _ := splitArgs(env)
	var d struct {
		Device string `json:"device"`
	}
	b, _ := json.Marshal(args)
	_ = json.Unmarshal(b, &d)
	c, _ := r.newCall(ctx, env, d.Device)
	sent := 0
	for _, req := range p.reqs {
		if _, err := c.r.API.Do(ctx, c.dev, c.cred, req); err != nil {
			if sent == 0 && definite(err) {
				j.forget(op)
				return contract.Receipt{}, translateWrite(err)
			}
			// The request may or may not have arrived, or only some keys were
			// pressed. The host asks for the receipt, gets "unknown", and
			// leaves the decision to the person.
			if !errors.Is(err, kvmd.ErrUnreadable) {
				return contract.Receipt{}, err
			}
		}
		sent++
	}
	out := map[string]any{"tool": tool, "sent": true}
	switch tool {
	case ToolPowerAction:
		out["action"] = p.sample["action"]
		out["note"] = "The button was pressed. The machine reacts on its own: read the power state to see the result."
	case ToolTypeText:
		out["chars"] = p.typed
	case ToolSendKey:
		out["keys"] = p.sample["keys"]
	}
	rc := contract.Receipt{OperationID: op, State: contract.ReceiptCommitted, Revision: rev, Result: contract.Result{Payload: smallResult(out)}}
	return rc, j.commit(rc)
}

func smallResult(data map[string]any) json.RawMessage {
	return plainJSON(map[string]any{"source": SourceName, "untrusted": true, "data": data})
}

// definite reports an error that proves the device did not act.
func definite(err error) bool {
	var limited *contract.ErrRateLimited
	return errors.As(err, &limited) || errors.Is(err, kvmd.ErrUnauthorized) || errors.Is(err, kvmd.ErrForbidden) || errors.Is(err, kvmd.ErrNotFound) ||
		errors.Is(err, kvmd.ErrInvalid) || errors.Is(err, kvmd.ErrBusy) || errors.Is(err, kvmd.ErrCertificate) || errors.Is(err, kvmd.ErrPin) ||
		errors.Is(err, kvmd.ErrBadAddress) || errors.Is(err, kvmd.ErrRedirect)
}

func translateWrite(err error) error {
	var limited *contract.ErrRateLimited
	if errors.As(err, &limited) {
		return bad("the device is limiting calls; nothing was sent. Propose it again in %d seconds", limited.RetryAfter)
	}
	return translate(err)
}
