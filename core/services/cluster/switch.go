package cluster

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mudler/xlog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// This file is the protocol of a change of carrier. It moves the cluster row
// through
//
//	stable -> prepare -> commit -> stable
//
// and from prepare back to stable (abort). Every move is a compare-and-set on
// the epoch of the row (CarrierStore.Transition), so two admins, or two
// replicas that both think they lead, cannot interleave. The protocol keeps no
// state outside the row and the instances table: a leader that dies between two
// moves is replaced by one that reads the same row and goes on. Every timeout is
// measured on the clock of the database.
//
// The replicas do the work of a change. In prepare each one builds the target
// carrier, listens on it, and reports its epoch as ready. In commit each one
// starts to publish on the target and confirms with the epoch of the commit.
// The leader (Switch.Drive) only decides when the next move is due.

var (
	// ErrBusy means a change is under way, or another writer started one first.
	ErrBusy = errors.New("a change of carrier is already under way")
	// ErrNotAbortable means there is nothing to abort. Only prepare can be
	// aborted: after the commit the target is the active carrier, and going back
	// is a change like any other.
	ErrNotAbortable = errors.New("only a change that is being prepared can be aborted")
)

// WorkerInfo is what the switch needs to know about one worker.
type WorkerInfo struct {
	ID   string
	Name string
	// Type is the node type: backend or agent.
	Type string
	// Attached lists the carriers the worker is connected to now.
	Attached []Carrier
	// Reports is true when the worker reports which carriers it can follow. A
	// worker that predates carrier switching reports nothing.
	Reports bool
	// Follow lists the carriers the worker can attach to.
	Follow []Carrier
	// FollowError is why the worker cannot follow, as it reported.
	FollowError string
}

func (w WorkerInfo) canFollow(target Carrier) (bool, string) {
	if slices.Contains(w.Attached, target) {
		return true, ""
	}
	if !w.Reports {
		return false, "the worker predates carrier switching: it reports no capabilities and stays on NATS"
	}
	if slices.Contains(w.Follow, target) {
		return true, ""
	}
	reason := fmt.Sprintf("the worker cannot use the %s carrier", target)
	if w.FollowError != "" {
		reason += ": " + w.FollowError
	}
	return false, reason
}

// WorkerSource lists the workers that are alive.
type WorkerSource interface {
	Workers(ctx context.Context) ([]WorkerInfo, error)
}

// InFlight counts the work that a change of carrier can touch. It is
// information for the admin, and it never blocks a change.
type InFlight struct {
	// Loads is the number of models that are loading.
	Loads int `json:"loads"`
	// Jobs is the number of jobs that run.
	Jobs int `json:"jobs"`
	// PendingClaims and ClaimedClaims count the rows of the claim queue.
	PendingClaims int `json:"pending_claims"`
	ClaimedClaims int `json:"claimed_claims"`
}

// WorkSource counts the work in flight.
type WorkSource interface {
	InFlight(ctx context.Context) (InFlight, error)
}

// Kinds of Blocker.
const (
	BlockerBusy    = "busy"
	BlockerSame    = "same"
	BlockerReplica = "replica"
	BlockerWorker  = "worker"
)

// Blocker is one reason a change would not go ahead.
type Blocker struct {
	Kind   string `json:"kind"`
	ID     string `json:"id,omitempty"`
	Reason string `json:"reason"`
	// Forceable is true when a forced request goes ahead anyway.
	Forceable bool `json:"forceable"`
}

// ReplicaStatus is one live frontend replica.
type ReplicaStatus struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	ReadyEpoch  int64  `json:"ready_epoch"`
	ReadyReason string `json:"ready_reason,omitempty"`
	// Availability maps each carrier to the reason the replica cannot build it,
	// empty when it can. A carrier it never looked at is absent.
	Availability    map[Carrier]string `json:"availability,omitempty"`
	AvailabilityAge time.Duration      `json:"availability_age_ns,omitempty"`
}

// WorkerStatus is one worker, and whether it can follow the target.
type WorkerStatus struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Attached    []Carrier `json:"attached,omitempty"`
	Follow      []Carrier `json:"follow,omitempty"`
	FollowError string    `json:"follow_error,omitempty"`
	// CanFollow and Reason are set when the report has a target.
	CanFollow bool   `json:"can_follow"`
	Reason    string `json:"reason,omitempty"`
}

// Report is the state of the cluster for the admin. With a target it is also the
// answer of a dry run.
type Report struct {
	Row      CarrierRow      `json:"row"`
	Active   Carrier         `json:"active"`
	Target   Carrier         `json:"target,omitempty"`
	Epoch    int64           `json:"epoch"`
	State    State           `json:"state"`
	OK       bool            `json:"ok"`
	Blockers []Blocker       `json:"blockers,omitempty"`
	Warnings []string        `json:"warnings,omitempty"`
	Replicas []ReplicaStatus `json:"replicas"`
	Workers  []WorkerStatus  `json:"workers"`
	InFlight InFlight        `json:"in_flight"`
	Timings  Timings         `json:"timings"`
	// DrainRemaining is how long the previous carrier stays attached, when one
	// is draining.
	DrainRemaining time.Duration `json:"drain_remaining_ns,omitempty"`
}

// Request asks for a change of carrier.
type Request struct {
	Target Carrier
	// By names who asked. It goes in the row and in the log.
	By string
	// Force goes ahead past a replica or a worker that is not ready.
	Force bool
}

// BlockedError is returned by Switch.Request when the preflight blocks it. The
// row is unchanged.
type BlockedError struct{ Report Report }

func (e *BlockedError) Error() string {
	parts := make([]string, 0, len(e.Report.Blockers))
	for _, b := range e.Report.Blockers {
		if b.ID != "" {
			parts = append(parts, b.ID+": "+b.Reason)
		} else {
			parts = append(parts, b.Reason)
		}
	}
	return "the change of carrier is blocked: " + strings.Join(parts, "; ")
}

// SwitchOptions are the inputs of NewSwitch.
type SwitchOptions struct {
	Store    *CarrierStore
	Registry *Registry
	// Workers and Work may be nil; the report is then empty on those points.
	Workers WorkerSource
	Work    WorkSource
	// Timings returns the waits of a change, read at each use so that a change of
	// the settings applies to the next decision.
	Timings func() Timings
	// NATSURL returns the address of the NATS server that a change to NATS builds
	// from. Request copies it into the row, so that every replica builds from the
	// same address whatever happens to the setting during the change. It may be
	// nil.
	NATSURL func(ctx context.Context) (string, error)
	// Liveness is the window of replica liveness. InstanceLiveness when zero.
	Liveness time.Duration
	// AvailabilityMaxAge is how old a replica's report of what it can build may
	// be. Two minutes when zero.
	AvailabilityMaxAge time.Duration
	// OnChange is called after every move of the row, with the new row. The
	// application uses it to send a hint to the other replicas, so that they look
	// at the row at once. It is a courtesy: a replica that never hears it reads
	// the row at its next poll. It may be nil.
	OnChange func(CarrierRow)
	Meter    metric.Meter
}

// Switch is the protocol of a change of carrier.
type Switch struct {
	o       SwitchOptions
	changes metric.Int64Counter
	phase   metric.Float64Histogram
}

// NewSwitch returns the protocol over the row and the registry.
func NewSwitch(o SwitchOptions) (*Switch, error) {
	if o.Store == nil || o.Registry == nil {
		return nil, errors.New("a switch needs the carrier store and the instance registry")
	}
	if o.Liveness <= 0 {
		o.Liveness = InstanceLiveness
	}
	if o.AvailabilityMaxAge <= 0 {
		o.AvailabilityMaxAge = 2 * time.Minute
	}
	if o.Timings == nil {
		o.Timings = func() Timings { return DefaultTimings }
	}
	meter := o.Meter
	if meter == nil {
		meter = otel.Meter("github.com/mudler/LocalAI")
	}
	s := &Switch{o: o}
	// An instrument that cannot be created is left nil: a metric must not stop a
	// change of carrier.
	s.changes, _ = meter.Int64Counter("localai_carrier_changes_total",
		metric.WithDescription("Moves of the cluster carrier row made by the leader or an admin, by outcome"))
	s.phase, _ = meter.Float64Histogram("localai_carrier_phase_duration_seconds",
		metric.WithDescription("How long the cluster stayed in prepare and in commit"), metric.WithUnit("s"))
	return s, nil
}

// transition is CarrierStore.Transition, and tells OnChange about a move that was
// made.
func (s *Switch) transition(ctx context.Context, from int64, change Change) (CarrierRow, error) {
	row, err := s.o.Store.Transition(ctx, from, change)
	if err == nil && s.o.OnChange != nil {
		s.o.OnChange(row)
	}
	return row, err
}

func (s *Switch) count(outcome string) {
	if s.changes != nil {
		s.changes.Add(context.Background(), 1, metric.WithAttributes(attribute.String("outcome", outcome)))
	}
}

func (s *Switch) observePhase(phase string, d time.Duration) {
	if s.phase != nil {
		s.phase.Record(context.Background(), d.Seconds(), metric.WithAttributes(attribute.String("phase", phase)))
	}
}

// Status reports the cluster as it is, with no target.
func (s *Switch) Status(ctx context.Context) (Report, error) {
	return s.report(ctx, "")
}

// Preflight reports what a change to target would meet. It changes nothing.
func (s *Switch) Preflight(ctx context.Context, target Carrier) (Report, error) {
	if !target.valid() {
		return Report{}, fmt.Errorf("%w: %q", ErrInvalidCarrier, target)
	}
	return s.report(ctx, target)
}

func (s *Switch) report(ctx context.Context, target Carrier) (Report, error) {
	row, err := s.o.Store.Get(ctx)
	if err != nil {
		return Report{}, err
	}
	r := Report{Row: row, Active: row.Active, Target: target, Epoch: row.Epoch, State: row.State, Timings: s.o.Timings().withDefaults()}

	if row.Draining != "" && row.DrainingUntil != nil {
		if now, err := s.o.Store.DBNow(ctx); err == nil && now.Before(*row.DrainingUntil) {
			r.DrainRemaining = row.DrainingUntil.Sub(now)
		}
	}

	live, err := s.o.Registry.ListLive(ctx, s.o.Liveness)
	if err != nil {
		return Report{}, err
	}
	for _, in := range live {
		rs := ReplicaStatus{ID: in.ID, Version: in.Version, ReadyEpoch: in.ReadyEpoch, ReadyReason: in.ReadyReason, AvailabilityAge: in.AvailabilityAge}
		for _, c := range []Carrier{CarrierNATS, CarrierTunnel} {
			if reason, known := in.AvailabilityFor(c); known {
				if rs.Availability == nil {
					rs.Availability = map[Carrier]string{}
				}
				rs.Availability[c] = reason
			}
		}
		r.Replicas = append(r.Replicas, rs)
	}

	var workers []WorkerInfo
	if s.o.Workers != nil {
		if workers, err = s.o.Workers.Workers(ctx); err != nil {
			return Report{}, err
		}
	}
	for _, w := range workers {
		ws := WorkerStatus{ID: w.ID, Name: w.Name, Type: w.Type, Attached: w.Attached, Follow: w.Follow, FollowError: w.FollowError}
		if target != "" {
			ws.CanFollow, ws.Reason = w.canFollow(target)
		}
		r.Workers = append(r.Workers, ws)
	}
	if s.o.Work != nil {
		if r.InFlight, err = s.o.Work.InFlight(ctx); err != nil {
			return Report{}, err
		}
	}

	if target == "" {
		return r, nil
	}
	switch {
	case row.State != StateStable:
		r.Blockers = append(r.Blockers, Blocker{Kind: BlockerBusy, Reason: fmt.Sprintf("a change to %s is under way (%s); only an abort is accepted", row.Target, row.State)})
	case target == row.Active:
		r.Blockers = append(r.Blockers, Blocker{Kind: BlockerSame, Reason: fmt.Sprintf("%s is the active carrier already", target)})
	}
	if len(r.Blockers) == 0 {
		for _, in := range live {
			reason, known := in.AvailabilityFor(target)
			switch {
			case !known:
				r.Blockers = append(r.Blockers, Blocker{Kind: BlockerReplica, ID: in.ID, Forceable: true,
					Reason: fmt.Sprintf("the replica has not reported whether it can use the %s carrier", target)})
			case in.AvailabilityAge > s.o.AvailabilityMaxAge:
				r.Blockers = append(r.Blockers, Blocker{Kind: BlockerReplica, ID: in.ID, Forceable: true,
					Reason: fmt.Sprintf("what the replica reported about the %s carrier is stale (%s old)", target, in.AvailabilityAge.Round(time.Second))})
			case reason != "":
				r.Blockers = append(r.Blockers, Blocker{Kind: BlockerReplica, ID: in.ID, Forceable: true,
					Reason: fmt.Sprintf("the replica cannot use the %s carrier: %s", target, reason)})
			}
		}
		for _, w := range r.Workers {
			if !w.CanFollow {
				r.Blockers = append(r.Blockers, Blocker{Kind: BlockerWorker, ID: w.ID, Forceable: true, Reason: w.Reason})
			}
		}
		if row.Draining != "" {
			r.Warnings = append(r.Warnings, fmt.Sprintf("the %s carrier is still draining from the last change; this change starts a second drain on top of it", row.Draining))
		}
	}
	r.OK = len(r.Blockers) == 0
	return r, nil
}

// Request starts a change to req.Target: it runs the preflight and, when nothing
// blocks, moves the row to prepare. A blocker that can be forced does not block a
// forced request. The row is unchanged when the request is refused.
func (s *Switch) Request(ctx context.Context, req Request) (CarrierRow, Report, error) {
	report, err := s.Preflight(ctx, req.Target)
	if err != nil {
		return CarrierRow{}, Report{}, err
	}
	var blocking []Blocker
	for _, b := range report.Blockers {
		if b.Kind == BlockerBusy {
			return CarrierRow{}, report, ErrBusy
		}
		if !b.Forceable || !req.Force {
			blocking = append(blocking, b)
		}
	}
	if len(blocking) > 0 {
		report.Blockers = blocking
		return CarrierRow{}, report, &BlockedError{Report: report}
	}
	row := report.Row
	note := ""
	if req.Force && len(report.Blockers) > 0 {
		note = fmt.Sprintf("forced past %d blocker(s)", len(report.Blockers))
	}
	change := Change{
		Active: row.Active, State: StatePrepare, Target: req.Target,
		Draining: row.Draining, DrainingUntil: row.DrainingUntil,
		Force: req.Force, Note: note, By: req.By,
	}
	if req.Target == CarrierNATS && s.o.NATSURL != nil {
		addr, err := s.o.NATSURL(ctx)
		if err != nil {
			return CarrierRow{}, report, fmt.Errorf("reading the NATS address of the cluster: %w", err)
		}
		change.NATSURL = PublicNATSURL(addr)
	}
	next, err := s.transition(ctx, row.Epoch, change)
	if errors.Is(err, ErrStaleEpoch) {
		return CarrierRow{}, report, ErrBusy
	}
	if err != nil {
		return CarrierRow{}, report, err
	}
	s.count("requested")
	xlog.Info("Change of carrier requested", "by", req.By, "from", row.Active, "to", req.Target, "epoch", next.Epoch, "force", req.Force)
	return next, report, nil
}

// Abort returns a cluster that is in prepare to stable on the carrier it had.
func (s *Switch) Abort(ctx context.Context, by string) (CarrierRow, error) {
	row, err := s.o.Store.Get(ctx)
	if err != nil {
		return CarrierRow{}, err
	}
	if row.State != StatePrepare {
		return CarrierRow{}, ErrNotAbortable
	}
	next, err := s.abort(ctx, row, by, fmt.Sprintf("aborted by %s", by))
	if errors.Is(err, ErrStaleEpoch) {
		// The leader moved the row first. Say what it is now.
		return CarrierRow{}, ErrNotAbortable
	}
	return next, err
}

func (s *Switch) abort(ctx context.Context, row CarrierRow, by, note string) (CarrierRow, error) {
	next, err := s.transition(ctx, row.Epoch, Change{
		Active: row.Active, State: StateStable,
		Draining: row.Draining, DrainingUntil: row.DrainingUntil,
		Note: note, By: by,
	})
	if err != nil {
		return CarrierRow{}, err
	}
	s.count("aborted")
	xlog.Warn("Change of carrier aborted", "to", row.Target, "kept", row.Active, "by", by, "reason", note, "epoch", next.Epoch)
	return next, nil
}

// Drive makes the next move that is due, if any. The leader calls it on a tick.
// It is safe to call from any number of replicas: each move is a compare-and-set
// on the epoch that it read, so a second caller finds the row moved and changes
// nothing. A caller that finds the row moved is not in error.
func (s *Switch) Drive(ctx context.Context) error {
	row, err := s.o.Store.Get(ctx)
	if err != nil {
		return err
	}
	now, err := s.o.Store.DBNow(ctx)
	if err != nil {
		return err
	}
	timings := s.o.Timings().withDefaults()
	age := now.Sub(row.ChangedAt)

	switch row.State {
	case StatePrepare:
		err = s.drivePrepare(ctx, row, now, age, timings)
	case StateCommit:
		err = s.driveCommit(ctx, row, age, timings)
	case StateStable:
		err = s.driveDrain(ctx, row, now)
	}
	if errors.Is(err, ErrStaleEpoch) {
		return nil
	}
	return err
}

func (s *Switch) drivePrepare(ctx context.Context, row CarrierRow, now time.Time, age time.Duration, t Timings) error {
	live, err := s.o.Registry.ListLive(ctx, s.o.Liveness)
	if err != nil {
		return err
	}
	var pending, failed []string
	reasons := map[string]string{}
	for _, in := range live {
		switch {
		case in.ReadyEpoch != row.Epoch:
			pending = append(pending, in.ID)
		case in.ReadyReason != "":
			failed = append(failed, in.ID)
			reasons[in.ID] = in.ReadyReason
		}
	}
	describe := func(ids []string) string {
		out := make([]string, len(ids))
		for i, id := range ids {
			out[i] = id
			if r := reasons[id]; r != "" {
				out[i] += " (" + r + ")"
			}
		}
		return strings.Join(out, ", ")
	}

	commit := func(note string) error {
		until := now.Add(t.MaxDrain)
		_, err := s.transition(ctx, row.Epoch, Change{
			Active: row.Target, State: StateCommit, Target: row.Target,
			Draining: row.Active, DrainingUntil: &until,
			Force: row.Force, Note: note, By: row.ChangedBy,
		})
		if err == nil {
			s.count("committed")
			s.observePhase("prepare", age)
			xlog.Info("Change of carrier committed", "to", row.Target, "from", row.Active, "prepared_for", age.Round(time.Millisecond), "note", note)
		}
		return err
	}
	abort := func(note string) error {
		_, err := s.abort(ctx, row, row.ChangedBy, note)
		return err
	}

	switch {
	case len(failed) > 0 && !row.Force:
		return abort("aborted: the replica cannot build the target: " + describe(failed))
	case len(pending) == 0:
		if len(failed) > 0 {
			return commit("committed without the replica that cannot build the target: " + describe(failed))
		}
		return commit("")
	case age >= t.PrepareTimeout && row.Force:
		return commit("committed without the replica that is not ready: " + describe(append(pending, failed...)))
	case age >= t.PrepareTimeout:
		return abort(fmt.Sprintf("aborted: the replica was not ready after %s: %s", t.PrepareTimeout, describe(append(pending, failed...))))
	}
	return nil
}

func (s *Switch) driveCommit(ctx context.Context, row CarrierRow, age time.Duration, t Timings) error {
	live, err := s.o.Registry.ListLive(ctx, s.o.Liveness)
	if err != nil {
		return err
	}
	var missing []string
	for _, in := range live {
		if in.ReadyEpoch != row.Epoch || in.ReadyReason != "" {
			missing = append(missing, in.ID)
		}
	}
	if len(missing) > 0 && age < t.TransitionWindow {
		return nil
	}
	note := row.Note
	if len(missing) > 0 {
		note = strings.TrimSpace(note + " The replica did not confirm the commit within " + t.TransitionWindow.String() + ": " + strings.Join(missing, ", "))
	}
	_, err = s.transition(ctx, row.Epoch, Change{
		Active: row.Active, State: StateStable,
		Draining: row.Draining, DrainingUntil: row.DrainingUntil,
		Force: row.Force, Note: note, By: row.ChangedBy,
	})
	if err == nil {
		s.count("settled")
		s.observePhase("commit", age)
		xlog.Info("Change of carrier settled", "active", row.Active, "draining", row.Draining, "epoch", row.Epoch+1, "unconfirmed", missing)
	}
	return err
}

func (s *Switch) driveDrain(ctx context.Context, row CarrierRow, now time.Time) error {
	if row.Draining == "" {
		return nil
	}
	if row.DrainingUntil != nil && now.Before(*row.DrainingUntil) {
		return nil
	}
	_, err := s.transition(ctx, row.Epoch, Change{
		Active: row.Active, State: StateStable, Note: "drained", By: row.ChangedBy,
	})
	if err == nil {
		s.count("drained")
		xlog.Info("The previous carrier is released", "released", row.Draining, "active", row.Active, "epoch", row.Epoch+1)
	}
	return err
}
