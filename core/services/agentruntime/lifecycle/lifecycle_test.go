// SPDX-License-Identifier: MIT
package lifecycle

import (
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestLifecycle(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Lifecycle") }

type traceInput struct {
	kind, id string
	override map[string]any
}
type traceCase struct {
	name           string
	policy         Policy
	events         []traceInput
	rules, effects []string
	final          string
}

var traces = []traceCase{
	{name: "happy", policy: Policy("Wait"), final: "Idle", rules: []string{"R17", "R27", "R31", "R33", "R24", "R25"}, effects: []string{"Run", "ShowApproval", "AuthorizedTool", "CommitCheckpoint"}, events: []traceInput{
		{"Submit", "a", map[string]any{}},
		{"ToolRequest", "", map[string]any{}},
		{"Approve", "", map[string]any{}},
		{"ToolResult", "", map[string]any{}},
		{"Complete", "", map[string]any{}},
		{"CheckpointOK", "", map[string]any{}},
	}},
	{name: "concurrent submit CAS retry FIFO", policy: Policy("Wait"), final: "Running", rules: []string{"R17", "R05", "R18", "R24", "R25"}, effects: []string{"Run", "CommitCheckpoint", "Run"}, events: []traceInput{
		{"Submit", "a", map[string]any{}},
		{"Submit", "b", map[string]any{"rev": 0}},
		{"Submit", "b", map[string]any{}},
		{"Complete", "", map[string]any{}},
		{"CheckpointOK", "", map[string]any{}},
	}},
	{name: "disconnect reconnect", policy: Policy("Wait"), final: "Idle", rules: []string{"R17", "R03", "R03", "R24", "R25"}, effects: []string{"Run", "ReplaySanitizedEvents", "CommitCheckpoint"}, events: []traceInput{
		{"Submit", "a", map[string]any{}},
		{"Disconnect", "", map[string]any{}},
		{"Reconnect", "", map[string]any{}},
		{"Complete", "", map[string]any{}},
		{"CheckpointOK", "", map[string]any{}},
	}},
	{name: "duplicate late event", policy: Policy("Wait"), final: "Idle", rules: []string{"R23", "R17", "R14", "R24", "R25", "R23"}, effects: []string{"Run", "CommitCheckpoint"}, events: []traceInput{
		{"Complete", "a", map[string]any{}},
		{"Submit", "a", map[string]any{}},
		{"Submit", "a", map[string]any{}},
		{"Complete", "", map[string]any{}},
		{"CheckpointOK", "", map[string]any{}},
		{"ToolResult", "a", map[string]any{}},
	}},
	{name: "stop delete late", policy: Policy("Wait"), final: "Deleted", rules: []string{"R17", "R22", "R06", "R39", "R11", "R02"}, effects: []string{"Run", "CancelTree", "RecordPartialTurn", "CancelTree", "RevokeArtifactAccess"}, events: []traceInput{
		{"Submit", "a", map[string]any{}},
		{"Stop", "", map[string]any{}},
		{"Complete", "a", map[string]any{"epoch": 1}},
		{"Stopped", "", map[string]any{}},
		{"Delete", "", map[string]any{}},
		{"Submit", "b", map[string]any{}},
	}},
	{name: "restart wait queue explicit resume", policy: Policy("Wait"), final: "Restoring", rules: []string{"R17", "R27", "R18", "R09", "R15", "R20"}, effects: []string{"Run", "ShowApproval", "CancelTree", "LoadCheckpointAndToolJournal"}, events: []traceInput{
		{"Submit", "a", map[string]any{}},
		{"ToolRequest", "", map[string]any{}},
		{"Submit", "b", map[string]any{}},
		{"Restart", "", map[string]any{}},
		{"Submit", "c", map[string]any{}},
		{"Resume", "", map[string]any{}},
	}},
	{name: "approval revoked", policy: Policy("Wait"), final: "Interrupted", rules: []string{"R17", "R27", "R12", "R06"}, effects: []string{"Run", "ShowApproval", "CancelTree", "PurgeTaintedContext"}, events: []traceInput{
		{"Submit", "a", map[string]any{}},
		{"ToolRequest", "", map[string]any{}},
		{"Revoke", "", map[string]any{}},
		{"Approve", "a", map[string]any{"epoch": 1}},
	}},
	{name: "storage failed", policy: Policy("Wait"), final: "Interrupted", rules: []string{"R17", "R24", "R04", "R10"}, effects: []string{"Run", "CommitCheckpoint", "CancelTree", "CloseAdmission"}, events: []traceInput{
		{"Submit", "a", map[string]any{}},
		{"Complete", "", map[string]any{}},
		{"CheckpointFailed", "", map[string]any{}},
		{"Submit", "b", map[string]any{}},
	}},
	{name: "compaction failure", policy: Policy("Wait"), final: "Restoring", rules: []string{"R17", "R36", "R38", "R20"}, effects: []string{"Run", "CompactSanitizedContext", "NotifyCompactionFailure", "LoadCheckpointAndToolJournal"}, events: []traceInput{
		{"Submit", "a", map[string]any{}},
		{"Compact", "", map[string]any{}},
		{"CompactFailed", "", map[string]any{}},
		{"Resume", "", map[string]any{}},
	}},
	{name: "timeout retry new identity", policy: Policy("Wait"), final: "Running", rules: []string{"R17", "R40", "R20", "R44", "R18"}, effects: []string{"Run", "CancelTree", "LoadCheckpointAndToolJournal", "ReuseRecordedResults", "ContinueRestoredTurn"}, events: []traceInput{
		{"Submit", "a", map[string]any{}},
		{"Timeout", "", map[string]any{}},
		{"Resume", "", map[string]any{}},
		{"RestoreOK", "", map[string]any{}},
		{"Submit", "b", map[string]any{}},
	}},
	{name: "unknown tool outcome never replay", policy: Policy("AllowAll"), final: "WaitingReconciliation", rules: []string{"R17", "R30", "R09", "R20", "R43"}, effects: []string{"Run", "AuthorizedTool", "CancelTree", "LoadCheckpointAndToolJournal", "ShowReconciliation"}, events: []traceInput{
		{"Submit", "a", map[string]any{}},
		{"ToolRequest", "", map[string]any{}},
		{"Restart", "", map[string]any{}},
		{"Resume", "", map[string]any{}},
		{"RestoreOK", "", map[string]any{}},
	}},
	{name: "questions and successful compaction", policy: Policy("Wait"), final: "Running", rules: []string{"R17", "R34", "R18", "R35", "R36", "R37", "R24", "R25"}, effects: []string{"Run", "ShowQuestion", "Continue", "CompactSanitizedContext", "CommitCheckpoint", "Run"}, events: []traceInput{
		{"Submit", "a", map[string]any{}},
		{"Ask", "", map[string]any{}},
		{"Submit", "b", map[string]any{}},
		{"Answer", "", map[string]any{}},
		{"Compact", "", map[string]any{}},
		{"CompactOK", "", map[string]any{}},
		{"Complete", "", map[string]any{}},
		{"CheckpointOK", "", map[string]any{}},
	}},
	{name: "same turn resume holds FIFO until checkpoint", policy: Policy("Wait"), final: "Running", rules: []string{"R17", "R18", "R09", "R20", "R44", "R24", "R25"}, effects: []string{"Run", "CancelTree", "LoadCheckpointAndToolJournal", "ReuseRecordedResults", "ContinueRestoredTurn", "CommitCheckpoint", "Run"}, events: []traceInput{
		{"Submit", "a", map[string]any{}},
		{"Submit", "b", map[string]any{}},
		{"Restart", "", map[string]any{}},
		{"Resume", "", map[string]any{}},
		{"RestoreOK", "", map[string]any{}},
		{"Complete", "", map[string]any{}},
		{"CheckpointOK", "", map[string]any{}},
	}},
	{name: "restore failure retains input and queue", policy: Policy("Wait"), final: "Interrupted", rules: []string{"R17", "R18", "R09", "R20", "R42"}, effects: []string{"Run", "CancelTree", "LoadCheckpointAndToolJournal", "NotifyRestoreFailure"}, events: []traceInput{
		{"Submit", "a", map[string]any{}},
		{"Submit", "b", map[string]any{}},
		{"Restart", "", map[string]any{}},
		{"Resume", "", map[string]any{}},
		{"RestoreFailed", "", map[string]any{}},
	}},
	{name: "stop timeout unknown journal survives reconciliation restart", policy: Policy("AllowAll"), final: "Running", rules: []string{"R17", "R30", "R18", "R22", "R40", "R09", "R20", "R43", "R00", "R09", "R20", "R43", "R46"}, effects: []string{"Run", "AuthorizedTool", "CancelTree", "CancelTree", "CancelTree", "LoadCheckpointAndToolJournal", "ShowReconciliation", "CancelTree", "LoadCheckpointAndToolJournal", "ShowReconciliation", "ReuseRecordedResults", "ContinueRestoredTurn"}, events: []traceInput{
		{"Submit", "a", map[string]any{}},
		{"ToolRequest", "", map[string]any{}},
		{"Submit", "b", map[string]any{}},
		{"Stop", "", map[string]any{}},
		{"Timeout", "", map[string]any{}},
		{"Restart", "", map[string]any{}},
		{"Resume", "", map[string]any{}},
		{"RestoreOK", "", map[string]any{}},
		{"ReconcileOK", "", map[string]any{}},
		{"Restart", "", map[string]any{}},
		{"Resume", "", map[string]any{}},
		{"RestoreOK", "", map[string]any{}},
		{"ReconcileOK", "", map[string]any{"result": "verified completed"}},
	}},
	{name: "duplicate previous result cannot settle next tool", policy: Policy("AllowAll"), final: "Running", rules: []string{"R17", "R30", "R33", "R30", "R50", "R00", "R33"}, effects: []string{"Run", "AuthorizedTool", "AuthorizedTool"}, events: []traceInput{
		{"Submit", "a", map[string]any{}},
		{"ToolRequest", "", map[string]any{}},
		{"ToolResult", "", map[string]any{}},
		{"ToolRequest", "", map[string]any{"request": "second"}},
		{"ToolResult", "", map[string]any{}},
		{"Complete", "", map[string]any{}},
		{"ToolResult", "", map[string]any{"request": "second"}},
	}},
	{name: "completed result reuse after compaction restart no redispatch", policy: Policy("AllowAll"), final: "Running", rules: []string{"R17", "R30", "R33", "R36", "R09", "R20", "R44", "R48"}, effects: []string{"Run", "AuthorizedTool", "CompactSanitizedContext", "CancelTree", "LoadCheckpointAndToolJournal", "ReuseRecordedResults", "ContinueRestoredTurn", "ReuseToolResult"}, events: []traceInput{
		{"Submit", "a", map[string]any{}},
		{"ToolRequest", "", map[string]any{}},
		{"ToolResult", "", map[string]any{}},
		{"Compact", "", map[string]any{}},
		{"Restart", "", map[string]any{}},
		{"Resume", "", map[string]any{}},
		{"RestoreOK", "", map[string]any{}},
		{"ToolRequest", "", map[string]any{}},
	}},
	{name: "late restore and tool callbacks cannot advance recovery", policy: Policy("AllowAll"), final: "WaitingReconciliation", rules: []string{"R17", "R30", "R09", "R20", "R41", "R41", "R43", "R00", "R41"}, effects: []string{"Run", "AuthorizedTool", "CancelTree", "LoadCheckpointAndToolJournal", "ShowReconciliation"}, events: []traceInput{
		{"Submit", "a", map[string]any{}},
		{"ToolRequest", "", map[string]any{}},
		{"Restart", "", map[string]any{}},
		{"Resume", "", map[string]any{}},
		{"RestoreOK", "", map[string]any{"attempt": 1}},
		{"ToolResult", "", map[string]any{"attempt": 1}},
		{"RestoreOK", "", map[string]any{}},
		{"ToolResult", "", map[string]any{}},
		{"ReconcileOK", "", map[string]any{"attempt": 1, "result": "old"}},
	}},
	{name: "unknown outcome cannot compact away recovery gate", policy: Policy("AllowAll"), final: "WaitingReconciliation", rules: []string{"R17", "R30", "R09", "R20", "R43", "R00", "R00"}, effects: []string{"Run", "AuthorizedTool", "CancelTree", "LoadCheckpointAndToolJournal", "ShowReconciliation"}, events: []traceInput{
		{"Submit", "a", map[string]any{}},
		{"ToolRequest", "", map[string]any{}},
		{"Restart", "", map[string]any{}},
		{"Resume", "", map[string]any{}},
		{"RestoreOK", "", map[string]any{}},
		{"Compact", "", map[string]any{}},
		{"Complete", "", map[string]any{}},
	}},
}

func snapshot(s State) string {
	b, err := json.Marshal(s)
	Expect(err).NotTo(HaveOccurred())
	return string(b)
}
func step(s State, kind, id string, overrides map[string]any) Transition {
	before := snapshot(s)
	tr, err := Reduce(s, testEvent(s, kind, id, overrides))
	Expect(err).NotTo(HaveOccurred())
	Expect(snapshot(s)).To(Equal(before), "input mutation")
	Expect(validate(tr.State)).To(Succeed())
	Expect(tr.Rule).NotTo(BeEmpty())
	Expect(tr.Guard).NotTo(BeEmpty())
	return tr
}

var _ = Describe("documentation traces", func() {
	for _, tc := range traces {
		It(tc.name, func() {
			s := NewState("owner")
			s.Policy = tc.policy
			rules, effects := []string{}, []string{}
			for _, e := range tc.events {
				tr := step(s, e.kind, e.id, e.override)
				s = tr.State
				rules = append(rules, tr.Rule)
				for _, ef := range tr.Effects {
					effects = append(effects, ef.tag())
				}
			}
			Expect(rules).To(Equal(tc.rules))
			Expect(effects).To(Equal(tc.effects))
			Expect(s.Phase.tag()).To(Equal(tc.final))
			if tc.name == "restore failure retains input and queue" || tc.name == "stop timeout unknown journal survives reconciliation restart" {
				Expect(turn(s.Phase)).To(Equal("a"))
				Expect(s.Queue).To(Equal([]string{"b"}))
			}
		})
	}
})
var _ = Describe("policy modes", func() {
	for policy, rule := range map[Policy]string{Wait: "R27", DenyContinue: "R28", StopPolicy: "R29", AllowAll: "R30"} {
		It(string(policy), func() {
			s := NewState("owner")
			s.Policy = policy
			s = step(s, "Submit", "a", nil).State
			Expect(step(s, "ToolRequest", "", nil).Rule).To(Equal(rule))
		})
	}
})
