// SPDX-License-Identifier: MIT

package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"example.com/addon-hello/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func envelope(args string) contract.Envelope {
	return contract.Envelope{
		Request: contract.Request{AddonID: "hello", OperationID: "op1", Generation: 1, Action: "read", Arguments: json.RawMessage(args)},
		Authority: contract.Authority{
			Actor: contract.Actor{UserID: "alice", CredentialID: "k", Mode: "named-user"},
			Scope: "read", ExpiresAt: time.Now().Add(time.Minute),
		},
	}
}

func TestGreet(t *testing.T) {
	res, err := tools.Runtime{}.Read(context.Background(), envelope(`{"tool":"greet","name":"Ada"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Payload) != `{"greeting":"Hello, Ada!","ok":true}` {
		t.Fatalf("unexpected payload %s", res.Payload)
	}
}

func TestGreetRefusesUnknownField(t *testing.T) {
	if _, err := (tools.Runtime{}).Read(context.Background(), envelope(`{"tool":"greet","name":"Ada","x":1}`)); err == nil {
		t.Fatal("an unknown argument must be refused")
	}
}

// The manifest must parse and pass the same checks as an installed package.
func TestManifest(t *testing.T) {
	data, err := os.ReadFile("../../manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contract.ParseManifest(data); err != nil {
		t.Fatal(err)
	}
}
