package modelupdates

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/backendupdates"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
)

// Defaults of the check.
const (
	DefaultInterval     = 6 * time.Hour
	DefaultInitialDelay = 30 * time.Second
	// DefaultCheckTimeout bounds a check-now call, its wait for a check that
	// another frontend runs included.
	DefaultCheckTimeout = 60 * time.Second
	// DefaultCooldown answers a check-now call from the last check when it
	// ended this recently.
	DefaultCooldown = 15 * time.Second
)

// Notifier writes the notification of the models that need a person.
type Notifier interface {
	// ModelUpdates sets the digest of the models that need a person: an
	// update, an edited update or a failed upgrade. An empty list removes it.
	ModelUpdates(ctx context.Context, items []Item) error
}

// Options configure a Service.
type Options struct {
	// SystemState locates the models directory and is used to plan an
	// install.
	SystemState *system.SystemState
	// ListGallery returns the model gallery listing. fresh asks it to read
	// every index again (an explicit check), not the copies of the last hour.
	ListGallery func(ctx context.Context, fresh bool) ([]*gallery.GalleryModel, error)
	// Fetcher reads the base configs of the entries. Nil makes one.
	Fetcher *gallery.BaseConfigFetcher
	// Lock runs fn when no other runner of the check holds the lock and
	// reports whether it ran. In a cluster it is the advisory lock
	// KeyModelUpdateCheck; nil uses a lock of this process.
	Lock func(ctx context.Context, fn func() error) (bool, error)
	// Notifier gets the digest after each check this process runs. May be
	// nil.
	Notifier Notifier

	Now          func() time.Time
	Interval     time.Duration
	InitialDelay time.Duration
	CheckTimeout time.Duration
	Cooldown     time.Duration
	// WaitPoll is how often a check-now call that waits for another
	// frontend's check reads the check file.
	WaitPoll time.Duration
}

// Service is the update check and the read model of model updates. It keeps
// no state of its own beyond a process lock: what it knows is in the models
// directory (see the package doc).
type Service struct {
	opts      Options
	localLock sync.Mutex
}

// New returns a Service. ListGallery and SystemState are required.
func New(opts Options) *Service {
	if opts.Fetcher == nil {
		opts.Fetcher = gallery.NewBaseConfigFetcher()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	if opts.InitialDelay <= 0 {
		opts.InitialDelay = DefaultInitialDelay
	}
	if opts.CheckTimeout <= 0 {
		opts.CheckTimeout = DefaultCheckTimeout
	}
	if opts.Cooldown <= 0 {
		opts.Cooldown = DefaultCooldown
	}
	if opts.WaitPoll <= 0 {
		opts.WaitPoll = 500 * time.Millisecond
	}
	s := &Service{opts: opts}
	if s.opts.Lock == nil {
		s.opts.Lock = func(_ context.Context, fn func() error) (bool, error) {
			if !s.localLock.TryLock() {
				return false, nil
			}
			defer s.localLock.Unlock()
			return true, fn()
		}
	}
	return s
}

func (s *Service) modelsPath() string { return s.opts.SystemState.Model.ModelsPath }

// Run checks every Interval, after InitialDelay, until ctx ends. Each tick
// runs only when this process gets the lock, so in a cluster one frontend
// checks.
func (s *Service) Run(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(s.opts.InitialDelay):
	}
	ticker := time.NewTicker(s.opts.Interval)
	defer ticker.Stop()
	for {
		s.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) tick(ctx context.Context) {
	_, err := s.opts.Lock(ctx, func() error {
		// Fresh: the gallery index cache is per process and lives an hour,
		// and in a cluster any frontend may run this check, so a cached index
		// could hide an update another frontend's upgrade already sees.
		if err := s.runCheck(ctx, true); err != nil {
			xlog.Warn("Model update check failed", "error", err)
		}
		return nil
	})
	if err != nil {
		xlog.Warn("Model update check lock failed", "error", err)
	}
}

// CheckNow runs a check and returns the new read model. When another runner
// holds the lock (a check on another frontend, or one in progress here), it
// waits for that check's result instead. A call within Cooldown of the last
// check is answered from it, with Throttled set. It installs nothing.
func (s *Service) CheckNow(ctx context.Context) Snapshot {
	if cf, err := readCheckFile(s.modelsPath()); err == nil && !cf.CheckedAt.IsZero() && s.opts.Now().Sub(cf.CheckedAt) < s.opts.Cooldown {
		snap := s.Snapshot(ctx)
		snap.Throttled = true
		return snap
	}
	ctx, cancel := context.WithTimeout(ctx, s.opts.CheckTimeout)
	defer cancel()
	start := s.opts.Now()
	ran, err := s.opts.Lock(ctx, func() error { return s.runCheck(ctx, true) })
	if err != nil {
		xlog.Warn("Model update check failed", "error", err)
	}
	if !ran && err == nil {
		s.waitForCheck(ctx, start)
	}
	return s.Snapshot(context.WithoutCancel(ctx))
}

// waitForCheck waits until the check file says a check ended after start.
func (s *Service) waitForCheck(ctx context.Context, start time.Time) {
	ticker := time.NewTicker(s.opts.WaitPoll)
	defer ticker.Stop()
	for {
		if cf, err := readCheckFile(s.modelsPath()); err == nil && !cf.CheckedAt.Before(start) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runCheck is the check. The caller holds the lock.
func (s *Service) runCheck(ctx context.Context, fresh bool) error {
	prev, err := readCheckFile(s.modelsPath())
	if err != nil {
		xlog.Warn("Discarding an unreadable model update check file", "error", err)
		prev = &checkFile{Models: map[string]checkResult{}}
	}
	models, listErr := s.opts.ListGallery(ctx, fresh)
	now := s.opts.Now().UTC()
	if listErr != nil {
		prev.CheckedAt = now
		prev.Error = listErr.Error()
		if err := writeCheckFile(s.modelsPath(), prev); err != nil {
			return errors.Join(listErr, err)
		}
		return listErr
	}
	next := &checkFile{CheckedAt: now, LastSuccessAt: now, Models: map[string]checkResult{}}
	names, _ := gallery.SidecarNames(s.modelsPath())
	for _, name := range names {
		sc, err := gallery.ReadSidecar(s.modelsPath(), name)
		if err != nil || !sc.IsV2() || sc.Origin == nil {
			continue
		}
		next.Models[name] = s.checkModel(ctx, models, name, sc)
	}
	if err := writeCheckFile(s.modelsPath(), next); err != nil {
		return fmt.Errorf("write %s: %w", CheckFileName, err)
	}
	s.Notify(ctx)
	return nil
}

// Notify writes the digest notification from the last check and the gallery
// files as they are now, without reading the galleries. A hold or the end of
// an upgrade changes which models need a person; this shows it at once. The
// digest is set, not added, so a repeat writes the same notification.
func (s *Service) Notify(ctx context.Context) {
	if s.opts.Notifier == nil {
		return
	}
	snap := s.Snapshot(ctx)
	if err := s.opts.Notifier.ModelUpdates(ctx, NeedingPerson(snap.Items)); err != nil {
		xlog.Warn("Could not write the model update notification", "error", err)
	}
}

func (s *Service) checkModel(ctx context.Context, models []*gallery.GalleryModel, name string, sc *gallery.ModelConfig) checkResult {
	r := checkResult{InstalledDigest: sc.Origin.EntryDigest}
	parent, resolved, _ := gallery.InstalledEntry(models, sc.Origin.Gallery, sc.Origin.EntryName, sc.ResolvedVariant)
	if parent == nil {
		r.Gone = true
		return r
	}
	digest, _, err := gallery.CurrentEntryDigest(ctx, s.opts.Fetcher, s.modelsPath(), parent, resolved)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	r.AvailableDigest = digest
	if digest == sc.Origin.EntryDigest {
		return r
	}
	_, plan, err := PlanUpgrade(ctx, s.opts.SystemState, models, name, sc, gallery.WithoutArtifactBinding())
	if err != nil {
		r.Error = err.Error()
		return r
	}
	r.PlannedConfig = string(plan.ConfigYAML)
	r.ChangedFiles = changedFiles(sc.Managed, plan.Gallery)
	return r
}

// PlanUpgrade works out what reinstalling a model from its recorded origin
// would write now, without writing anything (binding an artifact still
// materializes it unless gallery.WithoutArtifactBinding is passed). The
// install carries the new origin.
func PlanUpgrade(ctx context.Context, systemState *system.SystemState, models []*gallery.GalleryModel, name string, sc *gallery.ModelConfig, options ...gallery.InstallOption) (*gallery.GalleryInstall, *gallery.InstallPlan, error) {
	inst, err := gallery.PrepareReinstall(ctx, models, systemState.Model.ModelsPath, name, sc)
	if err != nil {
		return nil, nil, err
	}
	if inst == nil {
		return nil, nil, ErrGone
	}
	plan, err := gallery.PlanInstall(ctx, systemState, inst.Name, &inst.Config, inst.Overrides, false, options...)
	if err != nil {
		return nil, nil, err
	}
	return inst, plan, nil
}

// ErrGone is returned when no configured gallery lists the entry a model came
// from.
var ErrGone = errors.New("no configured gallery lists the entry this model was installed from")

// managedIndex maps the files a model owns by path.
func managedIndex(managed []gallery.ManagedFile) map[string]gallery.ManagedFile {
	out := make(map[string]gallery.ManagedFile, len(managed))
	for _, m := range managed {
		out[m.Path] = m
	}
	return out
}

// sameManaged is the one rule for "the update does not change this file":
// the same checksum (hex, any case) and the same source. A template has no
// source, so only its content hash counts.
func sameManaged(prev, cur gallery.ManagedFile) bool {
	return strings.EqualFold(prev.SHA256, cur.SHA256) && prev.URI == cur.URI
}

// droppedManaged returns the files a model owns whose path is not in listed,
// the files a new version no longer lists.
func droppedManaged(managed []gallery.ManagedFile, listed map[string]bool) []gallery.ManagedFile {
	var out []gallery.ManagedFile
	for _, m := range managed {
		if !listed[m.Path] {
			out = append(out, m)
		}
	}
	return out
}

// changedFiles compares the files a model owns with the files and templates
// of a gallery config.
func changedFiles(managed []gallery.ManagedFile, cfg *gallery.ModelConfig) []FileChange {
	old := managedIndex(managed)
	listed := map[string]bool{}
	var out []FileChange
	for _, cur := range gallery.ManagedFiles(cfg) {
		listed[cur.Path] = true
		prev, ok := old[cur.Path]
		switch {
		case !ok:
			out = append(out, FileChange{Path: cur.Path, Role: cur.Role, Change: ChangeAdded, Checksum: cur.SHA256 != ""})
		case !sameManaged(prev, cur):
			out = append(out, FileChange{Path: cur.Path, Role: cur.Role, Change: ChangeChanged, Checksum: cur.SHA256 != ""})
		}
	}
	for _, m := range droppedManaged(managed, listed) {
		out = append(out, FileChange{Path: m.Path, Role: m.Role, Change: ChangeRemoved, Checksum: m.SHA256 != ""})
	}
	slices.SortFunc(out, func(a, b FileChange) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// downloadsFiles reports whether applying the changes downloads a file (a
// change that is not a prompt template or a removal).
func downloadsFiles(changes []FileChange) bool {
	for _, c := range changes {
		if c.Role != gallery.RoleTemplate && c.Change != ChangeRemoved {
			return true
		}
	}
	return false
}

// Snapshot builds the read model from the gallery files and the check file as
// they are now. The hold flag, an upgrade and an edit show at once; what the
// gallery offers is as of the last check.
func (s *Service) Snapshot(_ context.Context) Snapshot {
	cf, err := readCheckFile(s.modelsPath())
	if err != nil {
		xlog.Warn("Could not read the model update check file", "error", err)
		cf = &checkFile{Models: map[string]checkResult{}}
	}
	snap := Snapshot{CheckedAt: cf.CheckedAt, Sources: map[string]backendupdates.Source{SourceGallery: source(cf)}, Items: []Item{}}
	names, _ := gallery.SidecarNames(s.modelsPath())
	for _, name := range names {
		if it, ok := s.item(name, cf); ok {
			switch it.State {
			case StateUpdateAvailable, StateEdited, StateFailed:
				it.NeedsPerson = true
			}
			snap.Items = append(snap.Items, it)
		}
	}
	return snap
}

func source(cf *checkFile) backendupdates.Source {
	src := backendupdates.Source{CheckedAt: cf.CheckedAt, LastSuccessAt: cf.LastSuccessAt, Error: cf.Error}
	switch {
	case cf.CheckedAt.IsZero():
		src.State = backendupdates.StateNever
	case cf.Error != "":
		src.State = backendupdates.StateFailed
		src.Stale = !cf.LastSuccessAt.IsZero()
	default:
		src.State = backendupdates.StateOK
	}
	return src
}

func (s *Service) item(name string, cf *checkFile) (Item, bool) {
	it := Item{ID: KindModel + ":" + name, Kind: KindModel, Name: name}
	sc, err := gallery.ReadSidecar(s.modelsPath(), name)
	if err != nil {
		it.State, it.Reason, it.Error = StateUnknown, ReasonUnreadable, err.Error()
		return it, true
	}
	if !sc.IsV2() {
		it.State, it.Reason = StateUnknown, ReasonLegacySidecar
		it.EntryName = sc.EntryName
		return it, true
	}
	if sc.Origin == nil {
		// Installed from a config or a URL, not from a gallery entry.
		return it, false
	}
	it.Gallery, it.EntryName, it.InstalledDigest, it.Held = sc.Origin.Gallery, sc.Origin.EntryName, sc.Origin.EntryDigest, sc.Hold
	if j := sc.Upgrade; j != nil && j.Kind == gallery.JournalUpgrade {
		it.Upgrade = upgradeStatus(j)
	}
	r, checked := cf.Models[name]
	if checked {
		it.AvailableDigest = r.AvailableDigest
	}
	switch {
	case sc.Hold:
		it.State = StateHeld
		return it, true
	case it.Upgrade != nil && (it.Upgrade.Phase == gallery.PhaseFailed || it.Upgrade.Phase == gallery.PhaseRolledBack) &&
		(!checked || r.AvailableDigest != sc.Origin.EntryDigest):
		it.State = StateFailed
		return it, true
	case !checked:
		it.State, it.Reason = StateUnknown, ReasonNotChecked
		return it, true
	case r.Gone:
		it.State = StateGone
		return it, true
	case r.Error != "":
		it.State, it.Reason, it.Error = StateUnknown, ReasonCheckFailed, r.Error
		return it, true
	case r.AvailableDigest == sc.Origin.EntryDigest:
		it.State = StateUpToDate
		return it, true
	}
	it.State = StateUpdateAvailable
	it.ChangedFiles = r.ChangedFiles
	it.ConfigOnly = !downloadsFiles(r.ChangedFiles)
	if sc.Origin.ConfigYAML == "" {
		return it, true
	}
	if err := utils.VerifyPath(name+".yaml", s.modelsPath()); err != nil {
		it.State, it.Reason, it.Error = StateUnknown, ReasonUnreadable, err.Error()
		return it, true
	}
	// #nosec G304 -- name was checked against the models directory just above.
	disk, err := os.ReadFile(filepath.Join(s.modelsPath(), name+".yaml"))
	if err != nil {
		it.State, it.Reason, it.Error = StateUnknown, ReasonUnreadable, err.Error()
		return it, true
	}
	var planned []byte
	if r.PlannedConfig != "" {
		planned = []byte(r.PlannedConfig)
	}
	edits, err := DiffEdits([]byte(sc.Origin.ConfigYAML), disk, planned)
	if err != nil {
		it.State, it.Reason, it.Error = StateUnknown, ReasonUnreadable, err.Error()
		return it, true
	}
	it.EditedKeys = edits
	if len(Conflicts(edits)) > 0 {
		it.State = StateEdited
	}
	return it, true
}

func upgradeStatus(j *gallery.Journal) *UpgradeStatus {
	return &UpgradeStatus{
		Phase: j.Phase, OpID: j.OpID, FromDigest: j.FromEntryDigest, ToDigest: j.ToEntryDigest,
		Error: j.Error, Attempts: j.Attempts, ConfigOnly: j.ConfigOnly,
		RetainedFiles: j.RetainedFiles, UpdatedAt: j.UpdatedAt, Running: !j.Terminal(),
		RemovableOld: j.Phase == gallery.PhaseConfirmed && len(j.RetainedFiles) > 0,
		Note:         j.Note,
	}
}

// NeedingPerson returns the items that need a person.
func NeedingPerson(items []Item) []Item {
	var out []Item
	for _, it := range items {
		if it.NeedsPerson {
			out = append(out, it)
		}
	}
	return out
}

// MissingChoices returns the edited keys of a model that the last check found
// the update would also change and that choices does not decide. It reads
// the last check; the upgrade decides again on fresh data.
func (s *Service) MissingChoices(name string, choices map[string]string) []string {
	cf, err := readCheckFile(s.modelsPath())
	if err != nil {
		return nil
	}
	it, ok := s.item(name, cf)
	if !ok {
		return nil
	}
	var missing []string
	for _, key := range Conflicts(it.EditedKeys) {
		if c := choices[key]; c != ChoiceMine && c != ChoiceGallery {
			missing = append(missing, key)
		}
	}
	return missing
}
