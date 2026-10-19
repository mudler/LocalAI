// SPDX-License-Identifier: MIT
package nibadapter

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	openai "github.com/sashabaranov/go-openai"
)

func TestNibAdapter(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Pinned nib adapter") }

var _ = Describe("v0.12.1 contract", func() {
	var cp Checkpoint
	BeforeEach(func() {
		cp = Checkpoint{Version: CheckpointVersion,
			Context:   []byte(`[{"role":"user","content":"summary artifact://3"},{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":"source"}]`),
			History:   []byte(`[{"role":"user","content":"visible history, not summary"}]`),
			Artifacts: []byte(`[{"id":3,"tool":"compaction","created":"1970-01-01T00:00:01Z","content":"full head"}]`),
			Pruning:   []byte(`{"pruned":{"c1":"old"},"compressed":{"c1":{"level":1,"content":"short"}},"compress_band":1}`)}
	})
	It("advertises only the executable bounded contract", func() {
		Expect(PinnedCapabilities()).To(Equal(Capabilities{Version: "v0.12.1", CheckpointRestore: true, HistoryRestore: true, ArtifactRestore: true, PruningRestore: true, Streaming: true, EventProjection: true, Approvals: true, Questions: true, CompactionEvents: true, HelperEvents: true}))
		Expect(RequireSecureTools(nil)).To(MatchError(ErrPolicyRequired))
		Expect(RequireSecureTools(func(Identity, chat.ToolCallRequest) error { return nil })).To(MatchError(ErrMandatoryPolicyUnsupported))
	})
	It("round trips independent owned buffers and rejects malformed versions and sections", func() {
		encoded, err := EncodeCheckpoint(cp)
		Expect(err).NotTo(HaveOccurred())
		decoded, err := DecodeCheckpoint(encoded)
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded).To(Equal(cp))
		encoded[0] = '!'
		Expect(decoded).To(Equal(cp))
		for _, section := range []string{"context", "history", "artifacts", "pruning"} {
			broken := cp
			switch section {
			case "context":
				broken.Context = []byte(`{}`)
			case "history":
				broken.History = []byte(`null`)
			case "artifacts":
				broken.Artifacts = []byte(`[{}] trailing`)
			case "pruning":
				broken.Pruning = []byte(`[]`)
			}
			_, err = RestoreConfig(types.Config{}, broken)
			Expect(err).To(HaveOccurred())
		}
		cp.Version = "other"
		_, err = EncodeCheckpoint(cp)
		Expect(err).To(HaveOccurred())
		_, err = DecodeCheckpoint([]byte(`{"version":"other"}`))
		Expect(err).To(HaveOccurred())
		_, err = DecodeCheckpoint([]byte(`{} garbage`))
		Expect(err).To(HaveOccurred())
	})
	It("restores and exports a real isolated offline nib session without collapsing history", func() {
		home := GinkgoT().TempDir()
		GinkgoT().Setenv("HOME", home)
		GinkgoT().Setenv("XDG_CONFIG_HOME", home)
		GinkgoT().Setenv("XDG_STATE_HOME", home)
		off := false
		cfg, err := RestoreConfig(types.Config{Model: "fake", APIKey: "fake", BaseURL: "http://127.0.0.1:1/v1", BaseDir: home, WorkingDir: home, LSPAutoDetect: &off, BuiltinTools: []string{"no-tools"}}, cp)
		Expect(err).NotTo(HaveOccurred())
		cp.Context[0] = '!'
		cp.History[0] = '!'
		cp.Artifacts[0] = '!'
		cp.Pruning[0] = '!'
		session, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
		Expect(err).NotTo(HaveOccurred())
		defer session.Close()
		_, err = ExportCheckpoint(session, false)
		Expect(err).To(MatchError(ErrNotQuiescent))
		exported, err := ExportCheckpoint(session, true)
		Expect(err).NotTo(HaveOccurred())
		restored, err := RestoreConfig(types.Config{}, exported)
		Expect(err).NotTo(HaveOccurred())
		Expect(restored.InitialContext).To(Equal(cfg.InitialContext))
		Expect(restored.InitialHistory).To(Equal(cfg.InitialHistory))
		Expect(restored.InitialArtifacts).To(Equal(cfg.InitialArtifacts))
		Expect(restored.InitialPruning).To(Equal(cfg.InitialPruning))
		Expect(restored.InitialContext[1].ToolCalls[0].ID).To(Equal("c1"))
		Expect(restored.InitialContext[2].ToolCallID).To(Equal("c1"))
		Expect(restored.InitialArtifacts[0].ID).To(Equal(int64(3)))
		restored.InitialContext[1].ToolCalls[0].ID = "changed"
		restored.InitialPruning.Pruned["c1"] = "changed"
		again, err := ExportCheckpoint(session, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(exported))
		secondCfg, err := RestoreConfig(types.Config{Model: "fake", APIKey: "fake", BaseURL: "http://127.0.0.1:1/v1", BaseDir: GinkgoT().TempDir(), LSPAutoDetect: &off}, again)
		Expect(err).NotTo(HaveOccurred())
		second, err := chat.NewSession(context.Background(), secondCfg, chat.Callbacks{})
		Expect(err).NotTo(HaveOccurred())
		defer second.Close()
		secondExport, err := ExportCheckpoint(second, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(secondExport).To(Equal(again))
	})
	It("rejects empty context with display history before construction or encoding", func() {
		cp.Context = []byte(`[]`)
		cp.Pruning = []byte(`{}`)
		_, err := RestoreConfig(types.Config{}, cp)
		Expect(err).To(MatchError(ContainSubstring("unsupported checkpoint state")))
		_, err = EncodeCheckpoint(cp)
		Expect(err).To(HaveOccurred())
		wire, err := json.Marshal(cp)
		Expect(err).NotTo(HaveOccurred())
		_, err = DecodeCheckpoint(wire)
		Expect(err).To(HaveOccurred())
	})
	DescribeTable("rejects pruning that nib discards with empty context",
		func(pruning string) {
			cp.Context, cp.History, cp.Pruning = []byte(`[]`), []byte(`[]`), []byte(pruning)
			_, err := RestoreConfig(types.Config{}, cp)
			Expect(err).To(MatchError(ContainSubstring("unsupported checkpoint state")))
			_, err = EncodeCheckpoint(cp)
			Expect(err).To(HaveOccurred())
			wire, err := json.Marshal(cp)
			Expect(err).NotTo(HaveOccurred())
			_, err = DecodeCheckpoint(wire)
			Expect(err).To(HaveOccurred())
		},
		Entry("pruned map even with an empty value", `{"pruned":{"c1":""}}`),
		Entry("compressed map even with a zero entry", `{"compressed":{"c1":{"level":0,"content":""}}}`),
		Entry("positive compression band", `{"compress_band":1}`),
		Entry("negative nonzero compression band", `{"compress_band":-1}`),
	)
	DescribeTable("round trips genuinely empty checkpoints through real isolated sessions",
		func(pruning string) {
			cp.Context, cp.History, cp.Artifacts, cp.Pruning = []byte(`[]`), []byte(`[]`), []byte(`[]`), []byte(pruning)
			wire, err := EncodeCheckpoint(cp)
			Expect(err).NotTo(HaveOccurred())
			decoded, err := DecodeCheckpoint(wire)
			Expect(err).NotTo(HaveOccurred())
			cfg, err := RestoreConfig(isolatedConfig(), decoded)
			Expect(err).NotTo(HaveOccurred())
			session, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
			Expect(err).NotTo(HaveOccurred())
			defer session.Close()
			exported, err := ExportCheckpoint(session, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(exported.Context)).To(Equal(`[]`))
			Expect(string(exported.History)).To(Equal(`[]`))
			Expect(string(exported.Artifacts)).To(Equal(`[]`))
			Expect(string(exported.Pruning)).To(Equal(`{}`))
			secondCfg, err := RestoreConfig(isolatedConfig(), exported)
			Expect(err).NotTo(HaveOccurred())
			second, err := chat.NewSession(context.Background(), secondCfg, chat.Callbacks{})
			Expect(err).NotTo(HaveOccurred())
			defer second.Close()
			again, err := ExportCheckpoint(second, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(again).To(Equal(exported))
		},
		Entry("zero pruning", `{}`),
		Entry("empty pruning maps and zero band", `{"pruned":{},"compressed":{},"compress_band":0}`),
	)
	It("rejects a real export whose system-only context leaves nonempty history", func() {
		// nib excludes system messages from ExportContext, but not display history.
		cfg := isolatedConfig()
		cfg.InitialContext = []openai.ChatCompletionMessage{{Role: "system", Content: "regenerated"}}
		cfg.InitialHistory = []openai.ChatCompletionMessage{{Role: "user", Content: "visible"}}
		session, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
		Expect(err).NotTo(HaveOccurred())
		defer session.Close()
		Expect(session.ExportContext()).To(BeEmpty())
		Expect(session.ExportHistory()).NotTo(BeEmpty())
		_, err = ExportCheckpoint(session, true)
		Expect(err).To(MatchError(ContainSubstring("unsupported checkpoint state")))
	})
	It("rejects a real export after ClearHistory leaves pruning without context", func() {
		cfg, err := RestoreConfig(isolatedConfig(), cp)
		Expect(err).NotTo(HaveOccurred())
		session, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
		Expect(err).NotTo(HaveOccurred())
		defer session.Close()
		session.ClearHistory()
		Expect(session.ExportContext()).To(BeEmpty())
		Expect(session.ExportHistory()).To(BeEmpty())
		Expect(session.ExportPruning()).To(Equal(cfg.InitialPruning))
		_, err = ExportCheckpoint(session, true)
		Expect(err).To(MatchError(ContainSubstring("unsupported checkpoint state")))
	})
	DescribeTable("rejects artifact IDs that native restore drops or overwrites before construction",
		func(artifacts string) {
			cp.Artifacts = []byte(artifacts)
			expectRejectedCheckpoint(cp, "artifact")
		},
		Entry("zero ID", `[{"id":0,"content":"lost"}]`),
		Entry("negative ID among valid artifacts", `[{"id":3,"content":"keep"},{"id":-1,"content":"lost"}]`),
		Entry("duplicate IDs with different data", `[{"id":3,"content":"first"},{"id":3,"content":"second"}]`),
		Entry("duplicate IDs even with identical data", `[{"id":3,"content":"same"},{"id":3,"content":"same"}]`),
	)
	DescribeTable("rejects regenerated system messages before construction",
		func(section, messages string) {
			if section == "context" {
				cp.Context = []byte(messages)
			} else {
				cp.History = []byte(messages)
			}
			expectRejectedCheckpoint(cp, section)
		},
		Entry("mixed context", "context", `[{"role":"user","content":"keep"},{"role":"system","content":"stale"}]`),
		Entry("system-only context with history and pruning", "context", `[{"role":"system","content":"stale"}]`),
		Entry("mixed history", "history", `[{"role":"user","content":"keep"},{"role":"system","content":"stale"}]`),
		Entry("system-only history", "history", `[{"role":"system","content":"stale"}]`),
	)
	It("rejects system-only context with pruning even without display history", func() {
		cp.Context, cp.History = []byte(`[{"role":"system","content":"stale"}]`), []byte(`[]`)
		expectRejectedCheckpoint(cp, "context")
	})
	DescribeTable("preserves distinct positive artifact IDs and native message data in real offline sessions",
		func(artifacts string) {
			cp.Artifacts = []byte(artifacts)
			wire, err := EncodeCheckpoint(cp)
			Expect(err).NotTo(HaveOccurred())
			decoded, err := DecodeCheckpoint(wire)
			Expect(err).NotTo(HaveOccurred())
			cfg, err := RestoreConfig(isolatedConfig(), decoded)
			Expect(err).NotTo(HaveOccurred())
			session, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
			Expect(err).NotTo(HaveOccurred())
			defer session.Close()
			exported, err := ExportCheckpoint(session, true)
			Expect(err).NotTo(HaveOccurred())
			restored, err := RestoreConfig(isolatedConfig(), exported)
			Expect(err).NotTo(HaveOccurred())
			Expect(restored.InitialContext).To(Equal(cfg.InitialContext))
			Expect(restored.InitialHistory).To(Equal(cfg.InitialHistory))
			Expect(restored.InitialPruning).To(Equal(cfg.InitialPruning))
			// Native snapshots sort artifacts by ID; their order is not identity.
			Expect(restored.InitialArtifacts).To(ConsistOf(cfg.InitialArtifacts))
			second, err := chat.NewSession(context.Background(), restored, chat.Callbacks{})
			Expect(err).NotTo(HaveOccurred())
			defer second.Close()
			again, err := ExportCheckpoint(second, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(again).To(Equal(exported))
		},
		Entry("sparse unordered IDs", `[{"id":9,"tool":"read","created":"1970-01-01T00:00:02Z","content":"nine"},{"id":1,"tool":"compaction","content":"one"},{"id":3,"content":"three"}]`),
		Entry("empty artifacts", `[]`),
	)
	It("captures immutable identities even for concurrent late callbacks", func() {
		var mu sync.Mutex
		var events []Event
		sink := func(e Event) { mu.Lock(); defer mu.Unlock(); events = append(events, e) }
		id := Identity{Principal: "p", Conversation: "c", Turn: "t", Attempt: 1, Epoch: 2}
		a, err := NewCallbacks(id, func(Identity, chat.ToolCallRequest) error { return nil }, sink, nil)
		Expect(err).NotTo(HaveOccurred())
		id.Turn = "next"
		id.Attempt = 2
		id.Epoch = 3
		b, err := NewCallbacks(id, func(Identity, chat.ToolCallRequest) error { return nil }, sink, nil)
		Expect(err).NotTo(HaveOccurred())
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); a.OnResponse("late") }()
		go func() { defer wg.Done(); b.OnResponse("new") }()
		wg.Wait()
		Expect(events).To(ConsistOf(Event{Identity: Identity{Principal: "p", Conversation: "c", Turn: "t", Attempt: 1, Epoch: 2}, Kind: Text, Text: "late"}, Event{Identity: id, Kind: Text, Text: "new"}))
	})
	It("projects actual callback types including stream tools helpers compaction and failures", func() {
		var events []Event
		cb, err := NewCallbacks(Identity{"p", "c", "t", 1, 1}, func(Identity, chat.ToolCallRequest) error { return nil }, func(e Event) { events = append(events, e) }, nil)
		Expect(err).NotTo(HaveOccurred())
		cb.OnStatus("working")
		cb.OnReasoning("think")
		cb.OnStepContent("step")
		cb.OnResponse("reply")
		cb.OnError(errors.New("failed"))
		cb.OnStream(chat.StreamEvent{Kind: "content", Content: "delta", AgentID: "helper"})
		cb.OnToolStart(chat.ToolStart{Name: "read", Arguments: "{}"})
		cb.OnToolResult(chat.ToolResult{Name: "read", Result: "data", AgentID: "h"})
		cb.OnAgentEvent(chat.AgentEvent{ID: "h", Status: chat.AgentStatusCompleted, Result: "done"})
		cb.OnAgentTitle("h", "title")
		cb.OnCompactDone(10, 5)
		cb.OnCompactFailed(errors.New("compact"))
		cb.OnPruneDone(2, 4)
		cb.OnParked("park")
		cb.OnResumed()
		Expect(events).To(HaveLen(15))
		Expect(events[0].Kind).To(Equal(Status))
		Expect(events[1].Kind).To(Equal(Reasoning))
		Expect(events[4].Err).To(MatchError("failed"))
		Expect(events[5].Stream).To(Equal(chat.StreamEvent{Kind: "content", Content: "delta", AgentID: "helper"}))
		Expect(events[6].ToolName).To(Equal("read"))
		Expect(events[7].Text).To(Equal("data"))
		Expect(events[8].Helper.ID).To(Equal("h"))
		Expect(events[10].Before).To(Equal(10))
		Expect(events[11].Err).To(MatchError("compact"))
		Expect(events[12].After).To(Equal(4))
	})
	It("fails closed on missing policy, validates identity, and never mints broad grants", func() {
		id := Identity{"p", "c", "t", 1, 1}
		_, err := NewCallbacks(id, nil, func(Event) {}, nil)
		Expect(err).To(MatchError(ErrPolicyRequired))
		_, err = NewCallbacks(Identity{}, func(Identity, chat.ToolCallRequest) error { return nil }, func(Event) {}, nil)
		Expect(err).To(HaveOccurred())
		denied, err := NewCallbacks(id, func(got Identity, req chat.ToolCallRequest) error {
			Expect(got).To(Equal(id))
			Expect(req.Name).To(Equal("read"))
			return errors.New("ACL denied")
		}, func(Event) {}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(denied.OnToolCall(chat.ToolCallRequest{Name: "read"})).To(Equal(chat.ToolCallResponse{Adjustment: "ACL denied"}))
		allowed, err := NewCallbacks(id, func(Identity, chat.ToolCallRequest) error { return nil }, func(Event) {}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(allowed.OnToolCall(chat.ToolCallRequest{})).To(Equal(chat.ToolCallResponse{Approved: true}))
		Expect(allowed.OnAskUser(chat.AskRequest{})).To(Equal("Question handler unavailable"))
	})
	It("passes questions with stable host identity and separately owned options", func() {
		id := Identity{"p", "c", "t", 1, 1}
		options := []string{"one"}
		cb, err := NewCallbacks(id, func(Identity, chat.ToolCallRequest) error { return nil }, func(Event) {}, func(got Identity, req chat.AskRequest) string {
			Expect(got).To(Equal(id))
			req.Options[0] = "changed"
			return "answer"
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(cb.OnAskUser(chat.AskRequest{Question: "which", Options: options})).To(Equal("answer"))
		Expect(options).To(Equal([]string{"one"}))
	})
	It("does not reuse or mutate caller config buffers while restoring", func() {
		base := types.Config{InitialHistory: []openai.ChatCompletionMessage{{Role: "user", Content: "original"}}, InitialPruning: types.PruningState{Pruned: map[string]string{"unrelated": "keep"}}}
		restored, err := RestoreConfig(base, cp)
		Expect(err).NotTo(HaveOccurred())
		Expect(base.InitialHistory[0].Content).To(Equal("original"))
		Expect(base.InitialPruning.Pruned).To(Equal(map[string]string{"unrelated": "keep"}))
		Expect(restored.InitialPruning.Pruned).NotTo(HaveKey("unrelated"))
	})

	It("uses the actual nib config JSON protocol rather than a display-only substitute", func() {
		cfg, err := RestoreConfig(types.Config{}, cp)
		Expect(err).NotTo(HaveOccurred())
		wire, err := json.Marshal(cfg.InitialContext)
		Expect(err).NotTo(HaveOccurred())
		var messages []openai.ChatCompletionMessage
		Expect(json.Unmarshal(wire, &messages)).To(Succeed())
		Expect(messages[1].ToolCalls[0].Function.Arguments).To(Equal("{}"))
	})
})

// No turns, transports, providers, or tools are invoked by these session tests.
func isolatedConfig() types.Config {
	home := GinkgoT().TempDir()
	GinkgoT().Setenv("HOME", home)
	GinkgoT().Setenv("XDG_CONFIG_HOME", home)
	GinkgoT().Setenv("XDG_STATE_HOME", home)
	off := false
	return types.Config{Model: "fake", APIKey: "fake", BaseURL: "http://127.0.0.1:1/v1", BaseDir: home, WorkingDir: home, LSPAutoDetect: &off, BuiltinTools: []string{"no-tools"}}
}

// Only validated configs may reach chat.NewSession. Exercise every checkpoint
// admission path without constructing any session for a rejected payload.
func expectRejectedCheckpoint(cp Checkpoint, section string) {
	cfg, err := RestoreConfig(types.Config{}, cp)
	Expect(err).To(MatchError(ContainSubstring(section)))
	Expect(cfg).To(Equal(types.Config{}))
	_, err = EncodeCheckpoint(cp)
	Expect(err).To(HaveOccurred())
	wire, err := json.Marshal(cp)
	Expect(err).NotTo(HaveOccurred())
	_, err = DecodeCheckpoint(wire)
	Expect(err).To(HaveOccurred())
}
