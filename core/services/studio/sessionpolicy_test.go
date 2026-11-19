// SPDX-License-Identifier: MIT
package studio_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type unavailablePolicyConnectors struct{}

func (unavailablePolicyConnectors) List(context.Context, string) ([]mcpconns.Connector, error) {
	return nil, errors.New("optional connector unavailable")
}
func (unavailablePolicyConnectors) Resolve(context.Context, string, []string, bool) ([]mcpconns.Tool, []string) {
	return nil, nil
}

var _ = Describe("mandatory policy on session turns", func() {
	It("blocks an actual sent turn before model send or tool execution when the owner becomes unavailable", func() {
		var unavailable atomic.Bool
		var outageReads atomic.Int32
		ownerErr := errors.New("owner policy unavailable")
		svc, h := newToolService(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "search", Args: `{}`}, {Say: "done"}}
		}, func(d *studio.Deps) {
			d.ToolPins = func(context.Context) (map[string]string, error) {
				// A chat that reopens for its next turn is not primed again, so
				// the first read after the outage is the turn hook's.
				if unavailable.Load() && outageReads.Add(1) > 0 {
					return nil, fmt.Errorf("read owner: %w", ownerErr)
				}
				return nil, nil
			}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "normal control")
		idle(svc, alice, id)
		Expect(h.Sent(id)).To(HaveLen(1))
		Expect(h.Results()).To(HaveLen(1))
		events, cancel, err := svc.Events(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		defer cancel()
		unavailable.Store(true)
		sent(svc, alice, id, "must not reach the model")
		var fatal session.Event
		Eventually(func() bool {
			for {
				select {
				case e := <-events:
					if e.Kind == session.KindError && e.Fatal {
						fatal = e
					}
					if e.Kind == session.KindTurnEnd {
						return true
					}
				default:
					return false
				}
			}
		}).Should(BeTrue())
		Expect(h.Sent(id)).To(HaveLen(1), "mandatory policy failure must prevent harness Send")
		Expect(h.Results()).To(HaveLen(1), "mandatory policy failure must prevent tool execution")
		Expect(fatal.Fatal).To(BeTrue(), "a terminal error must be visible to the caller")
		Expect(fatal.Detail).To(ContainSubstring("owner policy unavailable"))
		Expect(turn(svc, alice, id).Last).To(Equal(store.TurnFailed))
	})

	It("rejects priming before starting the harness and preserves the owner cause", func() {
		ownerErr := errors.New("owner policy unavailable")
		svc, h := newToolService(sayOK, func(d *studio.Deps) {
			d.ToolPins = func(context.Context) (map[string]string, error) { return nil, fmt.Errorf("read owner: %w", ownerErr) }
		})
		id := create(svc, alice)
		_, err := svc.Send(context.Background(), alice, id, "must not start")
		Expect(errors.Is(err, ownerErr)).To(BeTrue(), "priming must return the mandatory policy cause, got %v", err)
		Expect(h.ToolNames(id)).To(BeNil(), "priming must fail before harness Start")
		Expect(h.Sent(id)).To(BeEmpty())
		Expect(h.Results()).To(BeEmpty())
	})

	It("keeps optional connector failures degradable on a sent turn", func() {
		svc, h := newToolService(sayOK, func(d *studio.Deps) { d.Connectors = unavailablePolicyConnectors{} })
		id := create(svc, alice)
		sent(svc, alice, id, "optional control")
		idle(svc, alice, id)
		Expect(h.Sent(id)).To(HaveLen(1))
		Expect(turn(svc, alice, id).Last).To(Equal(store.TurnDone))
	})
})
