// SPDX-License-Identifier: MIT
package hostsettings

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The host ceiling of connector files", func() {
	var dir, path string
	var s *Store
	ctx := context.Background()
	BeforeEach(func() {
		var err error
		dir, err = os.MkdirTemp("", "ceiling-")
		Expect(err).NotTo(HaveOccurred())
		path = filepath.Join(dir, "policy.json")
		s, err = Open(path, legacy())
		Expect(err).NotTo(HaveOccurred())
	})
	AfterEach(func() {
		if s != nil {
			Expect(s.Close()).To(Succeed())
			s = nil
		}
		Expect(os.RemoveAll(dir)).To(Succeed())
	})

	It("is the documented default until the owner sets one, and the policy always says so", func() {
		snap, err := s.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Policy.ConnectorFilesCeiling).To(BeZero())
		Expect(snap.Policy.FilesCeiling()).To(Equal(contract.DefaultFileCeilingBytes))
		raw, err := json.Marshal(snap.Policy)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"connector_files_ceiling_bytes":0`))
	})

	It("is set, read back and cleared with zero", func() {
		snap, err := s.Update(ctx, Patch{ConnectorFilesCeiling: ptr(int64(5 << 20))}, 1, "set")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Policy.FilesCeiling()).To(Equal(int64(5 << 20)))
		snap, err = s.Update(ctx, Patch{ConnectorFilesCeiling: ptr(int64(0))}, 2, "clear")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Policy.FilesCeiling()).To(Equal(contract.DefaultFileCeilingBytes))
	})

	It("refuses a value that is negative, too small or too large, and keeps the revision", func() {
		for i, v := range []int64{-1, MinFilesCeiling - 1, contract.MaxDeclaredFileBytes + 1} {
			_, err := s.Update(ctx, Patch{ConnectorFilesCeiling: ptr(v)}, 1, "bad-"+string(rune('a'+i)))
			Expect(err).To(MatchError(ErrInvalid), "%d", v)
		}
		snap, err := s.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Revision).To(Equal(uint64(1)))
	})

	It("takes a number only, as the other keys take their own type", func() {
		for _, body := range []string{`{"connector_files_ceiling_bytes":"5"}`, `{"connector_files_ceiling_bytes":null}`, `{"connector_files_ceiling_bytes":1.5}`} {
			var p Patch
			Expect(json.Unmarshal([]byte(body), &p)).NotTo(Succeed(), body)
		}
		var p Patch
		Expect(json.Unmarshal([]byte(`{"connector_files_ceiling_bytes":65536}`), &p)).To(Succeed())
		Expect(*p.ConnectorFilesCeiling).To(Equal(int64(65536)))
	})

	It("opens a policy document written before the key existed", func() {
		_, err := s.Update(ctx, Patch{ConnectorFilesCeiling: ptr(int64(2 << 20))}, 1, "set")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())
		s = nil
		raw, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		old := strings.ReplaceAll(string(raw), `"connector_files_ceiling_bytes":2097152,`, "")
		old = strings.ReplaceAll(old, `,"connector_files_ceiling_bytes":2097152`, "")
		Expect(old).NotTo(ContainSubstring("connector_files_ceiling_bytes"))
		Expect(os.WriteFile(path, []byte(old), 0o600)).To(Succeed())
		s, err = Open(path, nil)
		Expect(err).NotTo(HaveOccurred())
		snap, err := s.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Policy.FilesCeiling()).To(Equal(contract.DefaultFileCeilingBytes))
	})
})
