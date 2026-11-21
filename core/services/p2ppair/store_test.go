package p2ppair_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

const testCode = "ABCDE23456"

var t0 = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func invite(id string, ttl time.Duration) p2ppair.Invite {
	return p2ppair.Invite{
		ID: id, OwnerID: "owner", Kind: p2ppair.KindDevice, Label: "phone",
		Scope:     p2ppair.Scope{Models: []string{"m1"}},
		RelayIDs:  []string{"r1"},
		ExpiresAt: t0.Add(ttl), CreatedBy: "admin", CreatedAt: t0,
	}
}

func redeemParams(code, deviceID string) p2ppair.RedeemParams {
	return p2ppair.RedeemParams{Code: code, Peer: newPeer(), Now: t0.Add(time.Second), DeviceID: deviceID}
}

func openSQLite() *gorm.DB {
	path := filepath.Join(GinkgoT().TempDir(), "p.db")
	db, err := sqlitedb.Open("file:"+path+"?_busy_timeout=5000&_txlock=immediate", &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// storeContract is what every p2ppair.Store must do.
func storeContract(newStore func() p2ppair.Store) {
	var s p2ppair.Store
	ctx := context.Background()
	BeforeEach(func() { s = newStore() })

	It("creates, gets and lists invites newest first", func() {
		Expect(s.CreateInvite(ctx, invite("a", time.Hour), "AAAAAAAAA2")).To(Succeed())
		later := invite("b", time.Hour)
		later.CreatedAt = t0.Add(time.Minute)
		Expect(s.CreateInvite(ctx, later, "BBBBBBBBB2")).To(Succeed())
		got, err := s.GetInvite(ctx, "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Label).To(Equal("phone"))
		Expect(got.Scope.Models).To(Equal([]string{"m1"}))
		Expect(got.RelayIDs).To(Equal([]string{"r1"}))
		all, err := s.ListInvites(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect([]string{all[0].ID, all[1].ID}).To(Equal([]string{"b", "a"}))
		_, err = s.GetInvite(ctx, "zz")
		Expect(errors.Is(err, p2ppair.ErrNotFound)).To(BeTrue())
	})

	It("does not keep the code in the clear", func() {
		Expect(s.CreateInvite(ctx, invite("a", time.Hour), testCode)).To(Succeed())
		got, err := s.GetInvite(ctx, "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprintf("%+v", got)).NotTo(ContainSubstring(testCode))
	})

	It("redeems once: the second redeem of a code fails", func() {
		Expect(s.CreateInvite(ctx, invite("a", time.Hour), testCode)).To(Succeed())
		inv, dev, err := s.Redeem(ctx, redeemParams(testCode, "d1"))
		Expect(err).NotTo(HaveOccurred())
		Expect(inv.UsedAt).NotTo(BeNil())
		Expect(inv.DeviceID).To(Equal("d1"))
		Expect(dev.InviteID).To(Equal("a"))
		Expect(dev.Kind).To(Equal(p2ppair.KindDevice))
		Expect(dev.Label).To(Equal("phone"), "the label of the invite is the default")
		Expect(dev.Scope.Models).To(Equal([]string{"m1"}))
		_, _, err = s.Redeem(ctx, redeemParams(testCode, "d2"))
		Expect(errors.Is(err, p2ppair.ErrInviteInvalid)).To(BeTrue())
		got, _ := s.GetInvite(ctx, "a")
		Expect(got.UsedByPeer).To(Equal(dev.PeerID))
		Expect(got.Status(t0)).To(Equal(p2ppair.InviteUsed))
	})

	It("redeems the code in any case and with separators", func() {
		Expect(s.CreateInvite(ctx, invite("a", time.Hour), testCode)).To(Succeed())
		_, _, err := s.Redeem(ctx, redeemParams("abcde-23456", "d1"))
		Expect(err).NotTo(HaveOccurred())
	})

	It("lets exactly one of many concurrent redeems win", func() {
		Expect(s.CreateInvite(ctx, invite("a", time.Hour), testCode)).To(Succeed())
		var wins, invalid atomic.Int32
		var wg sync.WaitGroup
		for i := range 16 {
			wg.Add(1)
			go func(i int) {
				defer GinkgoRecover()
				defer wg.Done()
				_, _, err := s.Redeem(ctx, redeemParams(testCode, fmt.Sprintf("d%d", i)))
				switch {
				case err == nil:
					wins.Add(1)
				case errors.Is(err, p2ppair.ErrInviteInvalid):
					invalid.Add(1)
				default:
					Fail("unexpected error: " + err.Error())
				}
			}(i)
		}
		wg.Wait()
		Expect(wins.Load()).To(Equal(int32(1)))
		Expect(invalid.Load()).To(Equal(int32(15)))
		devs, _ := s.ListDevices(ctx)
		Expect(devs).To(HaveLen(1))
	})

	It("refuses an unknown, expired and canceled code with the same error", func() {
		Expect(s.CreateInvite(ctx, invite("a", time.Minute), testCode)).To(Succeed())
		Expect(s.CreateInvite(ctx, invite("c", time.Hour), "CCCCCCCCC2")).To(Succeed())
		Expect(s.CancelInvite(ctx, "c", t0)).To(Succeed())

		_, _, err := s.Redeem(ctx, redeemParams("ZZZZZZZZZ2", "d"))
		Expect(err).To(MatchError(p2ppair.ErrInviteInvalid))
		late := redeemParams(testCode, "d")
		late.Now = t0.Add(time.Minute)
		_, _, err = s.Redeem(ctx, late)
		Expect(err).To(MatchError(p2ppair.ErrInviteInvalid))
		_, _, err = s.Redeem(ctx, redeemParams("CCCCCCCCC2", "d"))
		Expect(err).To(MatchError(p2ppair.ErrInviteInvalid))
		devs, _ := s.ListDevices(ctx)
		Expect(devs).To(BeEmpty())
	})

	It("cancels a pending invite only", func() {
		Expect(s.CreateInvite(ctx, invite("a", time.Hour), testCode)).To(Succeed())
		Expect(s.CancelInvite(ctx, "a", t0)).To(Succeed())
		Expect(s.CancelInvite(ctx, "a", t0)).To(MatchError(p2ppair.ErrNotPending))
		Expect(s.CancelInvite(ctx, "nope", t0)).To(MatchError(p2ppair.ErrNotFound))
		got, _ := s.GetInvite(ctx, "a")
		Expect(got.Status(t0)).To(Equal(p2ppair.InviteCanceled))

		Expect(s.CreateInvite(ctx, invite("u", time.Hour), "UUUUUUUUU2")).To(Succeed())
		_, _, err := s.Redeem(ctx, redeemParams("UUUUUUUUU2", "d1"))
		Expect(err).NotTo(HaveOccurred())
		Expect(s.CancelInvite(ctx, "u", t0)).To(MatchError(p2ppair.ErrNotPending))
	})

	It("refuses a second active device for a peer and keeps the invite unused", func() {
		Expect(s.CreateInvite(ctx, invite("a", time.Hour), testCode)).To(Succeed())
		Expect(s.CreateInvite(ctx, invite("b", time.Hour), "BBBBBBBBB2")).To(Succeed())
		p := newPeer()
		first := redeemParams(testCode, "d1")
		first.Peer = p
		_, _, err := s.Redeem(ctx, first)
		Expect(err).NotTo(HaveOccurred())
		second := redeemParams("BBBBBBBBB2", "d2")
		second.Peer = p
		_, _, err = s.Redeem(ctx, second)
		Expect(err).To(MatchError(p2ppair.ErrPeerPaired))
		got, _ := s.GetInvite(ctx, "b")
		Expect(got.UsedAt).To(BeNil(), "the invite is not spent by a refused redeem")
	})

	It("lets a peer pair again after its device was revoked", func() {
		Expect(s.CreateInvite(ctx, invite("a", time.Hour), testCode)).To(Succeed())
		Expect(s.CreateInvite(ctx, invite("b", time.Hour), "BBBBBBBBB2")).To(Succeed())
		p := newPeer()
		first := redeemParams(testCode, "d1")
		first.Peer = p
		_, _, err := s.Redeem(ctx, first)
		Expect(err).NotTo(HaveOccurred())
		_, err = s.RevokeDevice(ctx, "d1", "admin", t0)
		Expect(err).NotTo(HaveOccurred())
		second := redeemParams("BBBBBBBBB2", "d2")
		second.Peer = p
		_, _, err = s.Redeem(ctx, second)
		Expect(err).NotTo(HaveOccurred())
		devs, _ := s.ListDevices(ctx)
		Expect(devs).To(HaveLen(2))
	})

	It("revokes a device, once, and records who", func() {
		Expect(s.CreateInvite(ctx, invite("a", time.Hour), testCode)).To(Succeed())
		_, dev, err := s.Redeem(ctx, redeemParams(testCode, "d1"))
		Expect(err).NotTo(HaveOccurred())
		got, err := s.RevokeDevice(ctx, "d1", "admin", t0.Add(time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(got.RevokedBy).To(Equal("admin"))
		Expect(got.Status()).To(Equal(p2ppair.DeviceRevoked))
		again, err := s.RevokeDevice(ctx, "d1", "other", t0.Add(2*time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(again.RevokedBy).To(Equal("admin"), "a second revoke changes nothing")
		Expect(again.RevokedAt.Equal(*got.RevokedAt)).To(BeTrue())
		_, err = s.RevokeDevice(ctx, "nope", "admin", t0)
		Expect(err).To(MatchError(p2ppair.ErrNotFound))
		Expect(dev.RevokedAt).To(BeNil())
	})

	It("touches an active device by peer and ignores others", func() {
		Expect(s.CreateInvite(ctx, invite("a", time.Hour), testCode)).To(Succeed())
		p := newPeer()
		rp := redeemParams(testCode, "d1")
		rp.Peer = p
		_, _, err := s.Redeem(ctx, rp)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.TouchDevice(ctx, p, t0.Add(time.Hour), "relay")).To(Succeed())
		Expect(s.TouchDevice(ctx, newPeer(), t0, "direct")).To(Succeed())
		d, _ := s.GetDevice(ctx, "d1")
		Expect(d.LastPath).To(Equal("relay"))
		Expect(d.LastSeenAt.Equal(t0.Add(time.Hour))).To(BeTrue())
	})

	It("renames a device", func() {
		Expect(s.CreateInvite(ctx, invite("a", time.Hour), testCode)).To(Succeed())
		_, _, err := s.Redeem(ctx, redeemParams(testCode, "d1"))
		Expect(err).NotTo(HaveOccurred())
		Expect(s.RenameDevice(ctx, "d1", "laptop")).To(Succeed())
		d, _ := s.GetDevice(ctx, "d1")
		Expect(d.Label).To(Equal("laptop"))
		Expect(s.RenameDevice(ctx, "nope", "x")).To(MatchError(p2ppair.ErrNotFound))
	})

	It("stores whether a device may use a slow path", func() {
		Expect(s.CreateInvite(ctx, invite("a", time.Hour), testCode)).To(Succeed())
		_, _, err := s.Redeem(ctx, redeemParams(testCode, "d1"))
		Expect(err).NotTo(HaveOccurred())
		d, _ := s.GetDevice(ctx, "d1")
		Expect(d.AllowSlowPath).To(BeFalse())
		Expect(s.SetDeviceAllowSlowPath(ctx, "d1", true)).To(Succeed())
		d, _ = s.GetDevice(ctx, "d1")
		Expect(d.AllowSlowPath).To(BeTrue())
		devs, _ := s.ListDevices(ctx)
		Expect(devs).To(HaveLen(1))
		Expect(devs[0].AllowSlowPath).To(BeTrue())
		Expect(s.SetDeviceAllowSlowPath(ctx, "d1", false)).To(Succeed())
		d, _ = s.GetDevice(ctx, "d1")
		Expect(d.AllowSlowPath).To(BeFalse())
		Expect(s.SetDeviceAllowSlowPath(ctx, "nope", true)).To(MatchError(p2ppair.ErrNotFound))
	})

	It("stores the node a split device is attached to", func() {
		inv := invite("a", time.Hour)
		inv.Kind = p2ppair.KindSplit
		inv.Scope = p2ppair.Scope{Labels: map[string]string{p2ppair.NodeLabel: "node-1"}}
		Expect(s.CreateInvite(ctx, inv, testCode)).To(Succeed())
		_, dev, err := s.Redeem(ctx, redeemParams(testCode, "d1"))
		Expect(err).NotTo(HaveOccurred())
		Expect(dev.NodeID).To(Equal("node-1"), "the invite names the first node")
		d, _ := s.GetDevice(ctx, "d1")
		Expect(d.NodeID).To(Equal("node-1"))
		Expect(s.SetDeviceNode(ctx, "d1", "node-2", "")).To(Succeed())
		devs, _ := s.ListDevices(ctx)
		Expect(devs).To(HaveLen(1))
		Expect(devs[0].NodeID).To(Equal("node-2"))
		Expect(s.SetDeviceNode(ctx, "d1", "", "gpu-2 is no longer a split server")).To(Succeed())
		d, _ = s.GetDevice(ctx, "d1")
		Expect(d.NodeID).To(BeEmpty(), "an empty node attaches the device to the server again")
		Expect(d.NodeNote).To(Equal("gpu-2 is no longer a split server"), "a detached worker keeps why")
		Expect(s.SetDeviceNode(ctx, "d1", "node-1", "")).To(Succeed())
		d, _ = s.GetDevice(ctx, "d1")
		Expect(d.NodeNote).To(BeEmpty(), "attaching it again clears the note")
		Expect(s.SetDeviceNode(ctx, "nope", "node-1", "")).To(MatchError(p2ppair.ErrNotFound))
	})

	It("undoes the redeem when the key issuer fails", func() {
		Expect(s.CreateInvite(ctx, invite("a", time.Hour), testCode)).To(Succeed())
		rp := redeemParams(testCode, "d1")
		rp.Issue = func(context.Context, p2ppair.Tx, p2ppair.Invite, p2ppair.Device) (string, error) {
			return "", errors.New("boom")
		}
		_, _, err := s.Redeem(ctx, rp)
		Expect(err).To(HaveOccurred())
		got, _ := s.GetInvite(ctx, "a")
		Expect(got.UsedAt).To(BeNil())
		devs, _ := s.ListDevices(ctx)
		Expect(devs).To(BeEmpty())
		_, _, err = s.Redeem(ctx, redeemParams(testCode, "d2"))
		Expect(err).NotTo(HaveOccurred(), "the code still works")
	})

	It("keeps the key ID the issuer returns on the device", func() {
		Expect(s.CreateInvite(ctx, invite("a", time.Hour), testCode)).To(Succeed())
		rp := redeemParams(testCode, "d1")
		rp.Issue = func(context.Context, p2ppair.Tx, p2ppair.Invite, p2ppair.Device) (string, error) {
			return "key-1", nil
		}
		_, dev, err := s.Redeem(ctx, rp)
		Expect(err).NotTo(HaveOccurred())
		Expect(dev.ClientKeyID).To(Equal("key-1"))
		got, _ := s.GetDevice(ctx, "d1")
		Expect(got.ClientKeyID).To(Equal("key-1"))
	})
}

var _ = Describe("stores", func() {
	Describe("JSON store in memory", func() {
		storeContract(func() p2ppair.Store {
			s, err := p2ppair.NewJSONStore("", "secret")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})
	Describe("JSON store on disk", func() {
		storeContract(func() p2ppair.Store {
			s, err := p2ppair.NewJSONStore(filepath.Join(GinkgoT().TempDir(), "pair"), "secret")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})
	Describe("GORM store on SQLite", func() {
		storeContract(func() p2ppair.Store {
			s, err := p2ppair.NewGormStore(openSQLite(), "secret")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	Describe("JSON file", func() {
		It("is private, survives a restart and holds no plaintext code", func() {
			dir := filepath.Join(GinkgoT().TempDir(), "pair")
			s, err := p2ppair.NewJSONStore(dir, "secret")
			Expect(err).NotTo(HaveOccurred())
			Expect(s.CreateInvite(context.Background(), invite("a", time.Hour), testCode)).To(Succeed())
			path := filepath.Join(dir, p2ppair.JSONFileName)
			st, err := os.Stat(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o600)))
			raw, _ := os.ReadFile(path)
			Expect(string(raw)).NotTo(ContainSubstring(testCode))

			again, err := p2ppair.NewJSONStore(dir, "secret")
			Expect(err).NotTo(HaveOccurred())
			_, _, err = again.Redeem(context.Background(), redeemParams(testCode, "d1"))
			Expect(err).NotTo(HaveOccurred())
		})

		It("moves a damaged file aside and starts empty", func() {
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(dir, p2ppair.JSONFileName), []byte("{not json"), 0o600)).To(Succeed())
			s, err := p2ppair.NewJSONStore(dir, "")
			Expect(err).NotTo(HaveOccurred())
			all, err := s.ListInvites(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(all).To(BeEmpty())
			entries, _ := os.ReadDir(dir)
			Expect(entries).To(HaveLen(1), "the damaged file is kept under another name")
		})
	})

	Describe("GORM tables", func() {
		It("are created by Models in a database that migrates on its own", func() {
			db := openSQLite()
			Expect(db.AutoMigrate(p2ppair.Models()...)).To(Succeed())
			Expect(db.Migrator().HasTable("p2p_invites")).To(BeTrue())
			Expect(db.Migrator().HasTable("p2p_devices")).To(BeTrue())
		})
		It("enforce one active device per peer in the database", func() {
			db := openSQLite()
			Expect(db.AutoMigrate(p2ppair.Models()...)).To(Succeed())
			ins := func(id string, revoked bool) error {
				q := "INSERT INTO p2p_devices (id, kind, peer_id, revoked_at) VALUES (?, 'device', 'same', NULL)"
				if revoked {
					q = "INSERT INTO p2p_devices (id, kind, peer_id, revoked_at) VALUES (?, 'device', 'same', '2026-01-01')"
				}
				return db.Exec(q, id).Error
			}
			Expect(ins("1", false)).To(Succeed())
			Expect(ins("2", false)).NotTo(Succeed())
			Expect(ins("3", true)).To(Succeed())
			Expect(ins("4", true)).To(Succeed())
		})
	})
})
