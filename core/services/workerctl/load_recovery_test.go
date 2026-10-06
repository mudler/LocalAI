package workerctl_test

import (
	"encoding/json"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"testing"
)

func TestRecoveryWire(t *testing.T) {
	id := &workerctl.OperationIdentity{TrackingKey: "model", Generation: "g", Incarnation: "boot"}
	raw, err := json.Marshal(workerctl.ModelStopRequest{Operation: id, ProcessInstance: "instance"})
	if err != nil {
		t.Fatal(err)
	}
	var got workerctl.ModelStopRequest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Operation == nil || *got.Operation != *id || got.ProcessInstance != "instance" {
		t.Fatalf("lost identity: %s", raw)
	}
	var legacy workerctl.ModelsRunningReply
	if err := json.Unmarshal([]byte(`{"models":[]}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.ReportsOperations || legacy.Incarnation != "" {
		t.Fatal("legacy response claimed recovery capability")
	}
}
