package upgraderun

import (
	"context"

	"github.com/mudler/LocalAI/core/services/backendupdates"
	"github.com/mudler/LocalAI/core/services/modelupdates"
)

// BackendSnapshots is the backend update check (the upgrade checker of the
// application implements it).
type BackendSnapshots interface {
	UpdatesSnapshot(ctx context.Context) backendupdates.Snapshot
	CheckNow(ctx context.Context) backendupdates.Snapshot
}

// BackendChecker lists the gallery and library backends with an update, from
// the read model of GET /api/backends/updates.
type BackendChecker struct {
	Snapshots BackendSnapshots
}

// Extra keys of a library item.
const (
	ExtraSourceID = "source_id"
	ExtraBackend  = "backend"
	ExtraVersion  = "version"
	// ExtraRetry marks a model whose last upgrade failed: the upgrade runs as
	// a retry.
	ExtraRetry = "retry"
)

func (BackendChecker) Kinds() []Kind { return []Kind{KindGallery, KindLibrary} }

func (b BackendChecker) List(ctx context.Context, fresh bool) (Listing, error) {
	if b.Snapshots == nil {
		return Listing{Complete: true}, nil
	}
	var snap backendupdates.Snapshot
	if fresh {
		snap = b.Snapshots.CheckNow(ctx)
	} else {
		snap = b.Snapshots.UpdatesSnapshot(ctx)
	}
	return backendListing(snap), nil
}

// backendTarget is what an item moves to, with the rule of
// backendupdates.PendingUpdates: the version, else the digest, else the
// state.
func backendTarget(it backendupdates.Item) string {
	switch {
	case it.AvailableVersion != "":
		return it.AvailableVersion
	case it.AvailableDigest != "":
		return it.AvailableDigest
	}
	return it.State
}

func backendFrom(it backendupdates.Item) string {
	if it.InstalledVersion != "" {
		return it.InstalledVersion
	}
	return it.InstalledDigest
}

func backendListing(snap backendupdates.Snapshot) Listing {
	l := Listing{Complete: true, Throttled: snap.Throttled}
	for _, src := range snap.Sources {
		if src.State == backendupdates.StateFailed || src.State == backendupdates.StateNever {
			l.Complete = false
		}
	}
	for _, it := range snap.Items {
		c := Candidate{From: backendFrom(it), To: backendTarget(it), Risks: []string{}}
		switch it.Kind {
		case backendupdates.KindGallery:
			c.Kind, c.Name = KindGallery, it.Name
		case backendupdates.KindLibrary:
			c.Kind, c.Name = KindLibrary, it.SourceID+"/"+it.Name
			c.Extra = map[string]string{ExtraSourceID: it.SourceID, ExtraBackend: it.Name, ExtraVersion: it.AvailableVersion}
		default:
			continue
		}
		if snap.InstallsDisabled {
			c.Blocked = "installs_disabled"
		}
		stops := false
		for _, n := range it.Nodes {
			if len(n.Models) > 0 {
				stops = true
			}
		}
		if len(it.Nodes) > 0 {
			c.Risks = append(c.Risks, RiskCluster)
		}
		if stops {
			c.Risks = append(c.Risks, RiskStopsModels)
		}
		if it.DriftNodes > 0 {
			c.Risks = append(c.Risks, RiskDrift)
		}
		l.Candidates = append(l.Candidates, c)
	}
	return l
}

// ModelSnapshots is the model update check (modelupdates.Service implements
// it).
type ModelSnapshots interface {
	Snapshot(ctx context.Context) modelupdates.Snapshot
	CheckNow(ctx context.Context) modelupdates.Snapshot
}

// ModelChecker lists the gallery models with an update, from the read model
// of GET /api/models/updates. A held model and one whose update changes
// edited keys are listed, so a plan can show why they are skipped.
type ModelChecker struct {
	Snapshots ModelSnapshots
}

func (ModelChecker) Kinds() []Kind { return []Kind{KindModel} }

func (m ModelChecker) List(ctx context.Context, fresh bool) (Listing, error) {
	if m.Snapshots == nil {
		return Listing{Complete: true}, nil
	}
	var snap modelupdates.Snapshot
	if fresh {
		snap = m.Snapshots.CheckNow(ctx)
	} else {
		snap = m.Snapshots.Snapshot(ctx)
	}
	return modelListing(snap), nil
}

func modelListing(snap modelupdates.Snapshot) Listing {
	l := Listing{Complete: true, Throttled: snap.Throttled}
	for _, src := range snap.Sources {
		if src.State == backendupdates.StateFailed || src.State == backendupdates.StateNever {
			l.Complete = false
		}
	}
	for _, it := range snap.Items {
		c := Candidate{Kind: KindModel, Name: it.Name, From: it.InstalledDigest, To: it.AvailableDigest, Risks: []string{RiskStopsModel}}
		switch it.State {
		case modelupdates.StateUpdateAvailable:
		case modelupdates.StateEdited:
			for _, k := range it.EditedKeys {
				if k.Conflict {
					c.EditedKeys = append(c.EditedKeys, k.Key)
				}
			}
		case modelupdates.StateHeld:
			// A held model is listed only when its entry moved, so the plan
			// says why it is skipped; a held model with nothing new is not a
			// candidate.
			if it.AvailableDigest == "" || it.AvailableDigest == it.InstalledDigest {
				continue
			}
			c.Held = true
		case modelupdates.StateFailed:
			c.Extra = map[string]string{ExtraRetry: "true"}
		default:
			continue
		}
		if it.Held {
			c.Held = true
		}
		if it.ConfigOnly {
			c.Risks = append(c.Risks, RiskConfigOnly)
		} else {
			c.Risks = append(c.Risks, RiskDownloads)
		}
		l.Candidates = append(l.Candidates, c)
	}
	return l
}
