// SPDX-License-Identifier: MIT
package nibharness_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio/nibharness"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("production tool invocation identity", func() {
	It("preserves the engine call ID on an automatic retry after a lost response", func() {
		llm := &callingLLM{call: "add_memory"}
		var mu sync.Mutex
		var ids []string
		h := nibharness.New(nibharness.Config{LLM: func(workspace.Actor) (nibharness.Model, error) {
			return nibharness.Model{LLM: llm, Name: "host-model", Agent: nibharness.AgentOptions{MaxAttempts: 2}}, nil
		}})
		hs, err := h.Start(context.Background(), session.HarnessEnv{
			Dir: GinkgoT().TempDir(), Actor: workspace.Actor{Principal: "alice", ChatID: "c1"},
			Emit: func(session.Event) {},
			Tools: []session.Tool{{Name: "add_memory", Description: "record memory", Schema: json.RawMessage(`{"type":"object"}`),
				Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
					mu.Lock()
					defer mu.Unlock()
					ids = append(ids, session.ToolInvocation(ctx))
					if len(ids) == 1 {
						return "", errors.New("response lost after write")
					}
					return "saved", nil
				}}},
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = hs.Close() })
		send(hs, "remember this")
		mu.Lock()
		defer mu.Unlock()
		Expect(ids).To(Equal([]string{"call-1", "call-1"}), "all attempts must use the actual engine-owned identity")
	})
})
