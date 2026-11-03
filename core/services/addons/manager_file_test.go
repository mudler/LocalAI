//go:build linux

// SPDX-License-Identifier: MIT
package addons

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/uploads"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/zipformat"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("install from a local zip file", func() {
	var r *updateRig
	var base map[string]any
	BeforeEach(func() {
		r = newUpdateRig(false)
		r.m.SetUploads(&uploads.Store{Root: filepath.Join(r.cfg.Root, "uploads")})
		raw, e := os.ReadFile("../../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		Expect(json.Unmarshal(raw, &base)).To(Succeed())
	})
	// zipFor builds the zip of a sample add-on of the given version that asks
	// for the given required host operations.
	zipFor := func(version, osName string, required ...string) []byte {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		m["version"], m["os"] = version, osName
		if len(required) > 0 {
			m["capabilities"] = map[string]any{"service": true, "hostOperations": map[string]any{"required": required}}
		}
		mb, e := json.Marshal(m)
		Expect(e).NotTo(HaveOccurred())
		var b bytes.Buffer
		Expect(zipformat.Write(&b, "sample", version, osName, "amd64", "bin/sample", []zipformat.Entry{
			{Name: "manifest.json", Data: mb}, {Name: "bin/sample", Data: []byte("executable " + version), Exec: true},
		})).To(Succeed())
		return b.Bytes()
	}
	upload := func(user string, data []byte) (FileView, error) {
		a := contract.Actor{UserID: user, CredentialID: "key", Mode: "named-user"}
		id, _, e := r.m.FileBegin(ctx, a, "sample.zip")
		Expect(e).NotTo(HaveOccurred())
		for off := 0; off < len(data); off += 1000 {
			end := min(off+1000, len(data))
			Expect(r.m.FileChunk(ctx, a, id, int64(off), data[off:end])).To(Succeed())
		}
		return r.m.FileFinish(ctx, a, id)
	}
	fileCmd := func(op, action string, v FileView) LifecycleCommand {
		return LifecycleCommand{OperationID: op, AddonID: "sample", Action: action, SourceID: zipformat.SourceID, Version: v.FileID, Approval: &HostApproval{Digest: "sha256:" + v.SHA256}}
	}

	It("installs through the manager and records an unsigned local file", func() {
		v, e := upload("admin", zipFor("2.0.0", "linux"))
		Expect(e).NotTo(HaveOccurred())
		Expect(v.Relation).To(Equal(FileNew))
		Expect(v.Origin).To(Equal("local file, unsigned"))
		Expect(v.Version).To(Equal("2.0.0"))
		Expect(v.API.Min).To(Equal(contract.HostAPIVersion))
		_, e = r.m.Submit(ctx, actor, fileCmd("f1", "install", v))
		Expect(e).NotTo(HaveOccurred())
		st, _ := r.m.Status(ctx, "sample")
		Expect(st.Installed).To(BeTrue())
		rc := st.Generations[0].Receipt
		Expect(rc.SourceID).To(Equal("local-file"))
		Expect(rc.Origin).To(Equal("local file, unsigned"))
		Expect(rc.Verification).To(Equal("sha256"))
		Expect(rc.Digest).To(Equal("sha256:" + v.SHA256))
		// The staged file is gone once it is installed.
		_, e = r.m.FileDescribe(ctx, actor, v.FileID)
		Expect(errors.Is(e, uploads.ErrNotFound)).To(BeTrue())
		// A package from a file has no library to ask for updates.
		list, e := r.m.Installed(ctx, actor)
		Expect(e).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Update).To(BeNil())
		Expect(list[0].SourceID).To(Equal("local-file"))
	})

	It("never installs a file from the plain typed install, which a key or a tool can make", func() {
		v, e := upload("admin", zipFor("2.0.0", "linux"))
		Expect(e).NotTo(HaveOccurred())
		cmd := fileCmd("p1", "install", v)
		cmd.Approval = nil
		_, e = r.m.Submit(ctx, actor, cmd)
		Expect(e).To(MatchError(ErrForbidden))
		_, e = r.m.submit(ctx, actor, fileCmd("p2", "install", v), true)
		Expect(e).To(MatchError(ErrForbidden), "the automatic upgrade of the owner does not take files either")
		st, _ := r.m.Status(ctx, "sample")
		Expect(st.Installed).To(BeFalse())
		r.m.mu.Lock()
		Expect(r.m.journal.Operations).To(BeEmpty(), "a refused command leaves nothing in the journal")
		r.m.mu.Unlock()
		// The file is still staged for the dialog.
		_, e = r.m.FileDescribe(ctx, actor, v.FileID)
		Expect(e).NotTo(HaveOccurred())
	})

	It("keeps a file for the user that uploaded it", func() {
		v, e := upload("admin", zipFor("2.0.0", "linux"))
		Expect(e).NotTo(HaveOccurred())
		other := contract.Actor{UserID: "admin-two", CredentialID: "key", Mode: "named-user"}
		_, e = r.m.stageUpload(ctx, other, v.FileID)
		Expect(errors.Is(e, uploads.ErrNotFound)).To(BeTrue())
		// A command of another user is refused by the administrator check
		// before it can reach the file.
		_, e = r.m.Submit(ctx, other, fileCmd("f1", "install", v))
		Expect(e).To(MatchError(ErrForbidden))
		st, _ := r.m.Status(ctx, "sample")
		Expect(st.Installed).To(BeFalse())
	})

	It("refuses a file for another platform when it is uploaded, and leaves nothing staged", func() {
		_, e := upload("admin", zipFor("2.0.0", "darwin"))
		var fe *packages.FileError
		Expect(errors.As(e, &fe)).To(BeTrue())
		Expect(fe.Code).To(Equal(packages.FileWrongPlatform))
		ents, _ := os.ReadDir(filepath.Join(r.cfg.Root, "uploads"))
		Expect(ents).To(BeEmpty())
	})

	It("refuses an install when the administrator is not one", func() {
		_, _, e := r.m.FileBegin(ctx, contract.Actor{UserID: "user", Mode: "named-user"}, "x.zip")
		Expect(e).To(MatchError(ErrForbidden))
	})

	Describe("versions", func() {
		install := func(version string) FileView {
			v, e := upload("admin", zipFor(version, "linux"))
			Expect(e).NotTo(HaveOccurred())
			_, e = r.m.Submit(ctx, actor, fileCmd("i-"+version, "install", v))
			Expect(e).NotTo(HaveOccurred())
			return v
		}
		BeforeEach(func() { install("2.0.0") })

		It("upgrades to a higher version through the same generation machinery", func() {
			v, e := upload("admin", zipFor("2.1.0", "linux"))
			Expect(e).NotTo(HaveOccurred())
			Expect(v.Relation).To(Equal(FileUpgrade))
			Expect(v.InstalledVersion).To(Equal("2.0.0"))
			_, e = r.m.Submit(ctx, actor, fileCmd("u1", "upgrade", v))
			Expect(e).NotTo(HaveOccurred())
			st, _ := r.m.Status(ctx, "sample")
			Expect(st.Generation).To(Equal(uint64(2)))
			Expect(st.Generations[1].Receipt.Manifest.Version).To(Equal("2.1.0"))
		})

		It("refuses the same version", func() {
			v, e := upload("admin", zipFor("2.0.0", "linux"))
			Expect(e).NotTo(HaveOccurred())
			Expect(v.Relation).To(Equal(FileSame))
			_, e = r.m.Submit(ctx, actor, fileCmd("u1", "upgrade", v))
			Expect(e).To(MatchError(ErrOperationFailed))
			Expect(e.Error()).To(ContainSubstring(CauseVersionNotNewer))
			st, _ := r.m.Status(ctx, "sample")
			Expect(st.Generation).To(Equal(uint64(1)))
		})

		It("refuses a lower version unless the downgrade is confirmed", func() {
			v, e := upload("admin", zipFor("1.5.0", "linux"))
			Expect(e).NotTo(HaveOccurred())
			Expect(v.Relation).To(Equal(FileDowngrade))
			_, e = r.m.Submit(ctx, actor, fileCmd("d1", "upgrade", v))
			Expect(e).To(MatchError(ErrOperationFailed))
			Expect(e.Error()).To(ContainSubstring(CauseDowngradeRefused))
			st, _ := r.m.Status(ctx, "sample")
			Expect(st.Generations[st.Generation-1].Receipt.Manifest.Version).To(Equal("2.0.0"))
			// The file stays staged for a retry with the confirmation.
			cmd := fileCmd("d2", "upgrade", v)
			cmd.Downgrade = true
			_, e = r.m.Submit(ctx, actor, cmd)
			Expect(e).NotTo(HaveOccurred())
			st, _ = r.m.Status(ctx, "sample")
			Expect(st.Generations[st.Generation-1].Receipt.Manifest.Version).To(Equal("1.5.0"))
		})
	})

	Describe("host access approval", func() {
		It("grants only what the administrator approved for the digest of the file", func() {
			g := newGrantSim(r.m)
			v, e := upload("admin", zipFor("2.0.0", "linux", "workspace.materialize"))
			Expect(e).NotTo(HaveOccurred())
			Expect(v.Required).To(Equal([]string{"workspace.materialize"}))
			_, e = r.m.Submit(ctx, actor, fileCmd("f0", "install", v))
			var refusal *HostRequirementsRefusal
			Expect(errors.As(e, &refusal)).To(BeTrue(), "an unapproved required operation is refused")
			// Previews give the digest the dialog shows.
			rel, e := r.m.PreviewRelease(WithFileOwner(ctx, "admin"), zipformat.SourceID, "sample", v.FileID)
			Expect(e).NotTo(HaveOccurred())
			Expect(rel.Digest).To(Equal("sha256:" + v.SHA256))
			Expect(rel.Official).To(BeFalse())
			Expect(rel.Required).To(Equal([]string{"workspace.materialize"}))
			cmd := fileCmd("f1", "install", v)
			cmd.Approval = &HostApproval{Digest: "sha256:" + strings.Repeat("0", 64)}
			_, e = r.m.Submit(ctx, actor, cmd)
			var stale *ApprovalStaleError
			Expect(errors.As(e, &stale)).To(BeTrue(), "another digest is stale")
			cmd = fileCmd("f2", "install", v)
			cmd.Approval = &HostApproval{Digest: rel.Digest, Ops: []string{"workspace.materialize", "egress.check"}, Revision: 3}
			_, e = r.m.Submit(ctx, actor, cmd)
			Expect(e).NotTo(HaveOccurred())
			Expect(g.stagedOps("f2")).To(Equal([]string{"workspace.materialize"}))
			Expect(r.m.OperationCompleted("f2")).To(BeTrue())
		})

		It("previews only the files of the calling user", func() {
			v, e := upload("admin", zipFor("2.0.0", "linux"))
			Expect(e).NotTo(HaveOccurred())
			_, e = r.m.PreviewRelease(WithFileOwner(ctx, "someone"), zipformat.SourceID, "sample", v.FileID)
			Expect(errors.Is(e, uploads.ErrNotFound)).To(BeTrue())
			_, e = r.m.PreviewRelease(context.Background(), zipformat.SourceID, "sample", v.FileID)
			Expect(errors.Is(e, uploads.ErrNotFound)).To(BeTrue())
		})
	})
})
