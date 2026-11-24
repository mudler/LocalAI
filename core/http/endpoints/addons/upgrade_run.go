// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/mudler/xlog"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/http/auth"
	managerpkg "github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/updates"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/upgraderun"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/zipformat"
)

// An upgrade run (core/services/upgraderun) upgrades an add-on through the
// same path as POST /api/addons/{id}/upgrade: the owner records the upgrade
// for the administrator who asked (update-queue, with the pinned digests and
// every check of the explicit upgrade), and the operation queue runs it for
// that administrator (update-run-queued), when the pins are checked again.
// Nothing here acts as a system identity, and nothing skips a check: an item
// the explicit upgrade would refuse is refused, with the owner's type.

// RunConfirmID is the value of the confirmation header (X-LocalAI-Confirm) a
// browser sends with a request that starts or retries an upgrade run. A run
// whose request did not come from the administrator's own browser session
// with this header skips its add-on items (error key not_human_surface), as
// the explicit upgrade refuses such a request.
const RunConfirmID = "upgrade-run"

// Error keys of an add-on item that the host gives (see the ErrKey constants
// of upgraderun). The owner's refusal types (stale_installed,
// candidate_changed, upgrade_conflict, ...) and failure codes
// (upgrade_failed, approval_required, ...) pass through unchanged.
const (
	RunErrNotHumanSurface = upgraderun.ErrKeyNotHumanSurface
	RunErrForbidden       = upgraderun.ErrKeyForbidden
	RunErrUnavailable     = upgraderun.ErrKeyUnavailable
)

// runActor is the actor of a run as it is stored in the run (Run.Actor).
type runActor struct {
	User       string `json:"user"`
	Credential string `json:"credential,omitempty"`
	Mode       string `json:"mode,omitempty"`
	// Human is set when the request came from the administrator's own
	// browser session with the confirmation header.
	Human bool `json:"human,omitempty"`
}

// EncodeRunActor writes the actor of a run. The credential is the reference
// the add-on owner checks (a session or key id, never a secret).
func EncodeRunActor(a contract.Actor, human bool) string {
	b, _ := json.Marshal(runActor{User: a.UserID, Credential: a.CredentialID, Mode: a.Mode, Human: human})
	return string(b)
}

// DecodeRunActor reads an actor written by EncodeRunActor. ok is false for an
// actor that carries no add-on identity (a plain user id).
func DecodeRunActor(s string) (a contract.Actor, human bool, ok bool) {
	var r runActor
	if s == "" || s[0] != '{' || json.Unmarshal([]byte(s), &r) != nil || r.User == "" || r.Mode == "" {
		return contract.Actor{}, false, false
	}
	return contract.Actor{UserID: r.User, CredentialID: r.Credential, Mode: r.Mode}, r.Human, true
}

// RunActorUser is the user an actor of a run names, for display: the
// credential reference stays on the server.
func RunActorUser(s string) string {
	if a, _, ok := DecodeRunActor(s); ok {
		return a.UserID
	}
	return s
}

// RunPrincipal is the actor of a run requested through c: the add-on actor of
// the caller, with the browser session check of the explicit upgrade, when
// the caller is an add-on administrator; otherwise the plain user id, which
// upgrades backends and models only (its add-on items are skipped).
func RunPrincipal(c echo.Context, db *gorm.DB, mode string) string {
	user := ""
	if u := auth.GetUser(c); u != nil {
		user = u.ID
	}
	s := resolve([]Option{WithOwnership(mode)})
	a, e := actor(c, db, s)
	if e != nil || administrator(c, db, a, s) != nil {
		return user
	}
	return EncodeRunActor(a, humanSurface(c, s, a, RunConfirmID))
}

// QueuedUpgrader starts the add-on items of upgrade runs
// (upgraderun.AddonUpgrader).
//
// The operation the upgrade route recorded on the owner is stored with the
// item (upgraderun.ExtraOperationID), not in this process, so whichever
// frontend runs the item runs that record. As an observer of the runs
// (upgraderun.Observer) it drops a recorded operation whose item ended
// without starting it, so the add-on is free again. Distributed mode: the
// observer acts on Local changes only, on the frontend that runs the run;
// dropped holds the items it already dropped on this process, and a second
// drop after a takeover is harmless (the owner answers the dropped record).
type QueuedUpgrader struct {
	Gateway func() *gateway.Gateway
	Queue   func() *galleryop.GalleryService
	// OpCache lists the operation in /api/operations; nil does not.
	OpCache func() *galleryop.OpCache

	dropped sync.Map
}

func (u *QueuedUpgrader) gateway() *gateway.Gateway {
	if u.Gateway == nil {
		return nil
	}
	return u.Gateway()
}

func (u *QueuedUpgrader) queue() *galleryop.GalleryService {
	if u.Queue == nil {
		return nil
	}
	return u.Queue()
}

// ownerRefusal is the start error of an add-on item that the owner refused
// with a typed failure. Whether a retry can help is the retry table's answer
// for the type (upgraderun.RetryableKey), the same answer the item of any
// other failure gets: a moved installed version or candidate, a failed check,
// an owner that did not answer or an upgrade that failed and kept the
// installed version can be tried again; an incompatible release, a conflict
// or a refusal that needs a person cannot.
func ownerRefusal(f *gateway.UpdateFailure) error {
	return &upgraderun.StartError{ErrorKey: f.Type, Retryable: upgraderun.RetryableKey(f.Type), Err: errors.New(f.Message)}
}

// StartAddonUpgrade records the upgrade on the owner as the run's actor and
// queues it. See the comment at the top of this file.
func (u *QueuedUpgrader) StartAddonUpgrade(ctx context.Context, actorText, addonID, expectedDigest, candidateDigest, opID string) (string, error) {
	a, human, ok := DecodeRunActor(actorText)
	if actorText == managerpkg.AutoUpgradeActor.UserID {
		// The automatic update upgrades its add-on items on the owner's own
		// process (the application's automatic add-on starter). Its system
		// actor never goes through the gateway, whose admin check refuses it.
		ok = false
	}
	if !ok {
		return "", &upgraderun.StartError{ErrorKey: RunErrForbidden, Skip: true,
			Err: errors.New("the run was not requested by an add-on administrator; upgrade the add-on from its page")}
	}
	if !human {
		return "", &upgraderun.StartError{ErrorKey: RunErrNotHumanSurface, Skip: true,
			Err: errors.New("an add-on upgrade needs your own browser session; upgrade the add-on from its page")}
	}
	g, q := u.gateway(), u.queue()
	if g == nil || q == nil {
		return "", &upgraderun.StartError{ErrorKey: RunErrUnavailable, Retryable: true, Err: errors.New("add-on upgrades are not available on this server")}
	}
	if expectedDigest == "" || candidateDigest == "" {
		return "", &upgraderun.StartError{ErrorKey: upgraderun.ErrKeyOperationConflict, Err: errors.New("the installed and the candidate digest are required")}
	}
	if opID == "" {
		opID = "run-" + uuid.NewString()
		args, _ := json.Marshal(struct {
			ExpectedDigest  string `json:"expectedDigest"`
			CandidateDigest string `json:"candidateDigest"`
		}{expectedDigest, candidateDigest})
		out, e := g.Dispatch(ctx, a, contract.Request{AddonID: addonID, OperationID: opID, Action: gateway.ActionUpdateQueue, Arguments: args})
		if e != nil {
			if gateway.StatusFor(e) == http.StatusServiceUnavailable {
				return "", &upgraderun.StartError{ErrorKey: RunErrUnavailable, Retryable: true, Err: errors.New("the add-on owner did not answer")}
			}
			return "", &upgraderun.StartError{ErrorKey: RunErrForbidden, Err: errors.New("the add-on owner refused the upgrade for this account")}
		}
		var env gateway.UpdateEnvelope
		if json.Unmarshal(out.Payload, &env) != nil {
			return "", &upgraderun.StartError{ErrorKey: RunErrUnavailable, Retryable: true, Err: errors.New("unreadable answer of the add-on owner")}
		}
		if env.Error != nil {
			return "", ownerRefusal(env.Error)
		}
		var st managerpkg.OperationStatus
		if json.Unmarshal(env.Data, &st) != nil || st.Phase != "accepted" {
			return "", &upgraderun.StartError{ErrorKey: upgraderun.ErrKeyOperationConflict, Err: fmt.Errorf("the add-on owner did not accept the upgrade (%s)", st.Phase)}
		}
	}
	var opcache addonOpCache
	if u.OpCache != nil {
		if oc := u.OpCache(); oc != nil {
			opcache = oc
		}
	}
	return enqueueRecordedUpgrade(g, q, opcache, a, addonID, opID, true), nil
}

// WithUpgradeRuns makes POST /api/addons/{id}/upgrade start an upgrade run of
// one item through upgrader, when runs returns a service. Without it, or
// while runs returns nil, the route queues the upgrade directly.
func WithUpgradeRuns(runs func() *upgraderun.Service, upgrader *QueuedUpgrader) Option {
	return func(s *settings) { s.runs, s.upgrader = runs, upgrader }
}

// afterRefusal is a typed answer of an afterUpdate step.
type afterRefusal struct {
	status  int
	kind    string
	message string
}

func (r *afterRefusal) Error() string { return r.kind + ": " + r.message }

// runStartWait bounds how long the upgrade route waits for its run to queue
// the operation; a variable so tests can shorten it.
var runStartWait = upgraderun.DefaultStartWait

// ctxUpdateRequest holds the decoded body of an update mutation, for the
// step that runs after the owner answered.
const ctxUpdateRequest = "addon.update.request"

// runUpgrade hands an upgrade the owner recorded under the caller's
// operation ID to a run of one item, and answers as queueUpgrade does, with
// the run added. A run that cannot start drops the record, so the add-on is
// free again.
func runUpgrade(c echo.Context, g *gateway.Gateway, s settings, svc *upgraderun.Service, a contract.Actor, id, opID string, status json.RawMessage) (json.RawMessage, int, error) {
	var view map[string]any
	if json.Unmarshal(status, &view) != nil {
		return nil, 0, errors.New("unreadable operation status")
	}
	// A repeated request answers the recorded operation and queues nothing.
	if phase, _ := view["Phase"].(string); phase != "accepted" {
		return status, 0, nil
	}
	ctx := c.Request().Context()
	req, _ := c.Get(ctxUpdateRequest).(*UpgradeRequest)
	if req == nil {
		data, err := queueUpgrade(g, s, a, id, opID, status)
		return data, 0, err
	}
	drop := func() { dropRecorded(context.WithoutCancel(ctx), g, a, id, opID) }
	run, item, err := svc.StartOne(ctx, EncodeRunActor(a, true), upgraderun.StartItem{Kind: upgraderun.KindAddon, Name: id, ExpectedTo: req.CandidateDigest,
		Extra: map[string]string{upgraderun.ExtraOperationID: opID}}, upgraderun.SourceManual, runStartWait)
	switch upgraderun.CodeOf(err) {
	case "":
	case upgraderun.CodeRunActive:
		drop()
		return nil, 0, &afterRefusal{status: http.StatusConflict, kind: upgraderun.CodeRunActive, message: "Another upgrade run is running. Wait for it to end, or cancel it, and upgrade again"}
	case upgraderun.CodeNoUpdate, upgraderun.CodeSourceUnchecked, upgraderun.CodeStale:
		// The run's check does not list the add-on as the owner just did
		// (the check is older, or this frontend does not list add-ons): the
		// owner accepted the upgrade, so queue it as before.
		data, err := queueUpgrade(g, s, a, id, opID, status)
		return data, 0, err
	default:
		drop()
		return nil, 0, err
	}
	out := upgraderun.SingleOutcome(run, item)
	answer := http.StatusOK
	switch out.State {
	case upgraderun.SingleRefused:
		drop()
		return nil, 0, &afterRefusal{status: http.StatusConflict, kind: out.ErrorKey, message: out.Error}
	case upgraderun.SingleStarted:
		view["jobId"] = out.JobID
	case upgraderun.SinglePending:
		// The run keeps the recorded operation and starts it later: the
		// answer has the run to follow and no job yet.
		answer = http.StatusAccepted
	}
	// The actor names the user only; the credential reference it carries
	// stays on the server.
	run = out.Run
	run.Actor = RunActorUser(run.Actor)
	view["run"] = run
	view["runId"] = run.ID
	data, err := json.Marshal(view)
	return data, answer, err
}

// dropRecorded drops an upgrade the owner recorded and nothing started, so
// the add-on is free again. A record that already ran or was dropped stays
// as it is.
func dropRecorded(ctx context.Context, g *gateway.Gateway, a contract.Actor, id, opID string) {
	if g == nil {
		return
	}
	if _, err := g.Dispatch(ctx, a, contract.Request{AddonID: id, OperationID: opID, Action: gateway.ActionUpdateDropQueued, Arguments: json.RawMessage(`{}`)}); err != nil {
		xlog.Warn("could not drop a recorded add-on upgrade; the add-on stays busy until the record ends", "addon", id, "operation", opID, "error", err)
	}
}

// RunChanged drops the recorded operation of an add-on item that ended
// without starting it: cancelled while queued, skipped, refused, or failed
// after the upgrade route stopped waiting.
func (u *QueuedUpgrader) RunChanged(c upgraderun.Change) {
	if !c.Local {
		return
	}
	for _, it := range c.Run.Items {
		opID := it.Extra[upgraderun.ExtraOperationID]
		if it.Kind != upgraderun.KindAddon || opID == "" || it.JobID != "" || !it.Phase.Terminal() {
			continue
		}
		if _, done := u.dropped.LoadOrStore(c.Run.ID+"\x00"+it.Key, struct{}{}); done {
			continue
		}
		a, _, ok := DecodeRunActor(c.Run.Actor)
		if !ok {
			continue
		}
		// The observer runs on the runner's goroutine and must return
		// quickly: the owner answers on its own time.
		go dropRecorded(context.Background(), u.gateway(), a, it.Name, opID)
	}
}

// GatewayUpdateChecker lists the add-ons with an update for upgrade runs
// (upgraderun.Checker), through the gateway, as the actor of the run: the
// gateway serves the owner's answer on the owner and forwards to it on every
// other frontend, so a preview, a start and the checks of a run see the same
// add-ons wherever they run. A run with no add-on identity cannot list them,
// and the kind is then unchecked rather than empty.
type GatewayUpdateChecker struct {
	Gateway func() *gateway.Gateway
}

func (GatewayUpdateChecker) Kinds() []upgraderun.Kind { return []upgraderun.Kind{upgraderun.KindAddon} }

// runActorOf is the add-on actor of the run in ctx. The system actor of the
// automatic update has none here: an automatic run lists its add-ons on the
// owner's process, never through the gateway.
func runActorOf(ctx context.Context) (contract.Actor, bool) {
	text := upgraderun.ActorFrom(ctx)
	if text == managerpkg.AutoUpgradeActor.UserID {
		return contract.Actor{}, false
	}
	a, _, ok := DecodeRunActor(text)
	return a, ok
}

// dispatch makes one read of the owner, each with its own operation ID as
// GetUpdate does.
func (GatewayUpdateChecker) dispatch(ctx context.Context, g *gateway.Gateway, a contract.Actor, id, action string) (json.RawMessage, error) {
	out, e := g.Dispatch(ctx, a, contract.Request{AddonID: id, OperationID: "run-check-" + uuid.NewString(), Action: action, Arguments: json.RawMessage(`{}`)})
	if e != nil {
		return nil, e
	}
	return out.Payload, nil
}

func (c GatewayUpdateChecker) List(ctx context.Context, fresh bool) (upgraderun.Listing, error) {
	a, ok := runActorOf(ctx)
	if !ok {
		return upgraderun.Listing{}, errors.New("the run has no add-on identity")
	}
	var g *gateway.Gateway
	if c.Gateway != nil {
		g = c.Gateway()
	}
	if g == nil {
		return upgraderun.Listing{}, errors.New("add-ons are not available on this server")
	}
	raw, err := c.dispatch(ctx, g, a, "", "addons-installed")
	if err != nil {
		return upgraderun.Listing{}, err
	}
	var installed struct {
		Addons []struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			SourceID string `json:"sourceId"`
		} `json:"addons"`
	}
	if json.Unmarshal(raw, &installed) != nil {
		return upgraderun.Listing{}, errors.New("unreadable list of installed add-ons")
	}
	action := gateway.ActionUpdateStatus
	if fresh {
		action = gateway.ActionUpdateCheck
	}
	l := upgraderun.Listing{Complete: true}
	for _, in := range installed.Addons {
		if in.SourceID == zipformat.SourceID {
			// An add-on installed from a file has no library to update from.
			continue
		}
		raw, err := c.dispatch(ctx, g, a, in.ID, action)
		var env gateway.UpdateEnvelope
		var st updates.Status
		if err != nil || json.Unmarshal(raw, &env) != nil || env.Error != nil || json.Unmarshal(env.Data, &st) != nil {
			l.Complete = false
			continue
		}
		if st.Throttled {
			l.Throttled = true
		}
		switch st.State {
		case updates.CheckFailed, updates.Unknown:
			l.Complete = false
		}
		if st.State != updates.UpdateAvailable || st.InstalledDigest == "" || st.CandidateDigest == "" {
			continue
		}
		l.Candidates = append(l.Candidates, upgraderun.AddonCandidate(upgraderun.AddonVersions{
			ID: in.ID, Title: in.Title,
			InstalledVersion: st.InstalledVersion, InstalledDigest: st.InstalledDigest,
			CandidateVersion: st.CandidateVersion, CandidateDigest: st.CandidateDigest,
		}))
	}
	return l, nil
}
