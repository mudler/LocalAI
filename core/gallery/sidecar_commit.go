package gallery

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/pkg/atomicfile"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
	"gopkg.in/yaml.v3"
)

// commitInstall writes the gallery file and the model config of an install.
//
// The two writes cannot be one atomic step, so the gallery file is written
// first with an install journal that holds the previous config and gallery
// file, then the config, then the gallery file without the journal. A crash
// leaves a journal, and RecoverInstallJournals either finishes the install
// (the config on disk is the new one) or puts the previous files back.
//
// The hold flag of an earlier install, and a finished upgrade journal (which
// names old files a person may still remove), carry over to the new file.
func commitInstall(basePath string, plan *InstallPlan, origin *Origin, instance string, now time.Time) error {
	name := plan.Name
	configPath := filepath.Join(basePath, name+".yaml")
	sidecarPath := SidecarPath(basePath, name)
	// The read of the old gallery file and the writes that replace it are one
	// step for a config edit that records its hash in the same file.
	defer LockSidecar(name)()

	// #nosec G304 -- sidecarPath is SidecarPath of the plan name, which the
	// planner checked with VerifyPath against basePath; SidecarPath also turns
	// a path separator in the name into "__".
	prevSidecarBytes, prevSidecarErr := os.ReadFile(sidecarPath)
	hadSidecar := prevSidecarErr == nil
	if prevSidecarErr != nil && !errors.Is(prevSidecarErr, os.ErrNotExist) {
		return fmt.Errorf("read gallery file: %w", prevSidecarErr)
	}
	var prev ModelConfig
	if hadSidecar {
		// A damaged old file must not block a reinstall, which is how it gets
		// repaired; only its hold flag and journal are lost.
		if err := yaml.Unmarshal(prevSidecarBytes, &prev); err != nil {
			xlog.Warn("Ignoring a gallery file that does not parse", "model", name, "error", err)
			prev = ModelConfig{}
		}
	}

	next := *plan.Gallery
	next.clearSidecarState()
	next.Version = SidecarVersion
	next.Managed = ManagedFiles(&next)
	next.Hold = prev.Hold
	if prev.Upgrade != nil && prev.Upgrade.Kind == JournalUpgrade && prev.Upgrade.Terminal() && len(prev.Upgrade.RetainedFiles) > 0 {
		next.Upgrade = prev.Upgrade
	}
	if origin != nil {
		o := *origin
		o.InstalledAt = now.UTC()
		if plan.WriteConfig {
			o.ConfigYAML = string(plan.ConfigYAML)
		}
		next.Origin = &o
	}

	if prev.Upgrade != nil && !prev.Upgrade.Terminal() {
		// The journal of a running change owns the gallery file: replacing it
		// would leave that change nothing to finish or undo.
		return fmt.Errorf("%w: a %s of model %s is in the %s phase; wait for it to end", ErrModelLocked, prev.Upgrade.Kind, name, prev.Upgrade.Phase)
	}
	if !plan.WriteConfig {
		return WriteSidecar(basePath, name, &next)
	}
	next.ConfigSHA256 = SHA256Hex(plan.ConfigYAML)

	previous := &Previous{HadSidecar: hadSidecar}
	if hadSidecar {
		previous.Sidecar = base64.StdEncoding.EncodeToString(prevSidecarBytes)
	}
	// #nosec G304 -- configPath is the model config of the plan name, which the
	// planner checked with VerifyPath against basePath.
	if data, err := os.ReadFile(configPath); err == nil {
		previous.HadConfig = true
		previous.ConfigYAML = base64.StdEncoding.EncodeToString(data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read model config: %w", err)
	}
	journaled := next
	journaled.Upgrade = &Journal{
		Kind: JournalInstall, Phase: PhaseCommitting, Instance: instance,
		StartedAt: now.UTC(), UpdatedAt: now.UTC(),
		ToConfigSHA256: next.ConfigSHA256, Previous: previous,
	}
	if err := WriteSidecar(basePath, name, &journaled); err != nil {
		return fmt.Errorf("write gallery file: %w", err)
	}
	if err := atomicfile.Write(configPath, plan.ConfigYAML, 0644); err != nil {
		return fmt.Errorf("failed to atomically write updated config file: %w", err)
	}
	xlog.Debug("Written config file", "file", configPath)
	if err := WriteSidecar(basePath, name, &next); err != nil {
		return fmt.Errorf("write gallery file: %w", err)
	}
	xlog.Debug("Written gallery file", "file", sidecarPath)
	return nil
}

// RecoverInstallJournals finishes or undoes the installs that a crash
// interrupted between writing the gallery file and the model config. act
// decides, for each install journal, whether this process may touch it (in
// distributed mode, its own journals and stale ones). It returns the names of
// the models it recovered.
//
// A journal whose model config has the new hash is finished: the gallery file
// is written without the journal. Otherwise the previous model config and
// gallery file are put back, or removed when there were none.
func RecoverInstallJournals(modelsPath string, act func(*Journal) bool) ([]string, error) {
	names, err := SidecarNames(modelsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var recovered []string
	var errs []error
	for _, name := range names {
		sc, err := ReadSidecar(modelsPath, name)
		if err != nil || sc.Upgrade == nil || sc.Upgrade.Kind != JournalInstall {
			continue
		}
		if act != nil && !act(sc.Upgrade) {
			continue
		}
		if err := recoverInstall(modelsPath, name, sc); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		recovered = append(recovered, name)
	}
	return recovered, errors.Join(errs...)
}

// carriedUpgradeJournal is the finished upgrade journal of the gallery file an
// install replaced, when it still lists old files a person may remove. The
// install carries it over (commitInstall); a recovery that finishes the
// install must too, or those files stay on disk with no way to remove them.
func carriedUpgradeJournal(prev *Previous) *Journal {
	if prev == nil || !prev.HadSidecar {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(prev.Sidecar)
	if err != nil {
		return nil
	}
	var old ModelConfig
	if yaml.Unmarshal(data, &old) != nil {
		return nil
	}
	if j := old.Upgrade; j != nil && j.Kind == JournalUpgrade && j.Terminal() && len(j.RetainedFiles) > 0 {
		return j
	}
	return nil
}

// RecoverInstallJournal finishes or undoes the interrupted install of one
// model, as RecoverInstallJournals does. The caller holds the lock of the
// model. It reports whether there was an install journal to recover.
func RecoverInstallJournal(modelsPath, name string) (bool, error) {
	sc, err := ReadSidecar(modelsPath, name)
	if err != nil || sc.Upgrade == nil || sc.Upgrade.Kind != JournalInstall {
		return false, nil
	}
	return true, recoverInstall(modelsPath, name, sc)
}

func recoverInstall(modelsPath, name string, sc *ModelConfig) error {
	j := sc.Upgrade
	configPath := filepath.Join(modelsPath, name+".yaml")
	// #nosec G304 -- name comes from SidecarNames (a file name found in
	// modelsPath) or from a caller that checked it with VerifyPath.
	if data, err := os.ReadFile(configPath); err == nil && SHA256Hex(data) == j.ToConfigSHA256 {
		finished := *sc
		finished.Upgrade = carriedUpgradeJournal(j.Previous)
		xlog.Info("Finishing an install that a restart interrupted", "model", name)
		return WriteSidecar(modelsPath, name, &finished)
	}
	xlog.Warn("Undoing an install that a restart interrupted", "model", name)
	prev := j.Previous
	if prev == nil {
		prev = &Previous{}
	}
	if prev.HadConfig {
		data, err := base64.StdEncoding.DecodeString(prev.ConfigYAML)
		if err != nil {
			return fmt.Errorf("decode previous model config: %w", err)
		}
		if err := atomicfile.Write(configPath, data, 0644); err != nil {
			return err
		}
	} else if err := utils.RemoveIfPresent(configPath); err != nil {
		return err
	}
	sidecarPath := SidecarPath(modelsPath, name)
	if !prev.HadSidecar {
		return utils.RemoveIfPresent(sidecarPath)
	}
	data, err := base64.StdEncoding.DecodeString(prev.Sidecar)
	if err != nil {
		return fmt.Errorf("decode previous gallery file: %w", err)
	}
	return atomicfile.Write(sidecarPath, data, 0644)
}
