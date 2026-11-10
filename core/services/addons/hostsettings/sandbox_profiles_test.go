// SPDX-License-Identifier: MIT
package hostsettings

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The sandbox profile policy", func() {
	var dir, path string
	var s *Store
	ctx := context.Background()
	BeforeEach(func() {
		var err error
		dir, err = os.MkdirTemp("", "sbprofiles-")
		Expect(err).NotTo(HaveOccurred())
		path = filepath.Join(dir, "policy.json")
		s, err = Open(path, legacy())
		Expect(err).NotTo(HaveOccurred())
	})
	AfterEach(func() {
		if s != nil {
			Expect(s.Close()).To(Succeed())
		}
		Expect(os.RemoveAll(dir)).To(Succeed())
	})
	patch := func(body string) Patch {
		var p Patch
		ExpectWithOffset(1, json.Unmarshal([]byte(body), &p)).To(Succeed(), body)
		return p
	}

	It("is open until the owner restricts it, and the policy always carries the key", func() {
		snap, err := s.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		p := snap.Policy.SandboxPolicy()
		Expect(p.Allows("heavy", "alice", "user")).To(BeTrue())
		Expect(p.DefaultProfile()).To(Equal("media"))
		raw, _ := json.Marshal(snap.Policy)
		Expect(string(raw)).To(ContainSubstring(`"sandbox_profiles":{"default":"","access":{},"memory_mib":0,"disk_mib":0,"pin_previous":[]}`))
	})

	It("is set as a whole, survives a reopen and is cleared by an empty object", func() {
		body := `{"sandbox_profiles":{"default":"media","access":{"heavy":{"roles":["admin"],"users":["carol"]}},"memory_mib":4096,"disk_mib":20480,"pin_previous":["media"]}}`
		snap, err := s.Update(ctx, patch(body), 1, "set-profiles")
		Expect(err).NotTo(HaveOccurred())
		p := snap.Policy.SandboxPolicy()
		Expect(p.DefaultProfile()).To(Equal("media"))
		Expect(p.Allows("heavy", "alice", "user")).To(BeFalse())
		Expect(p.Allows("heavy", "carol", "user")).To(BeTrue())
		Expect(p.Allows("heavy", "root", "admin")).To(BeTrue())
		Expect(p.MemoryMiB).To(Equal(4096))
		Expect(p.Pinned("media")).To(BeTrue())
		Expect(s.Close()).To(Succeed())
		s, err = Open(path, nil)
		Expect(err).NotTo(HaveOccurred())
		snap, err = s.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Policy.SandboxPolicy()).To(Equal(p))
		snap, err = s.Update(ctx, patch(`{"sandbox_profiles":{}}`), 2, "clear-profiles")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Policy.SandboxPolicy().Allows("heavy", "alice", "user")).To(BeTrue())
	})

	It("leaves the policy alone when a patch names other keys", func() {
		_, err := s.Update(ctx, patch(`{"sandbox_profiles":{"default":"media"}}`), 1, "a")
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Update(ctx, patch(`{"auto_upgrade_addons":true}`), 2, "b")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Policy.SandboxPolicy().Default).To(Equal("media"))
	})

	It("refuses an invalid policy and keeps the revision", func() {
		for i, body := range []string{
			`{"sandbox_profiles":{"default":"Not A Profile"}}`,
			`{"sandbox_profiles":{"access":{"media":{"roles":["root"]}}}}`,
			`{"sandbox_profiles":{"memory_mib":3}}`,
			`{"sandbox_profiles":{"unknown":1}}`,
			`{"sandbox_profiles":{"pin_previous":["media","media"]}}`,
		} {
			_, err := s.Update(ctx, patch(body), 1, "bad-"+string(rune('a'+i)))
			Expect(err).To(MatchError(ErrInvalid), body)
		}
		snap, err := s.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Revision).To(Equal(uint64(1)))
	})

	It("refuses a patch whose key is null, a list or repeated", func() {
		for _, body := range []string{`{"sandbox_profiles":null}`, `{"sandbox_profiles":[]}`, `{"sandbox_profiles":{"default":"a","default":"b"}}`} {
			var p Patch
			if err := json.Unmarshal([]byte(body), &p); err == nil {
				_, err = s.Update(ctx, p, 1, "x-"+body[:10])
				Expect(err).To(HaveOccurred(), body)
			}
		}
	})

	It("opens a document written before the key existed", func() {
		_, err := s.Update(ctx, Patch{}, 1, "touch")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())
		raw, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		var doc map[string]any
		Expect(json.Unmarshal(raw, &doc)).To(Succeed())
		var strip func(any)
		strip = func(v any) {
			switch m := v.(type) {
			case map[string]any:
				delete(m, "sandbox_profiles")
				for _, c := range m {
					strip(c)
				}
			}
		}
		strip(doc)
		old, _ := json.Marshal(doc)
		Expect(string(old)).NotTo(ContainSubstring("sandbox_profiles"))
		Expect(os.WriteFile(path, old, 0o600)).To(Succeed())
		s, err = Open(path, nil)
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Policy.SandboxPolicy().Allows("media", "alice", "user")).To(BeTrue())
	})
})
