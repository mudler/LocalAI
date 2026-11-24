package upgraderun

import (
	"context"
	"strconv"

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

// backendRisks adds what the cluster knows about a backend: the models that
// stop on its nodes, else the nodes it is replaced on, and the nodes that run
// another build now. A standalone server reports no nodes, so it says
// nothing it does not know.
func backendRisks(c *Candidate, it backendupdates.Item) {
	models, nodes := 0, 0
	for _, n := range it.Nodes {
		if len(n.Models) > 0 {
			models += len(n.Models)
			nodes++
		}
	}
	switch {
	case models > 0:
		c.AddRisk(RiskStopsModels, map[string]string{"count": strconv.Itoa(models), "nodes": strconv.Itoa(nodes)})
	case len(it.Nodes) > 1:
		c.AddRisk(RiskCluster, map[string]string{"nodes": strconv.Itoa(len(it.Nodes))})
	}
	if it.DriftNodes > 0 {
		c.AddRisk(RiskDrift, map[string]string{"count": strconv.Itoa(it.DriftNodes)})
	}
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
			c.Blocked = ReasonInstallsDisabled
		}
		backendRisks(&c, it)
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
		// Every model update stops the model and loads the new version as a
		// check, and downloads its files; the review says so once for all
		// models. Only a config-only update is worth a line.
		c := Candidate{Kind: KindModel, Name: it.Name, From: it.InstalledDigest, To: it.AvailableDigest, Risks: []string{}}
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
			c.AddRisk(RiskConfigOnly, nil)
		}
		l.Candidates = append(l.Candidates, c)
	}
	return l
}

// AddonVersions is what the update check of the add-on owner says about one
// add-on with an update.
type AddonVersions struct {
	ID string
	// Title is the name the Add-ons page shows; empty uses ID.
	Title            string
	InstalledVersion string
	InstalledDigest  string
	CandidateVersion string
	CandidateDigest  string
}

// AddonCandidate is the candidate of an add-on with an update. A person reads
// the versions (v0.8.1 to v0.8.2); a request pins the digest of the candidate
// (Pin), so a release that moves a version tag to new content is refused as
// stale, and the job path gets both digests. A version that is missing is
// shown as its digest.
func AddonCandidate(v AddonVersions) Candidate {
	orDigest := func(version, digest string) string {
		if version != "" {
			return version
		}
		return digest
	}
	return Candidate{
		Kind: KindAddon, Name: v.ID, Title: v.Title,
		From: orDigest(v.InstalledVersion, v.InstalledDigest), To: orDigest(v.CandidateVersion, v.CandidateDigest),
		Pin: v.CandidateDigest, Risks: []string{RiskMigratesData},
		Extra: map[string]string{
			ExtraExpectedDigest:  v.InstalledDigest,
			ExtraCandidateDigest: v.CandidateDigest,
		},
	}
}
