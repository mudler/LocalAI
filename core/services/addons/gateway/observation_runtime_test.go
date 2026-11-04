//go:build linux

// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Only lookup ordering is instrumented. Every returned client belongs to the
// real supervisor; admission, withdrawal and protocol dispatch are not mocked.
type observationLookup struct {
	supervisor *addonruntime.Supervisor
	calls      atomic.Int32
	captured   chan addonruntime.Instance
	resume     chan struct{}
	pauseAt    int32
}

func (s *observationLookup) Runtime(ctx context.Context, id string) (addonruntime.Instance, error) {
	n := s.calls.Add(1)
	if n == s.pauseAt && n == 2 {
		// The first real /read has completed; pause before owner consumption.
		s.captured <- addonruntime.Instance{}
		select {
		case <-s.resume:
		case <-ctx.Done():
			return addonruntime.Instance{}, ctx.Err()
		}
	}
	i, e := s.supervisor.Current(ctx, id, 1)
	if n == s.pauseAt && n == 1 && e == nil {
		s.captured <- i
		select {
		case <-s.resume:
		case <-ctx.Done():
			return addonruntime.Instance{}, ctx.Err()
		}
	}
	return i, e
}

type observationRecord struct {
	Incarnation string            `json:"incarnation"`
	Envelope    contract.Envelope `json:"envelope"`
}

var _ = Describe("supervised owner observation over authenticated carrier", func() {
	var binary string
	BeforeEach(func() {
		binary = filepath.Join(GinkgoT().TempDir(), "fixture")
		cmd := exec.Command("go", "build", "-o", binary, "../../../../tests/addons/fixture/cmd")
		out, e := cmd.CombinedOutput()
		Expect(e).NotTo(HaveOccurred(), string(out))
	})
	setup := func(pauseAt int32) (*addonruntime.Supervisor, packages.PackageReceipt, *observationLookup, *Gateway, ExecutionStatusRequest, string) {
		ctx := context.Background()
		caps, e := (observationCaps{}).DeclaredCapabilities(ctx, "sample", 1)
		Expect(e).NotTo(HaveOccurred())
		caps.Service = true
		caps.Actions = true
		for i := range caps.ToolDefinitions {
			caps.ToolDefinitions[i].Description = "Fixture tool"
			if len(caps.ToolDefinitions[i].InputSchema) == 0 {
				caps.ToolDefinitions[i].InputSchema = json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`)
			}
		}
		rawCaps, e := json.Marshal(caps)
		Expect(e).NotTo(HaveOccurred())
		var manifest map[string]any
		Expect(json.Unmarshal([]byte(uiManifest), &manifest)).To(Succeed())
		manifest["capabilities"] = json.RawMessage(rawCaps)
		rawManifest, e := json.Marshal(manifest)
		Expect(e).NotTo(HaveOccurred())
		b, e := os.ReadFile(binary)
		Expect(e).NotTo(HaveOccurred())
		logPath := filepath.Join(GinkgoT().TempDir(), "observations.jsonl")
		config, e := json.Marshal(map[string]string{"mode": "execution-status", "observationLog": logPath})
		Expect(e).NotTo(HaveOccurred())
		m := ownerManagerPackage(frontActor, true, string(rawManifest), map[string]string{"bin/sample": string(b), "fixture.json": string(config)}, 1)
		s, e := addonruntime.NewSupervisor(addonruntime.Config{StateRoot: GinkgoT().TempDir(), Limits: contract.DefaultLimits(), ReadyTimeout: 2 * time.Second, StableHealth: time.Second})
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(s.Close(ctx)).To(Succeed()) })
		Expect(m.AttachRuntime(ctx, s)).To(Succeed())
		_, e = m.Submit(ctx, frontActor, addons.LifecycleCommand{AddonID: "sample", OperationID: "enable", Action: "enable", Generation: 1})
		Expect(e).NotTo(HaveOccurred())
		first, e := s.Current(ctx, "sample", 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(first.Client).To(BeAssignableToTypeOf(&addonruntime.Client{}))
		// The installed receipt, not a separately invented replacement, starts again.
		st, e := m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Generations).To(HaveLen(1))
		p := st.Generations[0].Receipt
		Expect(p.Directory).NotTo(BeEmpty())
		lookup := &observationLookup{supervisor: s, captured: make(chan addonruntime.Instance, 1), resume: make(chan struct{}), pauseAt: pauseAt}
		g := &Gateway{Manager: m, Runtime: lookup, Admission: &addonAdmitted{}, Revalidate: func(_ context.Context, a contract.Actor) error {
			if a != frontActor {
				return ErrForbidden
			}
			return nil
		}, AuthorizeExecutionStatus: func(_ context.Context, a contract.Actor, b ExecutionStatusBinding) error {
			if a != frontActor || b.CurrentSelection != "rev" {
				return ErrForbidden
			}
			return nil
		}}
		_, cs := carrierFixture(func(c *Carrier) http.Handler { h, e := c.OwnerHandler(g); Expect(e).NotTo(HaveOccurred()); return h })
		front := &Gateway{Forward: cs["front-a"], Revalidate: g.Revalidate}
		r := ExecutionStatusRequest{Version: 1, Binding: ExecutionStatusBinding{Actor: frontActor, AddonID: "sample", Generation: 1, Incarnation: first.Incarnation, ExecutionTool: "run", StatusTool: "health", CurrentSelection: "rev"}, ObservationID: "q"}
		return s, p, lookup, front, r, logPath
	}
	records := func(path string) []observationRecord {
		f, e := os.Open(path)
		if os.IsNotExist(e) {
			return nil
		}
		Expect(e).NotTo(HaveOccurred())
		defer func() { Expect(f.Close()).To(Succeed()) }()
		d := json.NewDecoder(f)
		var out []observationRecord
		for d.More() {
			var r observationRecord
			Expect(d.Decode(&r)).To(Succeed())
			out = append(out, r)
		}
		return out
	}
	restart := func(s *addonruntime.Supervisor, p packages.PackageReceipt) addonruntime.Instance {
		old, e := s.Current(context.Background(), "sample", 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(s.Stop(context.Background(), old)).To(Succeed())
		next, e := s.Start(context.Background(), p, 1)
		Expect(e).NotTo(HaveOccurred())
		current, e := s.Current(context.Background(), "sample", 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(current.Client).To(BeIdenticalTo(next.Client))
		Expect(current.Client).NotTo(BeIdenticalTo(old.Client))
		Expect(current.Incarnation).NotTo(Equal(old.Incarnation))
		return next
	}
	positive := func(front *Gateway, r ExecutionStatusRequest, path string) {
		out, e := front.ExecutionStatusRead(context.Background(), frontActor, r)
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Binding).To(Equal(r.Binding))
		Expect(out.ObservationID).To(Equal(r.ObservationID))
		Expect(out.Status).To(Equal(contract.ExecutionStatus{Readiness: "available", Isolation: "isolated"}))
		rows := records(path)
		Expect(rows).NotTo(BeEmpty())
		row := rows[len(rows)-1]
		Expect(row.Incarnation).To(Equal(r.Binding.Incarnation))
		Expect(row.Envelope.Authority.Actor).To(Equal(frontActor))
		Expect(string(row.Envelope.Request.Arguments)).To(Equal(`{"tool":"health"}`))
		Expect(row.Envelope.Request.OperationID).To(HavePrefix("obs-"))
		Expect(row.Envelope.Request.OperationID).NotTo(Equal(r.ObservationID))
		Expect(row.Envelope.Request.ResourceID).To(BeEmpty())
	}
	It("reads typed status and preserves named tool and host operation IDs through a supervised process", func() {
		_, _, _, front, r, path := setup(0)
		positive(front, r, path)
		Expect(records(path)).To(HaveLen(1))
	})
	for _, tc := range []struct {
		name  string
		pause int32
		reads int
	}{
		{"rejects a captured supervised client after same generation replacement before carrier dispatch", 1, 0},
		{"rejects a late supervised carrier observation after replacement before owner consumption", 2, 1},
	} {
		tc := tc
		It(tc.name, func() {
			s, p, lookup, front, r, path := setup(tc.pause)
			type result struct {
				out ExecutionStatusResponse
				err error
			}
			done := make(chan result, 1)
			go func() {
				out, e := front.ExecutionStatusRead(context.Background(), frontActor, r)
				done <- result{out, e}
			}()
			var captured addonruntime.Instance
			Eventually(lookup.captured, 3*time.Second).Should(Receive(&captured))
			if tc.pause == 1 {
				Expect(captured.Incarnation).To(Equal(r.Binding.Incarnation))
			}
			next := restart(s, p)
			close(lookup.resume)
			var got result
			Eventually(done, 3*time.Second).Should(Receive(&got))
			Expect(got.err).To(HaveOccurred())
			Expect(got.out.Status.Readiness).To(Equal("unknown"))
			Expect(got.out.Status.Isolation).To(Equal("unknown"))
			rows := records(path)
			Expect(rows).To(HaveLen(tc.reads))
			for _, row := range rows {
				Expect(row.Incarnation).To(Equal(r.Binding.Incarnation))
			}
			r.Binding.Incarnation = next.Incarnation
			r.ObservationID = "fresh-q"
			positive(front, r, path)
			Expect(records(path)).To(HaveLen(tc.reads + 1))
		})
	}
})
