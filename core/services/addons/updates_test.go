// SPDX-License-Identifier: MIT
package addons

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/updates"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// updateLibrary serves a library whose entries a spec changes. Each entry is
// a package built from the fixture manifest with its own version label and
// content, so a digest-only change has the same label and other bytes.
type updateLibrary struct {
	mu       sync.Mutex
	server   *httptest.Server
	archives map[string][]byte
	releases []catalog.Release
	sources  *catalog.Sources
}

func (l *updateLibrary) publish(version, channel, content string, requires catalog.Requirements) catalog.Release {
	GinkgoHelper()
	manifest, e := os.ReadFile("../../../tests/addons/fixtures/packages/manifest.json")
	Expect(e).NotTo(HaveOccurred())
	var m map[string]any
	Expect(json.Unmarshal(manifest, &m)).To(Succeed())
	m["version"] = version
	manifest, _ = json.Marshal(m)
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, f := range []struct {
		name string
		data []byte
	}{{"manifest.json", manifest}, {"bin/sample", []byte(content)}} {
		Expect(tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0755, Size: int64(len(f.data))})).To(Succeed())
		_, e = tw.Write(f.data)
		Expect(e).NotTo(HaveOccurred())
	}
	Expect(tw.Close()).To(Succeed())
	name := fmt.Sprintf("/%s-%s-%x.tar", version, channel, sha256.Sum256([]byte(content)))
	l.mu.Lock()
	defer l.mu.Unlock()
	l.archives[name] = b.Bytes()
	r := catalog.Release{AddonID: "sample", Name: "Sample", Version: version, Channel: channel, Artifact: l.server.URL + name,
		Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(b.Bytes())), OS: "linux", Arch: "amd64", Requires: requires}
	// One entry per version and channel: a moving channel is replaced.
	kept := l.releases[:0]
	for _, o := range l.releases {
		if o.Version != version || o.Channel != channel {
			kept = append(kept, o)
		}
	}
	l.releases = append(kept, r)
	return r
}

func (l *updateLibrary) refresh() {
	GinkgoHelper()
	_, e := l.sources.Refresh(ctx, "test")
	Expect(e).NotTo(HaveOccurred())
}

type updateRig struct {
	lib  *updateLibrary
	m    *Manager
	cfg  OwnerConfig
	clk  *time.Time
	tick func(time.Duration)
}

func newUpdateRig(withUpdates bool) *updateRig {
	GinkgoHelper()
	root := GinkgoT().TempDir()
	lib := &updateLibrary{archives: map[string][]byte{}}
	lib.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lib.mu.Lock()
		defer lib.mu.Unlock()
		if r.URL.Path == "/library.yaml" {
			_ = catalog.WriteIndex(w, lib.releases)
			return
		}
		if data, ok := lib.archives[r.URL.Path]; ok {
			_, _ = w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	DeferCleanup(lib.server.Close)
	sources, e := catalog.NewSources(filepath.Join(root, "sources.json"), admin, catalog.CredentialResolver{})
	Expect(e).NotTo(HaveOccurred())
	lib.sources = sources
	Expect(sources.Save(ctx, actor, catalog.Source{ID: "test", Name: "Test", URL: lib.server.URL + "/library.yaml", Enabled: true, AllowLoopbackHTTP: true})).To(Succeed())
	c := catalog.Catalog{Sources: sources, TargetOS: "linux", TargetArch: "amd64"}
	cfg := OwnerConfig{InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "disk-one", Root: filepath.Join(root, "owner")}
	i := &packages.Installer{Root: filepath.Join(cfg.Root, "packages"), Catalog: c, Host: contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"}}
	DeferCleanup(func() {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, e error) error {
			if e == nil && info.IsDir() {
				_ = os.Chmod(p, 0700)
			}
			return nil
		})
	})
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	rig := &updateRig{lib: lib, cfg: cfg, clk: &now}
	rig.tick = func(d time.Duration) { now = now.Add(d) }
	// The first release exists before the manager starts.
	lib.publish("1.2.3-dev", "development", "build-1", catalog.Requirements{})
	lib.publish("1.2.3", "", "release-1.2.3", catalog.Requirements{})
	lib.refresh()
	rig.m = open(cfg, c, i)
	if withUpdates {
		rig.m.EnableUpdates(updates.Config{Now: func() time.Time { return now }, MinRefresh: time.Second, TTL: time.Hour})
	}
	return rig
}

func upgrade(op string, expected, candidate string) LifecycleCommand {
	return LifecycleCommand{OperationID: op, AddonID: "sample", Action: "upgrade", ExpectedDigest: expected, CandidateDigest: candidate}
}

func (r *updateRig) installChannel() Status {
	GinkgoHelper()
	_, e := r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "install", AddonID: "sample", Action: "install", SourceID: "test", Version: "development"})
	Expect(e).NotTo(HaveOccurred())
	s, e := r.m.Status(ctx, "sample")
	Expect(e).NotTo(HaveOccurred())
	return s
}

func (r *updateRig) installVersion() Status {
	GinkgoHelper()
	_, e := r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "install", AddonID: "sample", Action: "install", SourceID: "test", Version: "1.2.3"})
	Expect(e).NotTo(HaveOccurred())
	s, e := r.m.Status(ctx, "sample")
	Expect(e).NotTo(HaveOccurred())
	return s
}

func (r *updateRig) journalOps() int {
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	return len(r.m.journal.Operations)
}

var _ = Describe("explicit upgrade from an authoritative update check", func() {
	It("reports a digest-only change of a moving channel, applies nothing and offers the pinned upgrade", func() {
		r := newUpdateRig(true)
		s := r.installChannel()
		installed := s.Generations[0].Receipt
		Expect(installed.Selection).To(Equal("development"))
		// The channel moves: same version label, other bytes.
		next := r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{})
		st, e := r.m.CheckUpdates(ctx, actor, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.State).To(Equal(updates.UpdateAvailable))
		Expect(st.InstalledVersion).To(Equal(st.CandidateVersion))
		Expect(st.CandidateVersion).To(Equal("1.2.3-dev"))
		Expect(st.CandidateDigest).To(Equal(next.Digest))
		Expect(st.InstalledDigest).To(Equal(installed.Digest))
		// A check applied nothing.
		after, _ := r.m.Status(ctx, "sample")
		Expect(after.Generation).To(Equal(uint64(1)))
		Expect(after.Generations).To(HaveLen(1))

		out, e := r.m.Submit(ctx, actor, upgrade("up-1", installed.Digest, next.Digest))
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Phase).To(Equal("completed"))
		Expect(out.Generation).To(Equal(uint64(2)))
		s, _ = r.m.Status(ctx, "sample")
		Expect(s.Generations[1].Receipt.Digest).To(Equal(next.Digest))
		Expect(s.Generations[1].Receipt.Selection).To(Equal("development"))
		Expect(s.Generations[1].Receipt.SourceID).To(Equal("test"))
		// The previous generation is retained, never deleted.
		Expect(s.Generations[0].Receipt.Directory).To(BeADirectory())
		// The same operation ID is answered with the same result.
		again, e := r.m.Submit(ctx, actor, upgrade("up-1", installed.Digest, next.Digest))
		Expect(e).NotTo(HaveOccurred())
		Expect(again).To(Equal(out))
		s, _ = r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(2)))
		// The status of the new package is up to date and says so.
		st, e = r.m.CheckUpdates(ctx, actor, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.State).To(Equal(updates.UpToDate))
	})

	It("refuses a second operation ID for the same transition, in flight or done, and applies it once", func() {
		r := newUpdateRig(true)
		s := r.installChannel()
		installed := s.Generations[0].Receipt
		next := r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{})
		r.lib.refresh()
		var wg sync.WaitGroup
		errs := make([]error, 4)
		for i := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = r.m.Submit(ctx, actor, upgrade(fmt.Sprintf("racer-%d", i), installed.Digest, next.Digest))
			}()
		}
		wg.Wait()
		ok := 0
		for _, e := range errs {
			switch {
			case e == nil:
				ok++
			default:
				Expect(e).To(MatchError(ErrTransitionConflict))
			}
		}
		Expect(ok).To(Equal(1))
		s, _ = r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(2)))
		// After completion the same transition under yet another ID is refused too.
		_, e := r.m.Submit(ctx, actor, upgrade("late", installed.Digest, next.Digest))
		Expect(e).To(MatchError(ErrTransitionConflict))
		s, _ = r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(2)))
	})

	It("runs concurrent requests with one operation ID once and gives each the same answer", func() {
		r := newUpdateRig(true)
		s := r.installChannel()
		installed := s.Generations[0].Receipt
		next := r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{})
		r.lib.refresh()
		var wg sync.WaitGroup
		outs := make([]OperationStatus, 4)
		errs := make([]error, 4)
		for i := range outs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				outs[i], errs[i] = r.m.Submit(ctx, actor, upgrade("same", installed.Digest, next.Digest))
			}()
		}
		wg.Wait()
		for i := range outs {
			Expect(errs[i]).NotTo(HaveOccurred())
			Expect(outs[i]).To(Equal(outs[0]))
			Expect(outs[i].Phase).To(Equal("completed"))
		}
		s, _ = r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(2)))
	})

	It("serializes conflicting upgrades by the installed digest and refuses the loser without a record", func() {
		r := newUpdateRig(true)
		s := r.installChannel()
		installed := s.Generations[0].Receipt
		b := r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{})
		r.lib.refresh()
		_, e := r.m.Submit(ctx, actor, upgrade("first", installed.Digest, b.Digest))
		Expect(e).NotTo(HaveOccurred())
		before := r.journalOps()
		// A frontend that still shows the old state asks for another candidate.
		c := r.lib.publish("1.2.3-dev", "development", "build-3", catalog.Requirements{})
		r.lib.refresh()
		_, e = r.m.Submit(ctx, actor, upgrade("second", installed.Digest, c.Digest))
		Expect(e).To(MatchError(ErrStaleInstalled))
		Expect(r.journalOps()).To(Equal(before), "a refused request leaves nothing behind")
		s, _ = r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(2)))
	})

	It("refuses a candidate that is not the one the library resolves now", func() {
		r := newUpdateRig(true)
		s := r.installChannel()
		installed := s.Generations[0].Receipt
		old := r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{})
		r.lib.refresh()
		// The channel moves again before the administrator confirms.
		r.lib.publish("1.2.3-dev", "development", "build-3", catalog.Requirements{})
		r.lib.refresh()
		before := r.journalOps()
		_, e := r.m.Submit(ctx, actor, upgrade("pinned", installed.Digest, old.Digest))
		Expect(e).To(MatchError(ErrCandidateChanged))
		Expect(r.journalOps()).To(Equal(before))
		s, _ = r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(1)))
	})

	It("refuses an upgrade to the digest that is already installed", func() {
		r := newUpdateRig(true)
		s := r.installChannel()
		d := s.Generations[0].Receipt.Digest
		_, e := r.m.Submit(ctx, actor, upgrade("noop", d, d))
		Expect(e).To(MatchError(ErrCandidateChanged))
	})

	It("rechecks compatibility at execution and refuses an incompatible candidate", func() {
		r := newUpdateRig(true)
		s := r.installChannel()
		installed := s.Generations[0].Receipt
		next := r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{Host: contract.VersionRange{Min: 5, Max: 6}})
		st, e := r.m.CheckUpdates(ctx, actor, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.State).To(Equal(updates.Incompatible))
		Expect(st.Reasons).To(ConsistOf(updates.ReasonHost))
		_, e = r.m.Submit(ctx, actor, upgrade("inc", installed.Digest, next.Digest))
		Expect(e).To(MatchError(ErrIncompatible))
		s, _ = r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(1)))
	})

	It("selects a newer immutable version and treats a rewritten version as an integrity error", func() {
		r := newUpdateRig(true)
		s := r.installVersion()
		installed := s.Generations[0].Receipt
		Expect(installed.Selection).To(Equal("1.2.3"))
		rewritten := r.lib.publish("1.2.3", "", "release-rewritten", catalog.Requirements{})
		st, e := r.m.CheckUpdates(ctx, actor, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.State).To(Equal(updates.IntegrityError))
		_, e = r.m.Submit(ctx, actor, upgrade("rewrite", installed.Digest, rewritten.Digest))
		Expect(e).To(MatchError(ErrIntegrity))
		s, _ = r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(1)))

		// Restored to the original bytes, and a newer version exists.
		r.lib.mu.Lock()
		r.lib.releases = r.lib.releases[:0]
		r.lib.mu.Unlock()
		orig := r.lib.publish("1.2.3", "", "release-1.2.3", catalog.Requirements{})
		Expect(orig.Digest).To(Equal(installed.Digest))
		newer := r.lib.publish("1.3.0", "", "release-1.3.0", catalog.Requirements{})
		r.tick(2 * time.Second)
		st, e = r.m.CheckUpdates(ctx, actor, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.State).To(Equal(updates.UpdateAvailable))
		Expect(st.CandidateVersion).To(Equal("1.3.0"))
		out, e := r.m.Submit(ctx, actor, upgrade("v13", installed.Digest, newer.Digest))
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Phase).To(Equal("completed"))
		s, _ = r.m.Status(ctx, "sample")
		Expect(s.Generations[1].Receipt.Manifest.Version).To(Equal("1.3.0"))
		Expect(s.Generations[1].Receipt.Selection).To(Equal("1.3.0"))
	})

	It("keeps the last good generation when the candidate cannot be staged", func() {
		r := newUpdateRig(true)
		s := r.installChannel()
		installed := s.Generations[0].Receipt
		next := r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{})
		r.lib.refresh()
		// The archive no longer matches the digest the library lists.
		r.lib.mu.Lock()
		for name := range r.lib.archives {
			if r.lib.archives[name] != nil && fmt.Sprintf("sha256:%x", sha256.Sum256(r.lib.archives[name])) == next.Digest {
				r.lib.archives[name] = []byte("corrupt")
			}
		}
		r.lib.mu.Unlock()
		out, e := r.m.Submit(ctx, actor, upgrade("broken", installed.Digest, next.Digest))
		Expect(e).To(MatchError(ErrOperationFailed))
		Expect(out.Phase).NotTo(Equal("completed"))
		s, _ = r.m.Status(ctx, "sample")
		Expect(s.Installed).To(BeTrue())
		Expect(s.Generation).To(Equal(uint64(1)))
		Expect(s.Generations).To(HaveLen(1))
		Expect(s.Generations[0].Receipt.Directory).To(BeADirectory())
		// A failed attempt does not block a new operation for the same candidate.
		r.lib.mu.Lock()
		for name := range r.lib.archives {
			if string(r.lib.archives[name]) == "corrupt" {
				delete(r.lib.archives, name)
			}
		}
		r.lib.mu.Unlock()
		fixed := r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{})
		Expect(fixed.Digest).To(Equal(next.Digest))
		r.lib.refresh()
		retry, e := r.m.Submit(ctx, actor, upgrade("again", installed.Digest, next.Digest))
		Expect(e).NotTo(HaveOccurred())
		Expect(retry.Phase).To(Equal("completed"))
	})

	It("never switches the library or channel, and a failed check is never up to date", func() {
		r := newUpdateRig(true)
		s := r.installChannel()
		installed := s.Generations[0].Receipt
		// Another channel and a newer version exist; the installed channel is unchanged.
		r.lib.publish("9.9.9", "latest", "other", catalog.Requirements{})
		r.lib.publish("1.9.0", "", "newer", catalog.Requirements{})
		r.lib.refresh()
		st, _ := r.m.CheckUpdates(ctx, actor, "sample")
		Expect(st.State).To(Equal(updates.UpToDate))
		Expect(st.Channel).To(Equal("development"))
		// The library goes away.
		r.lib.server.Close()
		r.tick(time.Minute)
		st, e := r.m.CheckUpdates(ctx, actor, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.State).To(Equal(updates.CheckFailed))
		Expect(st.LastSuccess).NotTo(BeZero())
		Expect(st.Error).To(Equal(updates.ClassRefresh))
		// And the upgrade cannot be started from it.
		_, e = r.m.Submit(ctx, actor, upgrade("nolib", installed.Digest, "sha256:"+fmt.Sprintf("%064x", 1)))
		Expect(e).To(HaveOccurred())
		s, _ = r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(1)))
	})

	It("lets an administrator refresh and everyone with the feature read a redacted status", func() {
		r := newUpdateRig(true)
		r.installChannel()
		other := contract.Actor{UserID: "member", CredentialID: "key2", Mode: "named-user"}
		_, e := r.m.CheckUpdates(ctx, other, "sample")
		Expect(e).To(MatchError(ErrForbidden))
		// Not enabled: the add-on does not exist for a non-administrator.
		_, e = r.m.UpdateStatus(ctx, other, "sample")
		Expect(e).To(MatchError(ErrForbidden))
		_, e = r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "enable", AddonID: "sample", Action: "enable", Generation: 1})
		Expect(e).NotTo(HaveOccurred())
		r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{})
		_, e = r.m.CheckUpdates(ctx, actor, "sample")
		Expect(e).NotTo(HaveOccurred())
		view, e := r.m.UpdateStatus(ctx, other, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(view.State).To(Equal(updates.UpdateAvailable))
		Expect(view.InstalledVersion).To(Equal("1.2.3-dev"))
		Expect(view.CandidateDigest + view.InstalledDigest + view.SourceID + view.Channel).To(BeEmpty())
		full, e := r.m.UpdateStatus(ctx, actor, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(full.CandidateDigest).NotTo(BeEmpty())
		// A non-administrator cannot upgrade, whatever the pins say.
		_, e = r.m.Submit(ctx, other, upgrade("nope", full.InstalledDigest, full.CandidateDigest))
		Expect(e).To(MatchError(ErrForbidden))
	})

	It("shows the pending update in the apps list from the cache only", func() {
		r := newUpdateRig(true)
		r.installChannel()
		_, e := r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "enable", AddonID: "sample", Action: "enable", Generation: 1})
		Expect(e).NotTo(HaveOccurred())
		list, e := r.m.Apps(ctx, actor)
		Expect(e).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Update).To(BeEmpty(), "never checked: no chip")
		r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{})
		// The library server is gone: the apps list must not need it.
		r.lib.server.Close()
		list, _ = r.m.Apps(ctx, actor)
		Expect(list[0].Update).To(BeEmpty(), "a library change nobody checked is not reported")
		_, _ = r.m.CheckUpdates(ctx, actor, "sample")
		list, _ = r.m.Apps(ctx, actor)
		Expect(list[0].Update).To(BeEmpty(), "a failed check is not an update")
		installed, e := r.m.Installed(ctx, actor)
		Expect(e).NotTo(HaveOccurred())
		Expect(installed[0].Update).NotTo(BeNil())
		Expect(installed[0].Update.Available).To(BeFalse())
		Expect(installed[0].Update.State).To(Equal("check-failed"))
	})

	It("does not accept pins without an update checker or on another action", func() {
		r := newUpdateRig(false)
		s := r.installChannel()
		_, e := r.m.Submit(ctx, actor, upgrade("nochecker", s.Generations[0].Receipt.Digest, "sha256:"+fmt.Sprintf("%064x", 2)))
		Expect(e).To(HaveOccurred())
		Expect(e).NotTo(MatchError(ErrOperationFailed))
		_, e = r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "x", AddonID: "sample", Action: "disable", ExpectedDigest: "a", CandidateDigest: "b"})
		Expect(e).To(MatchError(ErrConflict))
		_, e = r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "y", AddonID: "sample", Action: "upgrade", ExpectedDigest: s.Generations[0].Receipt.Digest})
		Expect(e).To(MatchError(ErrConflict))
	})

	It("keeps the unpinned typed upgrade of an earlier release working", func() {
		r := newUpdateRig(true)
		r.installVersion()
		out, e := r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "legacy", AddonID: "sample", Action: "upgrade", SourceID: "test", Version: "1.2.3"})
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Phase).To(Equal("completed"))
	})
})

var _ = context.Background
