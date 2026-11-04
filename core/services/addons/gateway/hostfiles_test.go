// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing/iotest"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/uploads"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fileSim is the owner's file service over a real staging store. Finish
// checks nothing about the zip itself: the manager tests do that.
type fileSim struct {
	store   *uploads.Store
	max     int64
	aborted int
	reject  error
}

func (f *fileSim) FileBegin(_ context.Context, a contract.Actor, name string) (string, int64, error) {
	id, e := f.store.Begin(a.UserID, name, f.max)
	return id, f.max, e
}
func (f *fileSim) FileChunk(_ context.Context, a contract.Actor, id string, off int64, p []byte) error {
	return f.store.Append(id, a.UserID, off, p)
}
func (f *fileSim) FileFinish(_ context.Context, a contract.Actor, id string) (addons.FileView, error) {
	e, err := f.store.Finish(id, a.UserID)
	if err != nil {
		return addons.FileView{}, err
	}
	if f.reject != nil {
		_ = f.store.Remove(id, a.UserID)
		return addons.FileView{}, f.reject
	}
	return addons.FileView{FileID: e.ID, Name: e.Name, Size: e.Size, SHA256: e.SHA256, AddonID: "sample", Relation: addons.FileNew}, nil
}
func (f *fileSim) FileAbort(_ context.Context, a contract.Actor, id string) {
	f.aborted++
	f.store.Abort(id, a.UserID)
}
func (f *fileSim) FileDrop(_ context.Context, a contract.Actor, id string) error {
	return f.store.Remove(id, a.UserID)
}
func (f *fileSim) FileDescribe(_ context.Context, a contract.Actor, id string) (addons.FileView, error) {
	_, e, err := f.store.Open(id, a.UserID)
	if err != nil {
		return addons.FileView{}, err
	}
	return addons.FileView{FileID: e.ID, SHA256: e.SHA256, Size: e.Size}, nil
}

var _ = Describe("add-on zip files reach the owner", func() {
	ok := func(context.Context, contract.Actor) error { return nil }
	alice := contract.Actor{UserID: "alice", CredentialID: "session:a", Mode: "named-user"}
	bob := contract.Actor{UserID: "bob", CredentialID: "session:b", Mode: "named-user"}

	type rig struct {
		owner, front, other *Gateway
		sim                 *fileSim
	}
	build := func(max int64) rig {
		sim := &fileSim{store: &uploads.Store{Root: filepath.Join(GinkgoT().TempDir(), "uploads")}, max: max}
		fx := newHGFixture()
		fx.svc.WithFiles(sim)
		var og *Gateway
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			og = &Gateway{HostGrantsOwner: fx.svc, Revalidate: ok}
			h, e := c.OwnerHandler(og)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		return rig{owner: og, front: &Gateway{Forward: cs["front-a"], Revalidate: ok}, other: &Gateway{Forward: cs["front-b"], Revalidate: ok}, sim: sim}
	}
	random := func(n int) []byte {
		b := make([]byte, n)
		_, _ = rand.Read(b)
		return b
	}

	for _, topology := range []string{"on the owner", "through a frontend"} {
		Describe(topology, func() {
			gw := func(r rig) *Gateway {
				if topology == "on the owner" {
					return r.owner
				}
				return r.front
			}
			It("moves a file in chunks and keeps its digest", func() {
				r := build(64 << 20)
				// Three chunks and a bit: the last one is short.
				payload := random(3*FileChunkBytes + 12345)
				v, err := gw(r).UploadFile(context.Background(), alice, "sample.zip", bytes.NewReader(payload))
				Expect(err).NotTo(HaveOccurred())
				sum := sha256.Sum256(payload)
				Expect(v.SHA256).To(Equal(hex.EncodeToString(sum[:])))
				Expect(v.Size).To(Equal(int64(len(payload))))
				p, _, err := r.sim.store.Open(v.FileID, "alice")
				Expect(err).NotTo(HaveOccurred())
				onDisk, _ := os.ReadFile(p)
				Expect(onDisk).To(Equal(payload))
			})

			It("cuts an upload that goes over the limit and keeps nothing", func() {
				r := build(int64(FileChunkBytes) + 10)
				_, err := gw(r).UploadFile(context.Background(), alice, "big.zip", bytes.NewReader(random(2*FileChunkBytes)))
				Expect(typeOf(err)).To(Equal("too_large"))
				var he *HostGrantsError
				Expect(errors.As(err, &he)).To(BeTrue())
				Expect(he.Status).To(Equal(413))
				ents, _ := os.ReadDir(r.sim.store.Root)
				Expect(ents).To(BeEmpty())
			})

			It("passes the rule a refused file broke to the caller", func() {
				r := build(64 << 20)
				r.sim.reject = &packages.FileError{Code: packages.FileWrongPlatform, Msg: "This file is for linux/arm64; this host is linux/amd64"}
				_, err := gw(r).UploadFile(context.Background(), alice, "x.zip", strings.NewReader("zip bytes"))
				Expect(typeOf(err)).To(Equal("wrong_platform"))
				Expect(err.Error()).To(ContainSubstring("linux/arm64"))
				ents, _ := os.ReadDir(r.sim.store.Root)
				Expect(ents).To(BeEmpty(), "a refused file is not kept")
			})

			It("aborts when the client's stream breaks, and rejects an empty file", func() {
				r := build(64 << 20)
				broken := iotest.ErrReader(errors.New("connection reset"))
				_, err := gw(r).UploadFile(context.Background(), alice, "x.zip", broken)
				Expect(err).To(HaveOccurred())
				Expect(r.sim.aborted).To(Equal(1))
				_, err = gw(r).UploadFile(context.Background(), alice, "x.zip", bytes.NewReader(nil))
				Expect(typeOf(err)).To(Equal("bad_zip"))
				ents, _ := os.ReadDir(r.sim.store.Root)
				Expect(ents).To(BeEmpty())
			})

			It("binds a staged file to the user that uploaded it", func() {
				r := build(64 << 20)
				v, err := gw(r).UploadFile(context.Background(), alice, "x.zip", strings.NewReader("zip bytes"))
				Expect(err).NotTo(HaveOccurred())
				_, err = gw(r).DescribeFile(context.Background(), bob, v.FileID)
				Expect(typeOf(err)).To(Equal("not_found"))
				Expect(gw(r).DropFile(context.Background(), bob, v.FileID)).NotTo(Succeed())
				got, err := gw(r).DescribeFile(context.Background(), alice, v.FileID)
				Expect(err).NotTo(HaveOccurred())
				Expect(got.FileID).To(Equal(v.FileID))
				Expect(gw(r).DropFile(context.Background(), alice, v.FileID)).To(Succeed())
				_, err = gw(r).DescribeFile(context.Background(), alice, v.FileID)
				Expect(typeOf(err)).To(Equal("not_found"))
			})
		})
	}

	It("lets a second frontend read the file the first one uploaded", func() {
		r := build(64 << 20)
		v, err := r.front.UploadFile(context.Background(), alice, "x.zip", strings.NewReader("zip bytes"))
		Expect(err).NotTo(HaveOccurred())
		got, err := r.other.DescribeFile(context.Background(), alice, v.FileID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.SHA256).To(Equal(v.SHA256))
	})

	It("needs no human mark to stage a file, and never changes the policy by it", func() {
		r := build(64 << 20)
		_, err := r.front.UploadFile(context.Background(), alice, "x.zip", strings.NewReader("zip bytes"))
		Expect(err).NotTo(HaveOccurred())
		// The install is another call and keeps its rule.
		_, err = r.front.HostGrants(context.Background(), alice, call(HostGrantsInstall, "i1", "", HostGrantsInstallRequest{OperationID: "i1", Action: "install", SourceID: "local-file", AddonID: "sample", Version: strings.Repeat("a", 32), Approve: HostGrantsApprove{Digest: "sha256:aa", Nonce: "n"}}))
		Expect(typeOf(err)).To(Equal("not_human_surface"))
	})

	It("stages nothing while the deployment locks the UI host grants", func() {
		sim := &fileSim{store: &uploads.Store{Root: filepath.Join(GinkgoT().TempDir(), "uploads")}, max: 1 << 20}
		fx := newHGFixture(func(c *HostGrantsConfig) { c.Locked = true; c.LockedReason = LockedNoAuth })
		fx.svc.WithFiles(sim)
		g := &Gateway{HostGrantsOwner: fx.svc, Revalidate: ok}
		_, err := g.UploadFile(context.Background(), alice, "x.zip", strings.NewReader("x"))
		Expect(typeOf(err)).To(Equal("locked_by_deployment"))
		Expect(err.Error()).To(Equal(LockedNoAuthText))
		ents, _ := os.ReadDir(sim.store.Root)
		Expect(ents).To(BeEmpty())
	})

	It("answers unavailable when the owner has no file service", func() {
		fx := newHGFixture()
		g := &Gateway{HostGrantsOwner: fx.svc, Revalidate: ok}
		_, err := g.UploadFile(context.Background(), alice, "x.zip", strings.NewReader("x"))
		Expect(err).To(MatchError(ErrUnavailable))
	})
})
