// SPDX-License-Identifier: MIT
package updates_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/updates"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestUpdates(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Add-on update checker")
}

const (
	osName, archName = "linux", "amd64"
	digestA          = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB          = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	digestC          = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

// library is a catalog in memory. A refresh moves its entries to the
// "remote" ones, the way a fetch replaces a cache.
type library struct {
	mu        sync.Mutex
	cached    []catalog.Release
	remote    []catalog.Release
	failed    error // set: the cache is failed with this class
	refreshes atomic.Int32
	refreshE  error
	state     catalog.RefreshState
	hasState  bool
	delay     chan struct{}
	started   chan struct{}
	now       func() time.Time
}

func (l *library) Target() (string, string) { return osName, archName }
func (l *library) State(string) (catalog.RefreshState, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state, l.hasState
}
func (l *library) Releases(ctx context.Context, _, id string) ([]catalog.Release, error) {
	// The owner's catalog gives up on a finished context before it reads.
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failed != nil {
		return nil, l.failed
	}
	out := []catalog.Release{}
	for _, r := range l.cached {
		if r.AddonID == id {
			out = append(out, r)
		}
	}
	return out, nil
}
func (l *library) Resolve(_ context.Context, source, id, version string) (catalog.Release, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failed != nil {
		return catalog.Release{}, l.failed
	}
	var found *catalog.Release
	for _, r := range l.cached {
		if r.AddonID == id && r.OS == osName && r.Arch == archName && (r.Version == version || (r.Channel != "" && r.Channel == version)) {
			if found != nil {
				return catalog.Release{}, catalog.ErrInvalid
			}
			c := r
			c.Selection = version
			c.SourceID = source
			found = &c
		}
	}
	if found == nil {
		return catalog.Release{}, catalog.ErrNotFound
	}
	return *found, nil
}
func (l *library) Refresh(ctx context.Context, _ string) error {
	l.refreshes.Add(1)
	if l.started != nil {
		l.started <- struct{}{}
	}
	if l.delay != nil {
		select {
		case <-l.delay:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.refreshE != nil {
		l.failed = l.refreshE
		l.state.Failed = true
		return l.refreshE
	}
	l.failed = nil
	l.cached = append([]catalog.Release(nil), l.remote...)
	l.state = catalog.RefreshState{CheckedAt: l.now(), LastSuccess: l.now()}
	l.hasState = true
	return nil
}

type installs struct {
	mu sync.Mutex
	in map[string]updates.Installation
}

func (i *installs) Installation(_ context.Context, id string) (updates.Installation, bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	v, ok := i.in[id]
	return v, ok, nil
}

func rel(version, channel, digest string) catalog.Release {
	return catalog.Release{AddonID: "notes", Name: "notes", SourceID: "official", Version: version, Channel: channel, Digest: digest, OS: osName, Arch: archName}
}

func installed(version, selection, channel, digest string) updates.Installation {
	return updates.Installation{AddonID: "notes", Generation: 1, Receipt: packages.PackageReceipt{
		Digest: digest, SourceID: "official", Channel: channel, Selection: selection,
		Manifest: contract.Manifest{ID: "notes", Version: version, DataFormat: contract.VersionRange{Min: 1, Max: 1}},
	}}
}

var host = contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: osName, Arch: archName}

type rig struct {
	lib     *library
	inst    *installs
	checker *updates.Checker
	clock   *time.Time
}

func newRig(in updates.Installation, releases ...catalog.Release) *rig {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	r := &rig{clock: &now}
	r.lib = &library{cached: releases, remote: releases, now: func() time.Time { return *r.clock }, state: catalog.RefreshState{CheckedAt: now, LastSuccess: now}, hasState: true}
	r.inst = &installs{in: map[string]updates.Installation{"notes": in}}
	r.checker = updates.New(updates.Config{Host: host, TTL: time.Hour, MaxEntries: 3, MinRefresh: time.Minute, Now: func() time.Time { return *r.clock }}, r.lib, r.inst)
	return r
}

func check(r *rig, refresh bool) updates.Status {
	GinkgoHelper()
	st, e := r.checker.Check(context.Background(), "notes", refresh)
	Expect(e).NotTo(HaveOccurred())
	return st
}

var _ = Describe("Update detection", func() {
	Context("moving channels", func() {
		for _, channel := range []string{"development", "branch-fix-cache", "latest"} {
			It(fmt.Sprintf("offers a digest-only change of the %s channel with an identical version label", channel), func() {
				r := newRig(installed("1.0.0", channel, channel, digestA), rel("1.0.0", channel, digestB))
				st := check(r, false)
				Expect(st.State).To(Equal(updates.UpdateAvailable))
				Expect(st.Available).To(BeTrue())
				Expect(st.InstalledVersion).To(Equal(st.CandidateVersion))
				Expect(st.InstalledDigest).To(Equal(digestA))
				Expect(st.CandidateDigest).To(Equal(digestB))
				Expect(st.Channel).To(Equal(channel))
			})
		}
		It("is up to date when the channel resolves to the installed digest", func() {
			r := newRig(installed("1.0.0", "development", "development", digestA), rel("1.0.0", "development", digestA))
			st := check(r, false)
			Expect(st.State).To(Equal(updates.UpToDate))
			Expect(st.Available).To(BeFalse())
			Expect(st.CandidateDigest).To(Equal(digestA))
		})
		It("never switches the channel: another channel and other versions are not offered", func() {
			r := newRig(installed("1.0.0", "development", "development", digestA),
				rel("1.0.0", "development", digestA), rel("2.0.0", "latest", digestB), rel("1.1.0", "", digestC))
			st := check(r, false)
			Expect(st.State).To(Equal(updates.UpToDate))
			Expect(st.Channel).To(Equal("development"))
			Expect(st.SourceID).To(Equal("official"))
		})
	})

	Context("immutable versions", func() {
		It("selects the highest compatible greater version of the same major", func() {
			r := newRig(installed("1.2.0", "1.2.0", "", digestA),
				rel("1.2.0", "", digestA), rel("1.3.0", "", digestB), rel("1.10.0", "", digestC), rel("2.0.0", "", "sha256:"+strings.Repeat("d", 64)))
			st := check(r, false)
			Expect(st.State).To(Equal(updates.UpdateAvailable))
			// 1.10.0 is greater than 1.3.0 as a number, not as text.
			Expect(st.CandidateVersion).To(Equal("1.10.0"))
			Expect(st.CandidateDigest).To(Equal(digestC))
		})
		It("does not cross a major version", func() {
			r := newRig(installed("1.2.0", "1.2.0", "", digestA), rel("1.2.0", "", digestA), rel("2.0.0", "", digestB))
			Expect(check(r, false).State).To(Equal(updates.UpToDate))
		})
		It("does not move a stable install to a pre-release and orders pre-releases below their release", func() {
			r := newRig(installed("1.2.0", "1.2.0", "", digestA), rel("1.2.0", "", digestA), rel("1.3.0-beta.1", "", digestB))
			Expect(check(r, false).State).To(Equal(updates.UpToDate))
			r = newRig(installed("1.3.0-beta.1", "1.3.0-beta.1", "", digestA), rel("1.3.0-beta.1", "", digestA), rel("1.3.0", "", digestB))
			st := check(r, false)
			Expect(st.State).To(Equal(updates.UpdateAvailable))
			Expect(st.CandidateVersion).To(Equal("1.3.0"))
		})
		It("treats a lower version as nothing to offer", func() {
			r := newRig(installed("1.2.0", "1.2.0", "", digestA), rel("1.2.0", "", digestA), rel("1.1.0", "", digestB))
			Expect(check(r, false).State).To(Equal(updates.UpToDate))
		})
		It("reports the same digest as up to date", func() {
			r := newRig(installed("1.2.0", "1.2.0", "", digestA), rel("1.2.0", "", digestA))
			st := check(r, false)
			Expect(st.State).To(Equal(updates.UpToDate))
			Expect(st.Available).To(BeFalse())
		})
		It("reports a rewritten immutable version as an integrity error, never an update", func() {
			r := newRig(installed("1.2.0", "1.2.0", "", digestA), rel("1.2.0", "", digestB), rel("1.3.0", "", digestC))
			st := check(r, false)
			Expect(st.State).To(Equal(updates.IntegrityError))
			Expect(st.Available).To(BeFalse())
			Expect(st.Reasons).To(ConsistOf(updates.ReasonDigestChanged))
		})
	})

	Context("compatibility", func() {
		It("names the host, SDK and data format ranges a candidate does not fit", func() {
			for reason, requires := range map[string]catalog.Requirements{
				updates.ReasonHost:       {Host: contract.VersionRange{Min: 2, Max: 3}},
				updates.ReasonAPI:        {API: contract.VersionRange{Min: 2, Max: 2}},
				updates.ReasonProtocol:   {Protocol: contract.VersionRange{Min: 2, Max: 2}},
				updates.ReasonSDK:        {SDK: contract.VersionRange{Min: 2, Max: 2}},
				updates.ReasonDataFormat: {DataFormat: contract.VersionRange{Min: 2, Max: 3}},
			} {
				next := rel("1.0.0", "development", digestB)
				next.Requires = requires
				r := newRig(installed("1.0.0", "development", "development", digestA), next)
				st := check(r, false)
				Expect(st.State).To(Equal(updates.Incompatible), reason)
				Expect(st.Reasons).To(ConsistOf(reason))
				Expect(st.Available).To(BeFalse())
			}
		})
		It("refuses a candidate whose data format ends below the installed one", func() {
			in := installed("1.0.0", "development", "development", digestA)
			in.Receipt.Manifest.DataFormat = contract.VersionRange{Min: 1, Max: 2}
			next := rel("1.0.0", "development", digestB)
			next.Requires = catalog.Requirements{DataFormat: contract.VersionRange{Min: 1, Max: 1}}
			st := check(newRig(in, next), false)
			Expect(st.State).To(Equal(updates.Incompatible))
			Expect(st.Reasons).To(ConsistOf(updates.ReasonDataFormat))
		})
		It("reports a platform that is not offered, and prefers the compatible version over a newer incompatible one", func() {
			other := rel("1.0.0", "development", digestB)
			other.OS = "plan9"
			st := check(newRig(installed("1.0.0", "development", "development", digestA), other), false)
			Expect(st.State).To(Equal(updates.Incompatible))
			Expect(st.Reasons).To(ConsistOf(updates.ReasonPlatform))

			newer := rel("1.5.0", "", digestC)
			newer.Requires = catalog.Requirements{Host: contract.VersionRange{Min: 9, Max: 9}}
			st = check(newRig(installed("1.0.0", "1.0.0", "", digestA), rel("1.0.0", "", digestA), rel("1.4.0", "", digestB), newer), false)
			Expect(st.State).To(Equal(updates.UpdateAvailable))
			Expect(st.CandidateVersion).To(Equal("1.4.0"))
		})
		It("does not check the SDK range of a candidate that declares no UI", func() {
			next := rel("1.0.0", "development", digestB)
			next.Requires = catalog.Requirements{Host: contract.VersionRange{Min: 1, Max: 1}}
			Expect(check(newRig(installed("1.0.0", "development", "development", digestA), next), false).State).To(Equal(updates.UpdateAvailable))
		})
	})

	Context("failures", func() {
		It("reports a failed library as check-failed with the last success, never as up to date", func() {
			r := newRig(installed("1.0.0", "development", "development", digestA), rel("1.0.0", "development", digestA))
			Expect(check(r, false).State).To(Equal(updates.UpToDate))
			success := *r.clock
			*r.clock = success.Add(2 * time.Hour)
			r.lib.refreshE = fmt.Errorf("%w: %w", catalog.ErrRefresh, errors.New("dial tcp 10.0.0.9:443: connection refused"))
			st := check(r, true)
			Expect(st.State).To(Equal(updates.CheckFailed))
			Expect(st.State).NotTo(Equal(updates.UpToDate))
			Expect(st.Error).To(Equal(updates.ClassRefresh))
			Expect(st.LastSuccess).To(Equal(success))
			Expect(st.Available).To(BeFalse())
			// The text of the cause never reaches the status.
			Expect(fmt.Sprintf("%+v", st)).NotTo(ContainSubstring("10.0.0.9"))
		})
		It("names an unavailable source credential and an unreachable library by class", func() {
			r := newRig(installed("1.0.0", "development", "development", digestA), rel("1.0.0", "development", digestA))
			r.lib.refreshE = fmt.Errorf("%w: %w", catalog.ErrRefresh, catalog.ErrCredential)
			Expect(check(r, true).Error).To(Equal(updates.ClassCredential))
			*r.clock = r.clock.Add(time.Hour)
			r.lib.refreshE = catalog.ErrUnavailable
			Expect(check(r, true).Error).To(Equal(updates.ClassSource))
		})
		It("reports an add-on the library no longer lists as check-failed", func() {
			r := newRig(installed("1.0.0", "development", "development", digestA))
			st := check(r, false)
			Expect(st.State).To(Equal(updates.CheckFailed))
			Expect(st.Error).To(Equal(updates.ClassNotListed))
		})
		It("returns an error for an add-on that is not installed and for a canceled caller", func() {
			r := newRig(installed("1.0.0", "development", "development", digestA))
			_, e := r.checker.Check(context.Background(), "other", false)
			Expect(e).To(MatchError(updates.ErrNotInstalled))
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, e = r.checker.Check(ctx, "notes", false)
			Expect(e).To(MatchError(context.Canceled))
		})
	})

	Context("cache and refresh", func() {
		It("answers a status read from the cache and never evaluates or fetches", func() {
			r := newRig(installed("1.0.0", "development", "development", digestA), rel("1.0.0", "development", digestB))
			st, e := r.checker.Status(context.Background(), "notes")
			Expect(e).NotTo(HaveOccurred())
			Expect(st.State).To(Equal(updates.Unknown))
			Expect(r.lib.refreshes.Load()).To(BeZero())
			check(r, false)
			st, _ = r.checker.Status(context.Background(), "notes")
			Expect(st.State).To(Equal(updates.UpdateAvailable))
			Expect(r.lib.refreshes.Load()).To(BeZero())
		})
		It("serves a fresh answer from the cache and evaluates again when it is stale", func() {
			r := newRig(installed("1.0.0", "development", "development", digestA), rel("1.0.0", "development", digestA))
			Expect(check(r, false).State).To(Equal(updates.UpToDate))
			// The library data changes under the cache, not through a refresh.
			r.lib.mu.Lock()
			r.lib.cached = []catalog.Release{rel("1.0.0", "development", digestB)}
			r.lib.mu.Unlock()
			Expect(check(r, false).State).To(Equal(updates.UpToDate), "a fresh cached answer is served")
			*r.clock = r.clock.Add(2 * time.Hour)
			st := check(r, false)
			Expect(st.State).To(Equal(updates.UpdateAvailable))
			Expect(st.Stale).To(BeTrue(), "the library was last fetched longer ago than the time to live")
		})
		It("marks a cached answer older than the time to live as stale", func() {
			r := newRig(installed("1.0.0", "development", "development", digestA), rel("1.0.0", "development", digestA))
			check(r, false)
			r.lib.mu.Lock()
			r.lib.state.LastSuccess = r.clock.Add(3 * time.Hour)
			r.lib.mu.Unlock()
			*r.clock = r.clock.Add(2 * time.Hour)
			st, ok := r.checker.Cached("notes", digestA, 1)
			Expect(ok).To(BeTrue())
			Expect(st.Stale).To(BeTrue())
		})
		It("refreshes the library only on an explicit refresh and applies nothing", func() {
			r := newRig(installed("1.0.0", "development", "development", digestA), rel("1.0.0", "development", digestA))
			r.lib.remote = []catalog.Release{rel("1.0.0", "development", digestB)}
			Expect(check(r, false).State).To(Equal(updates.UpToDate))
			Expect(r.lib.refreshes.Load()).To(BeZero())
			*r.clock = r.clock.Add(time.Second)
			st := check(r, true)
			Expect(r.lib.refreshes.Load()).To(Equal(int32(1)))
			Expect(st.State).To(Equal(updates.UpdateAvailable))
			// The installed package did not change.
			in, _, _ := r.inst.Installation(context.Background(), "notes")
			Expect(in.Receipt.Digest).To(Equal(digestA))
		})
		It("limits explicit refreshes to one per interval and says so", func() {
			r := newRig(installed("1.0.0", "development", "development", digestA), rel("1.0.0", "development", digestA))
			check(r, true)
			st := check(r, true)
			Expect(r.lib.refreshes.Load()).To(Equal(int32(1)))
			Expect(st.Throttled).To(BeTrue())
			*r.clock = r.clock.Add(2 * time.Minute)
			st = check(r, true)
			Expect(r.lib.refreshes.Load()).To(Equal(int32(2)))
			Expect(st.Throttled).To(BeFalse())
		})
		It("shares one check among concurrent callers", func() {
			r := newRig(installed("1.0.0", "development", "development", digestA), rel("1.0.0", "development", digestA))
			r.lib.delay = make(chan struct{})
			r.lib.started = make(chan struct{}, 8)
			var wg sync.WaitGroup
			results := make([]updates.Status, 4)
			for i := range results {
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					st, e := r.checker.Check(context.Background(), "notes", true)
					Expect(e).NotTo(HaveOccurred())
					results[i] = st
				}()
			}
			<-r.lib.started
			Eventually(func() int { return len(r.lib.started) }).Should(BeZero())
			close(r.lib.delay)
			wg.Wait()
			Expect(r.lib.refreshes.Load()).To(Equal(int32(1)))
			for _, st := range results {
				Expect(st.State).To(Equal(updates.UpToDate))
			}
		})
		It("never lets an explicit refresh join a plain evaluation that is already in flight", func() {
			r := newRig(installed("1.0.0", "development", "development", digestA), rel("1.0.0", "development", digestA))
			// Hold the library so a plain evaluation stays in flight.
			r.lib.mu.Lock()
			plain := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				defer close(plain)
				_, _ = r.checker.Check(context.Background(), "notes", false)
			}()
			time.Sleep(50 * time.Millisecond)
			refreshed := make(chan updates.Status, 1)
			go func() {
				defer GinkgoRecover()
				st, e := r.checker.Check(context.Background(), "notes", true)
				Expect(e).NotTo(HaveOccurred())
				refreshed <- st
			}()
			time.Sleep(50 * time.Millisecond)
			r.lib.mu.Unlock()
			Eventually(refreshed, 5*time.Second).Should(Receive())
			<-plain
			Expect(r.lib.refreshes.Load()).To(Equal(int32(1)), "the explicit refresh reached the library")
		})
		It("keeps the work of a canceled caller from spoiling the answer of the others", func() {
			r := newRig(installed("1.0.0", "development", "development", digestA), rel("1.0.0", "development", digestA))
			r.lib.delay = make(chan struct{})
			r.lib.started = make(chan struct{}, 8)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { _, e := r.checker.Check(ctx, "notes", true); done <- e }()
			<-r.lib.started
			cancel()
			Expect(<-done).To(MatchError(context.Canceled))
			close(r.lib.delay)
			Eventually(func() updates.State {
				st, _ := r.checker.Status(context.Background(), "notes")
				return st.State
			}).Should(Equal(updates.UpToDate))
		})
		It("ends the checks in flight when it is closed and refuses new ones", func() {
			r := newRig(installed("1.0.0", "development", "development", digestA), rel("1.0.0", "development", digestA))
			r.lib.delay = make(chan struct{}) // never released: only Close can end the check
			r.lib.started = make(chan struct{}, 8)
			done := make(chan error, 1)
			go func() { _, e := r.checker.Check(context.Background(), "notes", true); done <- e }()
			<-r.lib.started
			closed := make(chan struct{})
			go func() { r.checker.Close(); close(closed) }()
			Eventually(closed).Should(BeClosed(), "Close returns once the check in flight has ended")
			// The caller got an answer, not a hang: the work was canceled
			// and its failure is a status, never an up to date.
			var e error
			Eventually(done).Should(Receive(&e))
			if e == nil {
				st, _ := r.checker.Status(context.Background(), "notes")
				Expect(st.State).NotTo(Equal(updates.UpToDate))
			}
			_, e = r.checker.Check(context.Background(), "notes", false)
			Expect(e).To(MatchError(updates.ErrUnavailable))
			r.checker.Close()
		})
		It("bounds the cache and forgets the answer of a replaced package", func() {
			r := newRig(installed("1.0.0", "development", "development", digestA), rel("1.0.0", "development", digestA))
			check(r, false)
			_, ok := r.checker.Cached("notes", digestB, 1)
			Expect(ok).To(BeFalse(), "another installed digest has no answer yet")
			_, ok = r.checker.Cached("notes", digestA, 2)
			Expect(ok).To(BeFalse(), "another generation has no answer yet")
			r.checker.Forget("notes")
			_, ok = r.checker.Cached("notes", digestA, 1)
			Expect(ok).To(BeFalse())
			for i := range 6 {
				id := fmt.Sprintf("addon-%d", i)
				r.inst.in[id] = updates.Installation{AddonID: id, Generation: 1, Receipt: installed("1.0.0", "development", "development", digestA).Receipt}
				*r.clock = r.clock.Add(time.Second)
				_, e := r.checker.Check(context.Background(), id, false)
				Expect(e).NotTo(HaveOccurred())
			}
			held := 0
			for i := range 6 {
				if _, ok := r.checker.Cached(fmt.Sprintf("addon-%d", i), digestA, 1); ok {
					held++
				}
			}
			Expect(held).To(Equal(3), "the oldest answers were evicted")
		})
	})

	It("redacts the library, channel and digests for a caller who is not an administrator", func() {
		r := newRig(installed("1.0.0", "development", "development", digestA), rel("1.1.0", "development", digestB))
		st := check(r, false)
		view := st.Redacted()
		Expect(view.State).To(Equal(updates.UpdateAvailable))
		Expect(view.InstalledVersion).To(Equal("1.0.0"))
		Expect(view.CandidateVersion).To(Equal("1.1.0"))
		Expect(view.SourceID + view.Channel + view.Selection + view.InstalledDigest + view.CandidateDigest).To(BeEmpty())
	})
})
