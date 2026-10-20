// SPDX-License-Identifier: MIT
package hostclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func startupFor(endpoint string) protocol.Startup {
	return protocol.Startup{HostEndpoint: endpoint, HostCredential: "secret", Limits: contract.DefaultLimits()}
}

func TestDoSendsTheCredentialAndTheBody(t *testing.T) {
	var gotCred string
	var gotReq contract.HostRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCred = r.Header.Get("X-Addon-Host-Credential")
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		_ = json.NewEncoder(w).Encode(contract.HostResponse{Result: json.RawMessage(`{"id":"j1"}`)})
	}))
	defer srv.Close()
	c := New(startupFor(srv.URL))
	if c == nil {
		t.Fatal("no client for a loopback endpoint")
	}
	var out struct{ ID string }
	if err := c.Do(context.Background(), "op1", "finetune.get", map[string]string{"jobId": "j1"}, &out); err != nil {
		t.Fatal(err)
	}
	if gotCred != "secret" || gotReq.OperationID != "op1" || gotReq.Op != "finetune.get" || string(gotReq.Arguments) != `{"jobId":"j1"}` || out.ID != "j1" {
		t.Fatalf("%q %+v %+v", gotCred, gotReq, out)
	}
}

func TestDoTurnsAnErrorAnswerIntoAHostError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(contract.HostResponse{Error: &contract.HostError{Code: "scope", Message: "no"}})
	}))
	defer srv.Close()
	err := New(startupFor(srv.URL)).Do(context.Background(), "op1", "finetune.start", nil, nil)
	var he *contract.HostError
	if !errors.As(err, &he) || he.Code != "scope" {
		t.Fatalf("%v", err)
	}
}

func TestAStartupWithoutAnEndpointGivesANilClient(t *testing.T) {
	c := New(protocol.Startup{})
	if c != nil {
		t.Fatal("expected nil")
	}
	if err := c.Do(context.Background(), "op1", "finetune.get", nil, nil); !errors.Is(err, ErrNoHostOps) {
		t.Fatalf("%v", err)
	}
}

func TestANonLoopbackEndpointGivesANilClient(t *testing.T) {
	for _, e := range []string{"http://10.0.0.1:80", "http://example.com:80", "https://127.0.0.1:80", "http://127.0.0.1", "http://127.0.0.1:80/x", "http://localhost:80", "http://127.0.0.1:0", "http://user@127.0.0.1:80", "http://[::1]:80"} {
		if New(startupFor(e)) != nil {
			t.Errorf("%s: got a client", e)
		}
	}
}
