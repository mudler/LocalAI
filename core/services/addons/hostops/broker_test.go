// SPDX-License-Identifier: MIT
package hostops

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func env(addon, op, scope string, exp time.Time) contract.Envelope {
	return contract.Envelope{
		Request:   contract.Request{AddonID: addon, OperationID: op, Generation: 1, Action: "start", Arguments: json.RawMessage(`{}`)},
		Authority: contract.Authority{Actor: contract.Actor{UserID: "u1", CredentialID: "c1", Mode: "named-user"}, Scope: scope, ExpiresAt: exp},
	}
}

type fixture struct {
	b        *Broker
	inflight *InFlight
	h        http.Handler
	seen     *Call
}

func newFixture(t *testing.T, has func(user, feature string) bool) *fixture {
	t.Helper()
	f := &fixture{inflight: NewInFlight(), seen: &Call{}}
	f.b = New(f.inflight, has, contract.DefaultLimits())
	err := f.b.Register(Spec{Op: "finetune.get", Scope: "read", Feature: "fine_tuning", Handler: func(_ context.Context, c Call) (json.RawMessage, error) {
		*f.seen = c
		return json.RawMessage(`{"ok":true}`), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.b.Register(Spec{Op: "finetune.start", Scope: "apply", Feature: "fine_tuning", Handler: func(context.Context, Call) (json.RawMessage, error) {
		return json.RawMessage(`{"id":"j1"}`), nil
	}}); err != nil {
		t.Fatal(err)
	}
	f.h = f.b.Handler("fine-tuning", "secret", []string{"finetune."})
	return f
}

func (f *fixture) call(credential, operationID, op, body string) (int, contract.HostResponse) {
	b, _ := json.Marshal(contract.HostRequest{OperationID: operationID, Op: op, Arguments: json.RawMessage(body)})
	req := httptest.NewRequest(http.MethodPost, "/host", bytes.NewReader(b))
	req.Header.Set("X-Addon-Host-Credential", credential)
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	var out contract.HostResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func always(string, string) bool { return true }

func TestAWrongCredentialIsRefused(t *testing.T) {
	f := newFixture(t, always)
	if code, _ := f.call("nope", "op1", "finetune.get", `{}`); code != http.StatusUnauthorized {
		t.Fatalf("got %d", code)
	}
}

func TestACallWithoutALiveEnvelopeIsRefused(t *testing.T) {
	f := newFixture(t, always)
	code, out := f.call("secret", "op1", "finetune.get", `{}`)
	if code != http.StatusForbidden || out.Error == nil || out.Error.Code != "no_live_operation" {
		t.Fatalf("%d %+v", code, out.Error)
	}
}

func TestAnExpiredAuthorityIsRefused(t *testing.T) {
	f := newFixture(t, always)
	end := f.inflight.Begin(env("fine-tuning", "op1", "read", time.Now().Add(-time.Second)))
	defer end()
	code, out := f.call("secret", "op1", "finetune.get", `{}`)
	if code != http.StatusForbidden || out.Error.Code != "no_live_operation" {
		t.Fatalf("%d %+v", code, out.Error)
	}
}

func TestAReadScopeCannotRunAnApplyOperation(t *testing.T) {
	f := newFixture(t, always)
	end := f.inflight.Begin(env("fine-tuning", "op1", "read", time.Now().Add(time.Minute)))
	defer end()
	code, out := f.call("secret", "op1", "finetune.start", `{}`)
	if code != http.StatusForbidden || out.Error.Code != "scope" {
		t.Fatalf("%d %+v", code, out.Error)
	}
}

func TestTheFeatureOfTheActorIsChecked(t *testing.T) {
	f := newFixture(t, func(user, feature string) bool { return feature != "fine_tuning" })
	end := f.inflight.Begin(env("fine-tuning", "op1", "read", time.Now().Add(time.Minute)))
	defer end()
	code, out := f.call("secret", "op1", "finetune.get", `{}`)
	if code != http.StatusForbidden || out.Error.Code != "feature" {
		t.Fatalf("%d %+v", code, out.Error)
	}
}

func TestTheOperationRunsAsTheActorOfTheEnvelope(t *testing.T) {
	f := newFixture(t, always)
	end := f.inflight.Begin(env("fine-tuning", "op1", "read", time.Now().Add(time.Minute)))
	defer end()
	// An argument that names a user is refused, whatever the schema says.
	code, out := f.call("secret", "op1", "finetune.get", `{"userId":"admin"}`)
	if code != http.StatusBadRequest || out.Error.Code != "bad_request" {
		t.Fatalf("%d %+v", code, out.Error)
	}
	if code, _ := f.call("secret", "op1", "finetune.get", `{"jobId":"j1"}`); code != http.StatusOK {
		t.Fatalf("got %d", code)
	}
	if f.seen.Actor.UserID != "u1" || f.seen.AddonID != "fine-tuning" {
		t.Fatalf("the operation did not run as the envelope's actor: %+v", f.seen)
	}
}

func TestAnOperationOutsideTheAllowedPrefixIsRefused(t *testing.T) {
	f := newFixture(t, always)
	_ = f.b.Register(Spec{Op: "quantize.get", Scope: "read", Feature: "quantization", Handler: func(context.Context, Call) (json.RawMessage, error) { return nil, nil }})
	end := f.inflight.Begin(env("fine-tuning", "op1", "read", time.Now().Add(time.Minute)))
	defer end()
	if code, out := f.call("secret", "op1", "quantize.get", `{}`); code != http.StatusForbidden || out.Error.Code != "unknown_op" {
		t.Fatalf("%d %+v", code, out.Error)
	}
}

func TestAnOversizedBodyIsRefused(t *testing.T) {
	f := newFixture(t, always)
	end := f.inflight.Begin(env("fine-tuning", "op1", "read", time.Now().Add(time.Minute)))
	defer end()
	big := `{"x":"` + string(bytes.Repeat([]byte("a"), int(contract.DefaultLimits().PayloadBytes)+1)) + `"}`
	if code, out := f.call("secret", "op1", "finetune.get", big); code != http.StatusRequestEntityTooLarge || out.Error.Code != "too_large" {
		t.Fatalf("%d %+v", code, out.Error)
	}
}

func TestAnIdempotentOperationRunsOncePerOperationID(t *testing.T) {
	f := newFixture(t, always)
	runs := 0
	_ = f.b.Register(Spec{Op: "finetune.once", Scope: "apply", Feature: "fine_tuning", Idempotent: true, Handler: func(context.Context, Call) (json.RawMessage, error) {
		runs++
		return json.RawMessage(`{"id":"j9"}`), nil
	}})
	end := f.inflight.Begin(env("fine-tuning", "op1", "apply", time.Now().Add(time.Minute)))
	defer end()
	for i := 0; i < 2; i++ {
		if code, _ := f.call("secret", "op1", "finetune.once", `{}`); code != http.StatusOK {
			t.Fatalf("call %d: %d", i, code)
		}
	}
	if runs != 1 {
		t.Fatalf("a repeated operationId must not run the operation twice, ran %d", runs)
	}
}
