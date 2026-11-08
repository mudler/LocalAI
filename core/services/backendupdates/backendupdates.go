// Package backendupdates builds the read model of backend updates: the
// gallery and library backends that have a newer build, the freshness of each
// source, and what the automatic update covers. It holds no state; the
// upgrade checker owns the cache and hands its parts to Build.
package backendupdates

import (
	"slices"
	"sort"
	"time"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
)

// Source states. A client must not say "no updates" while a source is failed
// or never checked.
const (
	StateOK       = "ok"
	StateFailed   = "failed"
	StateNever    = "never"
	StateDisabled = "disabled"
)

// Item kinds.
const (
	KindGallery = "gallery"
	KindLibrary = "library"
)

// Item states. One per item.
const (
	// ItemUpdate: the gallery or library lists a newer version or digest.
	ItemUpdate = "update"
	// ItemStale: a request showed the backend is older than the server.
	ItemStale = "stale"
	// ItemDrift: the nodes of a cluster do not agree on the build.
	ItemDrift = "drift"
)

// Snapshot is the answer of GET /backends/updates.
type Snapshot struct {
	// CheckedAt is the latest check of any source; zero (omitted) before the
	// first check.
	CheckedAt time.Time `json:"checkedAt,omitzero"`
	// Throttled is set by a check-now call that was answered from a check
	// that ran a few seconds ago.
	Throttled  bool              `json:"throttled,omitempty"`
	Sources    map[string]Source `json:"sources"`
	AutoUpdate AutoUpdate        `json:"autoUpdate"`
	// InstallsDisabled is true when this server refuses backend installs, so
	// no item can be updated here.
	InstallsDisabled bool   `json:"installsDisabled,omitempty"`
	Items            []Item `json:"items"`
}

// Source is the freshness of one source of updates.
type Source struct {
	// State is ok, failed, never or disabled.
	State         string    `json:"state"`
	CheckedAt     time.Time `json:"checkedAt,omitzero"`
	LastSuccessAt time.Time `json:"lastSuccessAt,omitzero"`
	// Error is the error of the last check, empty when it succeeded.
	Error string `json:"error,omitempty"`
	// Stale is true when the items of this source come from an older check
	// than CheckedAt, because the last one failed.
	Stale bool `json:"stale,omitempty"`
	// Reason says why a source is disabled.
	Reason string `json:"reason,omitempty"`
	// Note states a limit of what the source can see.
	Note string `json:"note,omitempty"`
}

// AutoUpdate says what the automatic update covers. The switch is
// auto_upgrade_backends; it covers gallery and library backends alike.
type AutoUpdate struct {
	Backends bool `json:"backends"`
}

// Item is one backend that has an update.
type Item struct {
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	Name             string `json:"name"`
	State            string `json:"state"`
	InstalledVersion string `json:"installedVersion,omitempty"`
	AvailableVersion string `json:"availableVersion,omitempty"`
	InstalledDigest  string `json:"installedDigest,omitempty"`
	AvailableDigest  string `json:"availableDigest,omitempty"`
	// Reason is why the backend is flagged when version and digest alone
	// would not flag it (the stale reason).
	Reason string `json:"reason,omitempty"`
	// SourceID and Signed are set on library items.
	SourceID string `json:"sourceId,omitempty"`
	Signed   bool   `json:"signed,omitempty"`
	// Nodes is every node that reports the backend (cluster only).
	// DriftNodes is the subset that differs from the majority.
	Nodes      []Node `json:"nodes,omitempty"`
	DriftNodes int    `json:"driftNodes,omitempty"`
}

// Node is one node that has the backend.
type Node struct {
	NodeID   string `json:"nodeId"`
	NodeName string `json:"nodeName"`
	Status   string `json:"status,omitempty"`
	Version  string `json:"version,omitempty"`
	Digest   string `json:"digest,omitempty"`
	// Drift is true when the node differs from the cluster majority.
	Drift bool `json:"drift,omitempty"`
	// Models are the models loaded on this node whose recorded backend is
	// this one. The registry records the backend a model was loaded with,
	// which can be empty or a different name than the installed variant, so
	// an empty list does not prove that nothing would stop.
	Models []string `json:"models,omitempty"`
}

// SourceStatus is what the checker knows about one source.
type SourceStatus struct {
	// Disabled, with Reason, when the source cannot be checked here.
	Disabled    bool
	Reason      string
	Note        string
	CheckedAt   time.Time
	LastSuccess time.Time
	Err         string
}

// LoadedModel is a model loaded on a node, with the backend it was loaded
// with.
type LoadedModel struct {
	NodeID  string
	Model   string
	Backend string
}

// Input is everything Build needs.
type Input struct {
	Gallery       map[string]gallery.UpgradeInfo
	GalleryStatus SourceStatus
	Library       []backendlib.Update
	LibraryStatus SourceStatus
	AutoBackends  bool
	// InstallsDisabled mirrors the server setting.
	InstallsDisabled bool
	Loaded           []LoadedModel
	Throttled        bool
}

func (s SourceStatus) source() Source {
	out := Source{CheckedAt: s.CheckedAt, LastSuccessAt: s.LastSuccess, Error: s.Err, Reason: s.Reason, Note: s.Note}
	switch {
	case s.Disabled:
		out.State = StateDisabled
		out.CheckedAt, out.LastSuccessAt, out.Error = time.Time{}, time.Time{}, ""
	case s.CheckedAt.IsZero():
		out.State = StateNever
	case s.Err != "":
		out.State = StateFailed
		out.Stale = !s.LastSuccess.IsZero()
	default:
		out.State = StateOK
	}
	return out
}

// Build assembles the read model. Items are sorted by kind (gallery first),
// then name.
func Build(in Input) Snapshot {
	snap := Snapshot{
		Sources: map[string]Source{
			KindGallery: in.GalleryStatus.source(),
			KindLibrary: in.LibraryStatus.source(),
		},
		AutoUpdate:       AutoUpdate{Backends: in.AutoBackends},
		InstallsDisabled: in.InstallsDisabled,
		Throttled:        in.Throttled,
		Items:            []Item{},
	}
	for _, s := range snap.Sources {
		if s.CheckedAt.After(snap.CheckedAt) {
			snap.CheckedAt = s.CheckedAt
		}
	}

	byNode := map[string][]LoadedModel{}
	for _, m := range in.Loaded {
		byNode[m.NodeID] = append(byNode[m.NodeID], m)
	}

	if !in.GalleryStatus.Disabled {
		for name, u := range in.Gallery {
			snap.Items = append(snap.Items, galleryItem(name, u, byNode))
		}
	}
	if !in.LibraryStatus.Disabled {
		for _, u := range in.Library {
			snap.Items = append(snap.Items, Item{
				ID: KindLibrary + ":" + u.SourceID + "/" + u.Name, Kind: KindLibrary, Name: u.Name, State: ItemUpdate,
				SourceID: u.SourceID, Signed: u.Signed,
				InstalledVersion: u.InstalledVersion, InstalledDigest: u.InstalledDigest,
				AvailableVersion: u.Version, AvailableDigest: u.Digest,
			})
		}
	}
	sort.Slice(snap.Items, func(a, b int) bool {
		x, y := snap.Items[a], snap.Items[b]
		if x.Kind != y.Kind {
			return x.Kind == KindGallery
		}
		if x.Name != y.Name {
			return x.Name < y.Name
		}
		return x.ID < y.ID
	})
	return snap
}

func galleryItem(key string, u gallery.UpgradeInfo, byNode map[string][]LoadedModel) Item {
	name := u.BackendName
	if name == "" {
		name = key
	}
	it := Item{
		ID: KindGallery + ":" + name, Kind: KindGallery, Name: name, State: ItemUpdate,
		InstalledVersion: u.InstalledVersion, AvailableVersion: u.AvailableVersion,
		InstalledDigest: u.InstalledDigest, AvailableDigest: u.AvailableDigest,
		Reason: u.Reason,
	}
	drift := map[string]bool{}
	for _, d := range u.NodeDrift {
		drift[d.NodeID] = true
	}
	it.DriftNodes = len(drift)
	for _, n := range u.Nodes {
		node := Node{NodeID: n.NodeID, NodeName: n.NodeName, Status: n.NodeStatus, Version: n.Version, Digest: n.Digest, Drift: drift[n.NodeID]}
		for _, m := range byNode[n.NodeID] {
			if m.Backend == name {
				node.Models = append(node.Models, m.Model)
			}
		}
		node.Models = uniqueSorted(node.Models)
		it.Nodes = append(it.Nodes, node)
	}
	switch {
	case u.Reason != "":
		it.State = ItemStale
	case u.InstalledVersion == u.AvailableVersion && u.InstalledDigest == u.AvailableDigest && len(drift) > 0,
		u.AvailableVersion == "" && u.AvailableDigest == "" && len(drift) > 0:
		it.State = ItemDrift
	}
	return it
}

func uniqueSorted(in []string) []string {
	sort.Strings(in)
	return slices.Compact(in)
}

// PendingUpdates is the list of updates that need a person, with the same rule as the
// badge of the Updates page (badgeOf in utils/updates.js): every listed item
// counts, unless the automatic update will apply it, or this server refuses
// installs so nobody can act on it. The ids name an item and the version or
// digest it would move to, so a newer release of the same backend is a new id.
//
// complete is false when a source that could be checked has not answered (it
// failed, or it was never checked). The list is then missing items of that
// source, and a caller that removes or shrinks something because an id is
// absent must not do it.
func PendingUpdates(s Snapshot) (items []Item, ids []string, complete bool) {
	complete = true
	for _, src := range s.Sources {
		if src.State == StateFailed || src.State == StateNever {
			complete = false
		}
	}
	if s.AutoUpdate.Backends || s.InstallsDisabled {
		return nil, nil, complete
	}
	for _, it := range s.Items {
		target := it.AvailableVersion
		if target == "" {
			target = it.AvailableDigest
		}
		if target == "" {
			target = it.State
		}
		items = append(items, it)
		ids = append(ids, it.ID+"@"+target)
	}
	return items, ids, complete
}
