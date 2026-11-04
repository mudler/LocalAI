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
	"sort"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/home-assistant/internal/haapi"
	"github.com/mudler/LocalAI/addons/home-assistant/internal/policy"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The two write tools are one operation with two confirmation levels. The host
// decides from the write mode of the connection whether call-service is
// refused, stored as a proposal for a person, or applied at once.
// call-physical-service declares approval "always" in the manifest: a person
// confirms every call, whatever the write mode is. A service goes to the tool
// that matches its class (policy.Physical), and the other tool refuses it.

const (
	maxDataBytes   = 2 << 10
	maxDataDepth   = 3
	maxDataString  = 200
	maxDataItems   = 20
	previewBudget  = 3 << 10
	quietExpiry    = 2 * time.Hour
	physicalExpiry = 15 * time.Minute
)

var (
	nameShape = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	// targetKeys inside data would add targets that the cap and the entity checks never saw.
	targetKeys = map[string]bool{"entity_id": true, "device_id": true, "area_id": true, "floor_id": true, "label_id": true}
)

type writeArgs struct {
	Instance string         `json:"instance"`
	Domain   string         `json:"domain"`
	Service  string         `json:"service"`
	EntityID []string       `json:"entityIds"`
	Data     map[string]any `json:"data"`
}

type write struct {
	tool string
	writeArgs
}

func checkData(v any, depth int) error {
	if depth > maxDataDepth {
		return bad("data is nested too deep")
	}
	switch x := v.(type) {
	case nil, bool, float64:
		return nil
	case string:
		if len(x) > maxDataString {
			return bad("a text in data has at most %d characters", maxDataString)
		}
		if strings.Contains(x, "{{") || strings.Contains(x, "{%") || strings.Contains(x, "{#") {
			return bad("data may not hold template markup")
		}
		return checkText("a text in data", x, maxDataString)
	case []any:
		if len(x) > maxDataItems {
			return bad("a list in data has at most %d items", maxDataItems)
		}
		for _, e := range x {
			if err := checkData(e, depth+1); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		if len(x) > maxDataItems {
			return bad("data has at most %d keys", maxDataItems)
		}
		for k, e := range x {
			if !nameShape.MatchString(k) || targetKeys[k] {
				return bad("data key %q is not allowed", short(k, 30))
			}
			if err := checkData(e, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return bad("data holds a value that is not JSON")
}

// parseWrite checks the arguments of a write tool against the policy.
func (c *call) parseWrite(tool string, raw map[string]json.RawMessage) (write, error) {
	var a writeArgs
	if err := decodeArgs(raw, &a); err != nil {
		return write{}, err
	}
	w := write{tool: tool, writeArgs: a}
	if err := c.instance(a.Instance); err != nil {
		return w, err
	}
	if !nameShape.MatchString(a.Domain) || !nameShape.MatchString(a.Service) {
		return w, bad("domain and service are words like light and turn_on")
	}
	if policy.HardService(a.Domain, a.Service) {
		return w, bad("%s.%s can never be called by this add-on: locks, alarms, sirens, valves, cameras, restarts, reloads, and shell, system and add-on management are not available to a model", a.Domain, a.Service)
	}
	if !c.pol.ServiceAllowed(a.Domain, a.Service) {
		return w, bad("%s.%s is not on the allowlist of the add-on. An administrator can allow it on the add-on page", a.Domain, a.Service)
	}
	phys := policy.Physical(a.Domain, a.Service)
	switch {
	case phys && tool == ToolCallService:
		return w, bad("%s.%s changes something physical or runs automation: use %s, which a person confirms each time", a.Domain, a.Service, ToolCallPhysical)
	case !phys && tool == ToolCallPhysical:
		return w, bad("%s.%s is a quiet service: use %s", a.Domain, a.Service, ToolCallService)
	}
	if n := len(a.EntityID); n < 1 || n > c.pol.MaxTargets {
		return w, bad("name 1 to %d entities in entityIds. Area, device and all targets are not accepted", c.pol.MaxTargets)
	}
	seen := map[string]bool{}
	for _, id := range a.EntityID {
		if err := checkEntity(id); err != nil {
			return w, err
		}
		if seen[id] {
			return w, bad("entityIds names %s twice", id)
		}
		seen[id] = true
		if d, _, _ := strings.Cut(id, "."); d != a.Domain {
			return w, bad("%s is not an entity of the domain %s", id, a.Domain)
		}
		if c.pol.EntityExcluded(id) {
			return w, bad("%s is excluded by the policy of the add-on", id)
		}
	}
	if a.Data != nil {
		b, _ := json.Marshal(a.Data)
		if len(b) > maxDataBytes {
			return w, bad("data is longer than %d bytes", maxDataBytes)
		}
		if err := checkData(a.Data, 1); err != nil {
			return w, err
		}
	}
	return w, nil
}

type target struct {
	EntityID string `json:"entityId"`
	Name     string `json:"name"`
	State    string `json:"state"`
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

// inspect reads each target and checks it. The revision holds what makes the
// call wrong if it changed: the entities, the service and the policy. The state
// of a light is not part of it, since it moves all the time.
func (c *call) inspect(w write) ([]target, uint64, error) {
	hid, err := c.hidden()
	if err != nil {
		return nil, 0, err
	}
	out := make([]target, 0, len(w.EntityID))
	for _, id := range w.EntityID {
		if !c.visible(id, hid) {
			return nil, 0, bad("%s is excluded by the policy of the add-on", id)
		}
		var s haState
		if err := c.get("/api/states/"+url.PathEscape(id), nil, &s); err != nil {
			if errors.Is(err, haapi.ErrNotFound) {
				return nil, 0, bad("%s does not exist", id)
			}
			return nil, 0, err
		}
		if w.Domain == "cover" {
			dc, _ := s.Attributes["device_class"].(string)
			if policy.HardCover(dc) {
				return nil, 0, bad("%s is a door, a gate, a garage door or a cover with no known type: this add-on never moves it", id)
			}
		}
		out = append(out, target{EntityID: id, Name: friendly(s.Attributes, id), State: short(s.State, 40)})
	}
	ids := append([]string(nil), w.EntityID...)
	sort.Strings(ids)
	parts := append([]string{"svc", w.Domain, w.Service, c.raw.Hash()}, ids...)
	return out, sum64(parts...), nil
}

func plainJSON(v any) json.RawMessage {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v)
	return bytes.TrimSpace(b.Bytes())
}

// sample is what the person sees before a call: facts built from the
// arguments and the states read from Home Assistant, never from model prose.
func sample(w write, ts []target, dataCut, nameCut int) map[string]any {
	rows := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		rows = append(rows, map[string]any{"entityId": t.EntityID, "name": short(t.Name, nameCut), "state": short(t.State, 30)})
	}
	s := map[string]any{"tool": w.tool, "instance": w.Instance, "domain": w.Domain, "service": w.Service,
		"action": w.Domain + "." + w.Service, "physical": policy.Physical(w.Domain, w.Service), "targets": rows}
	if len(w.Data) > 0 {
		b := plainJSON(w.Data)
		d, cut := runes(string(b), dataCut)
		s["data"] = d
		if cut {
			s["dataCut"] = true
		}
	}
	return s
}

func (r *Runtime) prepare(ctx context.Context, env contract.Envelope) (write, *call, error) {
	tool, args, err := splitArgs(env)
	if err != nil {
		return write{}, nil, err
	}
	if !isWrite(tool) {
		return write{}, nil, bad("unknown tool")
	}
	if env.Request.Credential == "" {
		return write{}, nil, bad("no token for this call")
	}
	c, err := r.newCall(ctx, env)
	if err != nil {
		return write{}, nil, err
	}
	w, err := c.parseWrite(tool, args)
	return w, c, err
}

// Preview checks a write and describes it. It changes nothing.
func (r *Runtime) Preview(ctx context.Context, env contract.Envelope) (contract.Preview, error) {
	w, c, err := r.prepare(ctx, env)
	if err != nil {
		return contract.Preview{}, translate(err)
	}
	ts, rev, err := c.inspect(w)
	if err != nil {
		return contract.Preview{}, translate(err)
	}
	summary := plainJSON(map[string]any{"count": len(ts), "sample": sample(w, ts, 500, 60)})
	if len(summary) > previewBudget {
		summary = plainJSON(map[string]any{"count": len(ts), "sample": sample(w, ts, 120, 24)})
	}
	if len(summary) > previewBudget {
		return contract.Preview{}, bad("the preview is too large")
	}
	exp := quietExpiry
	if policy.Physical(w.Domain, w.Service) {
		exp = physicalExpiry
	}
	return contract.Preview{OperationID: env.Request.OperationID, Revision: contract.Revision{Schema: 1, Data: rev},
		ExpiresAt: r.now().Add(exp), Summary: summary}, nil
}

// Receipt answers from the journal. An operation that was started and not
// finished is unknown: the add-on cannot tell whether Home Assistant got it.
func (r *Runtime) Receipt(_ context.Context, op string) (contract.Receipt, error) {
	if rc, ok := r.journal().receipt(op); ok {
		return rc, nil
	}
	return contract.Receipt{OperationID: op, State: contract.ReceiptUnknown}, nil
}

// Apply makes the call once. Home Assistant has no idempotency key, and a call
// such as toggle is not safe to repeat, so the add-on keeps a "started" note
// before it sends and never sends an operation twice: an operation that was
// started and has no receipt is unknown, and a person decides.
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
	w, c, err := r.prepare(ctx, env)
	if err != nil {
		return contract.Receipt{}, translateWrite(err)
	}
	_, now, err := c.inspect(w)
	if err != nil {
		return contract.Receipt{}, translateWrite(err)
	}
	if now != rev.Data {
		rc := contract.Receipt{OperationID: op, State: contract.ReceiptRejected, Revision: rev,
			Result: contract.Result{Payload: smallResult(map[string]any{"stale": true, "note": "The entities or the policy changed after the preview. Nothing was sent."})}}
		return rc, j.commit(rc)
	}
	if err := j.begin(op, r.now()); err != nil {
		return contract.Receipt{}, err
	}
	out, err := c.send(w)
	switch {
	case err == nil:
	case errors.Is(err, haapi.ErrUnreadable):
		out = map[string]any{"note": "sent; the answer of Home Assistant was not readable"}
	case definite(err):
		j.forget(op)
		return contract.Receipt{}, translateWrite(err)
	default:
		// The request may or may not have arrived. The host asks for the
		// receipt, gets "unknown", and leaves the decision to the person.
		return contract.Receipt{}, err
	}
	rc := contract.Receipt{OperationID: op, State: contract.ReceiptCommitted, Revision: rev, Result: contract.Result{Payload: smallResult(out)}}
	return rc, j.commit(rc)
}

func smallResult(data map[string]any) json.RawMessage {
	return plainJSON(map[string]any{"source": SourceName, "untrusted": true, "data": data})
}

// definite reports an error that proves Home Assistant did not make the change.
func definite(err error) bool {
	var limited *contract.ErrRateLimited
	return errors.As(err, &limited) || errors.Is(err, haapi.ErrUnauthorized) || errors.Is(err, haapi.ErrForbidden) ||
		errors.Is(err, haapi.ErrNotFound) || errors.Is(err, haapi.ErrInvalid) || errors.Is(err, haapi.ErrCertificate) || errors.Is(err, haapi.ErrBadAddress)
}

func translateWrite(err error) error {
	var limited *contract.ErrRateLimited
	if errors.As(err, &limited) {
		return bad("home assistant is limiting calls; nothing was sent. Propose it again in %d seconds", limited.Seconds())
	}
	return translate(err)
}

// send makes the call. The answer of Home Assistant lists the states it
// changed with all their attributes; only the entity and the new state of the
// named targets are kept.
func (c *call) send(w write) (map[string]any, error) {
	body := map[string]any{}
	for k, v := range w.Data {
		body[k] = v
	}
	body["entity_id"] = w.EntityID
	var changed []haState
	if err := c.r.API.PostJSON(c.ctx, c.target, c.token, "/api/services/"+w.Domain+"/"+w.Service, body, &changed); err != nil {
		return nil, err
	}
	asked := map[string]bool{}
	for _, id := range w.EntityID {
		asked[id] = true
	}
	rows := []map[string]any{}
	for _, s := range changed {
		if asked[s.EntityID] && len(rows) < policy.MaxTargetsCap {
			rows = append(rows, map[string]any{"entityId": s.EntityID, "state": short(s.State, 40)})
		}
	}
	return map[string]any{"service": w.Domain + "." + w.Service, "entities": len(w.EntityID), "states": rows}, nil
}
