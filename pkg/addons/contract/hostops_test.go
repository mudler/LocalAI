// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHostWireTypesRoundTrip(t *testing.T) {
	req := HostRequest{OperationID: "op1", Op: "finetune.get", Arguments: json.RawMessage(`{"jobId":"j1"}`)}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeHostRequest(b, 1<<20)
	if err != nil || got.OperationID != "op1" || got.Op != "finetune.get" || string(got.Arguments) != `{"jobId":"j1"}` {
		t.Fatalf("%+v %v", got, err)
	}
	resp := HostResponse{Error: &HostError{Code: "scope", Message: "no"}}
	b, _ = json.Marshal(resp)
	var back HostResponse
	if err := json.Unmarshal(b, &back); err != nil || back.Error == nil || back.Error.Code != "scope" || back.Error.Error() == "" {
		t.Fatalf("%+v %v", back, err)
	}
	b, _ = json.Marshal(HostResponse{Result: json.RawMessage(`{"ok":true}`)})
	if err := json.Unmarshal(b, &back); err != nil || string(back.Result) != `{"ok":true}` {
		t.Fatalf("%s %v", b, err)
	}
}

func TestTheStrictDecoderRefusesWhatItDoesNotKnow(t *testing.T) {
	for name, body := range map[string]string{
		"unknown field": `{"operationId":"a","op":"finetune.get","arguments":{},"userId":"x"}`,
		"duplicate key": `{"operationId":"a","operationId":"b","op":"finetune.get"}`,
		"trailing":      `{"operationId":"a","op":"finetune.get"} {}`,
		"not an object": `[]`,
		"empty":         ``,
	} {
		if _, err := DecodeHostRequest([]byte(body), 1<<20); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := DecodeHostRequest([]byte(`{"operationId":"a","op":"finetune.get"}`), 10); err == nil {
		t.Error("an oversized body was accepted")
	}
}

func TestAnOperationNameHasTheDottedShape(t *testing.T) {
	for op, want := range map[string]bool{
		"finetune.get": true, "quantize.start": true, "finetune.a.b": true,
		"": false, "finetune": false, ".get": false, "Finetune.get": false, "finetune.get ": false,
		"finetune/get": false, "finetune.get\n": false, "fine-tune.get": false, "finetune.": false,
		strings.Repeat("a", 70) + ".get": false,
	} {
		if got := ValidHostOp(op); got != want {
			t.Errorf("%q: got %v want %v", op, got, want)
		}
	}
}
