package backendupdates_test

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
	. "github.com/mudler/LocalAI/core/services/backendupdates"
)

var _ = Describe("source states", func() {
	now := time.Now()
	earlier := now.Add(-time.Hour)

	DescribeTable("maps what the checker knows to a state",
		func(in SourceStatus, state string, stale bool) {
			got := in.SourceForTest()
			Expect(got.State).To(Equal(state))
			Expect(got.Stale).To(Equal(stale))
		},
		Entry("never checked", SourceStatus{}, StateNever, false),
		Entry("disabled wins", SourceStatus{Disabled: true, Reason: "off", CheckedAt: now}, StateDisabled, false),
		Entry("ok", SourceStatus{CheckedAt: now, LastSuccess: now}, StateOK, false),
		Entry("failed after a good check keeps old data and says so", SourceStatus{CheckedAt: now, LastSuccess: earlier, Err: "boom"}, StateFailed, true),
		Entry("failed and never succeeded", SourceStatus{CheckedAt: now, Err: "boom"}, StateFailed, false),
	)

	It("shows only the reason of a disabled source", func() {
		d := SourceStatus{Disabled: true, Reason: "off", CheckedAt: now, Err: "x"}.SourceForTest()
		Expect(d.CheckedAt).To(BeZero())
		Expect(d.Error).To(BeEmpty())
		Expect(d.Reason).To(Equal("off"))
	})
})

var _ = Describe("Build", func() {
	It("merges the sources, sorts the items and keeps the stale reason", func() {
		now := time.Now()
		snap := Build(Input{
			Gallery: map[string]gallery.UpgradeInfo{
				"zeta":  {BackendName: "zeta", InstalledVersion: "1", AvailableVersion: "2"},
				"alpha": {BackendName: "alpha", InstalledVersion: "1", AvailableVersion: "1", Reason: "older than the server"},
			},
			GalleryStatus: SourceStatus{CheckedAt: now.Add(-time.Minute), LastSuccess: now.Add(-time.Minute)},
			Library:       []backendlib.Update{{Name: "piper", SourceID: "official", InstalledVersion: "1.2.0", Version: "1.3.1", Digest: "sha256:aa", Signed: true}},
			LibraryStatus: SourceStatus{CheckedAt: now, LastSuccess: now},
			AutoBackends:  true,
		})
		var ids []string
		for _, it := range snap.Items {
			ids = append(ids, it.ID)
		}
		Expect(ids).To(Equal([]string{"gallery:alpha", "gallery:zeta", "library:official/piper"}))
		Expect(snap.Items[0].State).To(Equal(ItemStale))
		Expect(snap.Items[0].Reason).NotTo(BeEmpty())
		Expect(snap.Items[1].State).To(Equal(ItemUpdate))
		lib := snap.Items[2]
		Expect(lib.Kind).To(Equal(KindLibrary))
		Expect(lib.SourceID).To(Equal("official"))
		Expect(lib.Signed).To(BeTrue())
		Expect(lib.InstalledVersion).To(Equal("1.2.0"))
		Expect(lib.AvailableVersion).To(Equal("1.3.1"))
		Expect(snap.CheckedAt).To(BeTemporally("==", now))
		Expect(snap.AutoUpdate.Backends).To(BeTrue())
	})

	It("encodes no items as an empty array and no check time as absent", func() {
		raw, err := json.Marshal(Build(Input{}))
		Expect(err).NotTo(HaveOccurred())
		var got map[string]any
		Expect(json.Unmarshal(raw, &got)).To(Succeed())
		Expect(got["items"]).To(BeEmpty())
		Expect(got["items"]).NotTo(BeNil())
		Expect(got).NotTo(HaveKey("checkedAt"))
	})

	It("lists no items of a disabled source", func() {
		snap := Build(Input{
			Gallery:       map[string]gallery.UpgradeInfo{"a": {BackendName: "a"}},
			GalleryStatus: SourceStatus{Disabled: true, Reason: "off"},
			Library:       []backendlib.Update{{Name: "b"}},
			LibraryStatus: SourceStatus{Disabled: true, Reason: "off"},
		})
		Expect(snap.Items).To(BeEmpty())
	})

	It("names drifting nodes and the deduplicated models of the backend on each", func() {
		info := gallery.UpgradeInfo{
			BackendName: "llama-cpp", InstalledVersion: "1", AvailableVersion: "1",
			NodeDrift: []gallery.NodeDriftInfo{{NodeID: "n2", NodeName: "gpu-b", Version: "0.9"}},
			Nodes: []gallery.NodeVersionInfo{
				{NodeID: "n1", NodeName: "gpu-a", NodeStatus: "healthy", Version: "1"},
				{NodeID: "n2", NodeName: "gpu-b", NodeStatus: "healthy", Version: "0.9"},
			},
		}
		snap := Build(Input{
			Gallery:       map[string]gallery.UpgradeInfo{"llama-cpp": info},
			GalleryStatus: SourceStatus{CheckedAt: time.Now(), LastSuccess: time.Now()},
			Loaded: []LoadedModel{
				{NodeID: "n2", Model: "qwen", Backend: "llama-cpp"},
				{NodeID: "n2", Model: "qwen", Backend: "llama-cpp"}, // second replica
				{NodeID: "n2", Model: "whisper", Backend: "whisper"},
				{NodeID: "n1", Model: "gemma", Backend: ""}, // backend not recorded
			},
		})
		it := snap.Items[0]
		Expect(it.State).To(Equal(ItemDrift), "equal versions with a drifting node is drift")
		Expect(it.DriftNodes).To(Equal(1))
		Expect(it.Nodes).To(HaveLen(2))
		Expect(it.Nodes[0].Drift).To(BeFalse())
		Expect(it.Nodes[1].Drift).To(BeTrue())
		Expect(it.Nodes[1].Models).To(Equal([]string{"qwen"}))
		Expect(it.Nodes[0].Models).To(BeEmpty(), "a model without a recorded backend is not guessed")
	})
})
