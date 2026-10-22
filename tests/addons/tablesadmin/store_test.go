// SPDX-License-Identifier: MIT
package tablesadmin_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/addons/resources"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

var _ = Describe("the conversation selection store", func() {
	open := func(dir string, o resources.SelectionOptions) *resources.SelectionStore {
		GinkgoHelper()
		s, e := resources.OpenSelections(dir, o)
		Expect(e).NotTo(HaveOccurred())
		return s
	}

	It("keys a reference by user, conversation and add-on, bounds it per user and in total and expires what is unused", func() {
		dir := GinkgoT().TempDir()
		c := &clock{t: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
		s := open(dir, resources.SelectionOptions{TTL: time.Hour, MaxPerUser: 3, MaxTotal: 5, Now: c.now})
		DeferCleanup(s.Close)

		Expect(s.Put("u1", "c1", "tables", "rA")).To(Succeed())
		Expect(s.Put("u2", "c1", "tables", "rB")).To(Succeed())
		c.advance(time.Second)
		Expect(s.Put("u1", "c1", "notes", "rC")).To(Succeed())
		get := func(u, c, a string) (string, bool) {
			GinkgoHelper()
			id, ok, e := s.Get(u, c, a)
			Expect(e).NotTo(HaveOccurred())
			return id, ok
		}
		for key, want := range map[[3]string]string{{"u1", "c1", "tables"}: "rA", {"u2", "c1", "tables"}: "rB", {"u1", "c1", "notes"}: "rC"} {
			got, ok := get(key[0], key[1], key[2])
			Expect(ok).To(BeTrue(), fmt.Sprint(key))
			Expect(got).To(Equal(want))
		}
		_, ok := get("u3", "c1", "tables")
		Expect(ok).To(BeFalse())
		_, ok = get("u1", "c2", "tables")
		Expect(ok).To(BeFalse())

		// A user over the bound loses the entry used least recently, and the
		// entry just stored stays.
		c.advance(time.Minute)
		Expect(s.Put("u1", "c2", "tables", "rD")).To(Succeed())
		c.advance(time.Minute)
		Expect(s.Put("u1", "c3", "tables", "rE")).To(Succeed())
		_, ok = get("u1", "c1", "tables")
		Expect(ok).To(BeFalse(), "the oldest of the user is evicted")
		_, ok = get("u1", "c3", "tables")
		Expect(ok).To(BeTrue())
		_, ok = get("u2", "c1", "tables")
		Expect(ok).To(BeTrue(), "another user's entry is never evicted for this user")

		// The total bound refuses once it is full of live entries.
		Expect(s.Put("u4", "c1", "tables", "r1")).To(Succeed())
		err := s.Put("u5", "c1", "tables", "r2")
		Expect(errors.Is(err, resources.ErrSelectionStore)).To(BeTrue())

		// Unused entries expire, and an expired one never resolves.
		c.advance(2 * time.Hour)
		_, ok = get("u2", "c1", "tables")
		Expect(ok).To(BeFalse())
		Expect(s.Put("u6", "c1", "tables", "r3")).To(Succeed())
		Expect(s.Len()).To(Equal(1), "expired entries are removed by the next write")
		// Delete is idempotent.
		Expect(s.Delete("u6", "c1", "tables")).To(Succeed())
		Expect(s.Delete("u6", "c1", "tables")).To(Succeed())
		_, ok = get("u6", "c1", "tables")
		Expect(ok).To(BeFalse())
	})

	It("is durable, held by one process, never resets a corrupt file and stops writing after a failed sync", func() {
		dir := GinkgoT().TempDir()
		s := open(dir, resources.SelectionOptions{})
		Expect(s.Put("u1", "c1", "tables", "rA")).To(Succeed())
		_, e := resources.OpenSelections(dir, resources.SelectionOptions{})
		Expect(e).To(HaveOccurred(), "one process holds the store")
		Expect(s.Close()).To(Succeed())
		Expect(s.Put("u1", "c2", "tables", "rB")).NotTo(Succeed(), "nothing is written after Close")

		s = open(dir, resources.SelectionOptions{})
		id, ok, e := s.Get("u1", "c1", "tables")
		Expect(e).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(id).To(Equal("rA"))
		Expect(s.Close()).To(Succeed())

		// A corrupt file is an error and is left as it is.
		file := filepath.Join(dir, "selections.json")
		Expect(os.WriteFile(file, []byte(`{"version":1,"entries":[{"user":"u1"`), 0o600)).To(Succeed())
		_, e = resources.OpenSelections(dir, resources.SelectionOptions{})
		Expect(e).To(HaveOccurred())
		raw, _ := os.ReadFile(file)
		Expect(string(raw)).To(Equal(`{"version":1,"entries":[{"user":"u1"`))
		for _, bad := range []string{`{"version":2,"entries":[]}`, `{"version":1,"entries":[],"extra":1}`, `{"version":1,"entries":[{"user":"","conversation":"c","addon":"a","resourceId":"r","usedAt":"2026-01-01T00:00:00Z"}]}`, `{"version":1,"entries":[]} {"version":1}`} {
			Expect(os.WriteFile(file, []byte(bad), 0o600)).To(Succeed())
			_, e = resources.OpenSelections(dir, resources.SelectionOptions{})
			Expect(e).To(HaveOccurred(), bad)
		}
		Expect(os.Remove(file)).To(Succeed())

		// A change that reached the directory but could not be made durable is
		// kept in memory and stops further writes until the store reopens.
		fail := errors.New("directory sync failed")
		failing := false
		s = open(dir, resources.SelectionOptions{SyncDirectory: func(f *os.File) error {
			if failing {
				return fail
			}
			return f.Sync()
		}})
		Expect(s.Put("u1", "c1", "tables", "rA")).To(Succeed())
		failing = true
		Expect(s.Put("u1", "c2", "tables", "rB")).NotTo(Succeed())
		failing = false
		Expect(s.Put("u1", "c3", "tables", "rC")).NotTo(Succeed(), "a failed write stops further writes")
		Expect(s.Delete("u1", "c1", "tables")).NotTo(Succeed())
		Expect(s.Close()).To(Succeed())
		s = open(dir, resources.SelectionOptions{})
		DeferCleanup(s.Close)
		Expect(s.Put("u1", "c3", "tables", "rC")).To(Succeed())
	})

	It("serves concurrent users without losing a reference", func() {
		s := open(GinkgoT().TempDir(), resources.SelectionOptions{})
		DeferCleanup(s.Close)
		var wg sync.WaitGroup
		for u := 0; u < 8; u++ {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				user := fmt.Sprintf("user-%d", u)
				for c := 0; c < 10; c++ {
					Expect(s.Put(user, fmt.Sprintf("conv-%d", c), "tables", fmt.Sprintf("r%d-%d", u, c))).To(Succeed())
				}
			}()
		}
		wg.Wait()
		for u := 0; u < 8; u++ {
			for c := 0; c < 10; c++ {
				id, ok, e := s.Get(fmt.Sprintf("user-%d", u), fmt.Sprintf("conv-%d", c), "tables")
				Expect(e).NotTo(HaveOccurred())
				Expect(ok).To(BeTrue())
				Expect(id).To(Equal(fmt.Sprintf("r%d-%d", u, c)))
			}
		}
		Expect(s.Len()).To(Equal(80))
	})
})

var _ = Describe("the tombstones of finished deletions", func() {
	It("keeps the newest bounded number, validates them on load and remaps them with the owner", func() {
		dir := GinkgoT().TempDir()
		tombstones := map[string]any{}
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for i := 0; i < policy.MaxTombstones; i++ {
			tombstones[fmt.Sprintf("rold%04d", i)] = map[string]any{"addonId": "tables", "owner": "bob", "op": fmt.Sprintf("op-%d", i), "at": base.Add(time.Duration(i) * time.Minute)}
		}
		write := func(deleted map[string]any) {
			GinkgoHelper()
			raw, e := json.Marshal(map[string]any{"version": 1, "mode": "named-user", "resources": map[string]any{}, "deleted": deleted})
			Expect(e).NotTo(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(dir, "policy.json"), raw, 0o600)).To(Succeed())
		}
		write(tombstones)
		st, e := policy.OpenStore(dir, policy.ModeNamedUser)
		Expect(e).NotTo(HaveOccurred())
		p := policy.New(st, policy.Options{
			Revalidate:      func(context.Context, contract.Actor) error { return nil },
			CreateAuthority: func(context.Context, contract.Actor, string) error { return nil },
		})
		ctx := context.Background()
		bob := contract.Actor{UserID: "bob", CredentialID: "session:bob", Mode: "named-user"}
		key, e := p.Register(ctx, bob, "tables")
		Expect(e).NotTo(HaveOccurred())
		Expect(p.Commit(ctx, bob, key)).To(Succeed())
		Expect(p.BeginDelete(ctx, bob, key, "op-new")).To(Succeed())
		Expect(p.ConfirmDelete(key)).To(Succeed())
		Expect(p.FinishDelete(key)).To(Succeed())

		got, e := p.DeletedBy(ctx, bob, key.ResourceID, "op-new")
		Expect(e).NotTo(HaveOccurred())
		Expect(got).To(BeTrue())
		got, _ = p.DeletedBy(ctx, bob, "rold0000", "")
		Expect(got).To(BeFalse(), "the oldest tombstone was dropped")
		got, _ = p.DeletedBy(ctx, bob, "rold0001", "op-1")
		Expect(got).To(BeTrue())
		got, _ = p.DeletedBy(ctx, bob, "rold0001", "op-2")
		Expect(got).To(BeFalse(), "another operation is not the deletion")
		carol := contract.Actor{UserID: "carol", CredentialID: "session:carol", Mode: "named-user"}
		got, _ = p.DeletedBy(ctx, carol, key.ResourceID, "")
		Expect(got).To(BeFalse(), "only the owner it had learns it")
		Expect(st.Close()).To(Succeed())
		var doc struct {
			Deleted map[string]any `json:"deleted"`
		}
		raw, e := os.ReadFile(filepath.Join(dir, "policy.json"))
		Expect(e).NotTo(HaveOccurred())
		Expect(json.Unmarshal(raw, &doc)).To(Succeed())
		Expect(doc.Deleted).To(HaveLen(policy.MaxTombstones))

		// A corrupt tombstone or one too many is a corrupt store, never reset.
		for name, bad := range map[string]map[string]any{
			"an empty owner": {"r1": map[string]any{"addonId": "tables", "owner": "", "op": "o", "at": base}},
			"no operation":   {"r1": map[string]any{"addonId": "tables", "owner": "bob", "op": "", "at": base}},
		} {
			write(bad)
			_, e = policy.OpenStore(dir, policy.ModeNamedUser)
			Expect(e).To(HaveOccurred(), name)
		}
		tombstones["rextra"] = map[string]any{"addonId": "tables", "owner": "bob", "op": "o", "at": base}
		write(tombstones)
		_, e = policy.OpenStore(dir, policy.ModeNamedUser)
		Expect(e).To(HaveOccurred(), "more than the bound")
	})
})

var _ = Describe("the manifest declaration of admin tools and resource tools", func() {
	manifest := func() map[string]any {
		GinkgoHelper()
		raw, e := os.ReadFile("../../../addons/tables/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		var m map[string]any
		Expect(json.Unmarshal(raw, &m)).To(Succeed())
		return m
	}
	caps := func(m map[string]any) map[string]any { return m["capabilities"].(map[string]any) }
	parse := func(m map[string]any) error {
		GinkgoHelper()
		b, e := json.Marshal(m)
		Expect(e).NotTo(HaveOccurred())
		_, e = contract.ParseManifest(b)
		return e
	}

	It("keeps the delete tool out of the model tools and their definitions", func() {
		m := manifest()
		Expect(parse(m)).To(Succeed())
		c := caps(m)
		Expect(c["adminTools"]).To(ConsistOf("databases-delete"))
		Expect(c["tools"]).NotTo(ContainElement("databases-delete"))
		for _, d := range c["toolDefinitions"].([]any) {
			Expect(d.(map[string]any)["name"]).NotTo(Equal("databases-delete"))
		}
	})

	It("refuses an admin tool that is also a model tool, has a definition, or is not mapped", func() {
		bad := map[string]func(map[string]any){
			"an admin tool that is a model tool": func(c map[string]any) {
				c["adminTools"] = []any{"databases-rename"}
				c["resources"].(map[string]any)["delete"] = "databases-rename"
			},
			"an admin tool with a model definition": func(c map[string]any) {
				defs := c["toolDefinitions"].([]any)
				c["toolDefinitions"] = append(defs, map[string]any{"name": "databases-delete", "description": "Delete.", "kind": "schema", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"resourceId": map[string]any{"type": "string"}}, "required": []any{"resourceId"}, "additionalProperties": false}})
			},
			"an admin tool nothing maps":       func(c map[string]any) { delete(c["resources"].(map[string]any), "delete") },
			"admin tools without a mapping":    func(c map[string]any) { delete(c, "resources") },
			"a delete mapping to a model tool": func(c map[string]any) { c["resources"].(map[string]any)["delete"] = "rows-delete" },
			"a delete mapping that is not declared": func(c map[string]any) {
				c["adminTools"] = []any{"databases-purge"}
				c["resources"].(map[string]any)["delete"] = "databases-delete"
			},
			"a create mapping to a read tool":        func(c map[string]any) { c["resources"].(map[string]any)["create"] = "databases-inspect" },
			"a rename mapping to an undeclared tool": func(c map[string]any) { c["resources"].(map[string]any)["rename"] = "databases-copy" },
			"an inspect mapping to a write tool":     func(c map[string]any) { c["resources"].(map[string]any)["inspect"] = "rows-insert" },
			"two admin tools":                        func(c map[string]any) { c["adminTools"] = []any{"databases-delete", "databases-purge"} },
			"a markup noun":                          func(c map[string]any) { c["resources"].(map[string]any)["noun"] = "<b>database</b>" },
			"an empty mapping":                       func(c map[string]any) { c["resources"] = map[string]any{}; c["adminTools"] = []any{} },
		}
		for name, mutate := range bad {
			m := manifest()
			mutate(caps(m))
			Expect(parse(m)).NotTo(Succeed(), name)
		}
		// A manifest with no admin tools and no mapping stays valid.
		m := manifest()
		delete(caps(m), "adminTools")
		delete(caps(m), "resources")
		Expect(parse(m)).To(Succeed())
	})
})
