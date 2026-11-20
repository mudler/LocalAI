package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Limits of the copy of one skill by MigrateProjectSkills.
const (
	migrateMaxFolders = 200
	migrateMaxFiles   = 500
)

// ProjectSkillsMigration is the outcome of MigrateProjectSkills.
type ProjectSkillsMigration struct {
	// Copied is the number of skills copied to a project skills folder.
	Copied int
	// Skipped is the number of skills left alone: the source is too large or
	// has no SKILL.md, or another frontend copied it first.
	Skipped int
	// Done is the number of projects left alone because their skills folder
	// exists already: the copy ran for them once, and what the owner deleted
	// from there stays deleted.
	Done int
	// Failed is the number of users whose projects could not all be read or
	// copied; the others were done.
	Failed int
	// Blocked is the number of projects whose old .skills folder was not read
	// because another person may write it.
	Blocked int
}

// MigrateProjectSkills copies the skills of the old location of a project,
// Projects/<project>/.skills/<skill>, to the project layer,
// Projects/<project>/skills/<skill>, for every user. Until now the old folder
// loaded for every chat of its owner; the folder walk does not enter Projects
// any more, so the copy keeps those skills for the project.
//
// It never deletes or changes the source. A project whose skills folder exists
// already is left alone as a whole, even if it is empty: the folder is made by
// the first copy, so the copy runs once per project, and a skill that the
// owner deletes later is not brought back from the old folder at the next
// start. A user whose home cannot be read is logged and skipped, and the other
// users are still done; the error returned joins the failures. A project whose old folder another person may write is
// skipped: the host never loaded that text with the owner's authority, and it
// must not become the owner's skill by a copy. Each copy is one change set.
func (s *Service) MigrateProjectSkills(ctx context.Context) (ProjectSkillsMigration, error) {
	var rep ProjectSkillsMigration
	if !s.Available() {
		return rep, ErrUnavailable
	}
	users, err := s.ws.List(ctx, s.admin, session.UsersRoot)
	if errors.Is(err, workspace.ErrNotFound) {
		return rep, nil
	}
	if err != nil {
		return rep, fmt.Errorf("files: list homes: %w", err)
	}
	var failures []error
	for _, u := range users {
		if u.Kind != workspace.KindDir || !validPrincipal(u.Name) {
			continue
		}
		if err := s.migrateUserProjectSkills(ctx, u.Name, &rep); err != nil {
			rep.Failed++
			failures = append(failures, err)
			xlog.Warn("Project skills: could not copy the old .skills folders of a user, going on with the others", "user", u.Name, "error", err)
		}
	}
	if rep.Copied > 0 || rep.Skipped > 0 || rep.Blocked > 0 || rep.Failed > 0 {
		xlog.Info("Project skills: copied the old .skills folders of projects to skills",
			"copied", rep.Copied, "skipped", rep.Skipped, "blocked", rep.Blocked, "failed", rep.Failed)
	}
	return rep, errors.Join(failures...)
}

func (s *Service) migrateUserProjectSkills(ctx context.Context, user string, rep *ProjectSkillsMigration) error {
	root := session.HomePath(user) + "/" + projectsDir
	projs, err := s.ws.List(ctx, s.admin, root)
	if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, workspace.ErrInvalid) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("files: list projects of %s: %w", user, err)
	}
	var others workspace.Writable
	var othersRead bool
	for _, pr := range projs {
		if pr.Kind != workspace.KindDir || !ValidProjectDir(pr.Name) {
			continue
		}
		src := root + "/" + pr.Name + "/" + skills.SkillsDir
		kids, err := s.ws.List(ctx, s.admin, src)
		if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, workspace.ErrInvalid) {
			continue
		}
		if err != nil {
			return fmt.Errorf("files: list %s: %w", src, err)
		}
		if !othersRead {
			if others, err = s.OthersWrite(ctx, user); err != nil {
				return err
			}
			othersRead = true
		}
		if others.Covers(src) {
			rep.Blocked++
			xlog.Warn("Project skills: the old .skills folder of a project has other writers and was not copied", "project", pr.Name)
			continue
		}
		layer := root + "/" + pr.Name + "/" + skills.SkillsLayerDir
		if _, err := s.ws.Stat(ctx, s.admin, layer); err == nil {
			rep.Done++
			continue
		} else if !errors.Is(err, workspace.ErrNotFound) {
			return fmt.Errorf("files: look at %s: %w", layer, err)
		}
		for _, k := range kids {
			if k.Kind != workspace.KindDir || !skills.ValidName(k.Name) {
				continue
			}
			dst := layer + "/" + k.Name
			ok, err := s.copySkillTree(ctx, src+"/"+k.Name, dst)
			if errors.Is(err, workspace.ErrConflict) || errors.Is(err, workspace.ErrExists) {
				// Another frontend started the same copy: its copy stands.
				rep.Skipped++
				continue
			}
			if err != nil {
				return err
			}
			if ok {
				rep.Copied++
			} else {
				rep.Skipped++
			}
		}
	}
	return nil
}

// copySkillTree copies the folder src to dst in one change set, as the
// administrator. ok is false, and nothing is written, when the folder has no
// SKILL.md or is over the limits of a copy.
func (s *Service) copySkillTree(ctx context.Context, src, dst string) (ok bool, err error) {
	type file struct{ rel string }
	var found []file
	folders := []string{""}
	for n := 0; len(folders) > 0; n++ {
		if n >= migrateMaxFolders {
			return false, nil
		}
		rel := folders[0]
		folders = folders[1:]
		kids, err := s.ws.List(ctx, s.admin, path.Join(src, rel))
		if err != nil {
			if errors.Is(err, workspace.ErrNotFound) {
				return false, nil
			}
			return false, fmt.Errorf("files: list %s: %w", src, err)
		}
		for _, k := range kids {
			krel := path.Join(rel, k.Name)
			if k.Kind == workspace.KindDir {
				folders = append(folders, krel)
				continue
			}
			if len(found) >= migrateMaxFiles {
				return false, nil
			}
			found = append(found, file{krel})
		}
	}
	hasSkill := false
	for _, f := range found {
		if f.rel == skills.SkillFile {
			hasSkill = true
		}
	}
	if !hasSkill {
		return false, nil
	}
	tx, err := s.ws.Begin(ctx, s.admin, "Copy a project skill to the project skills folder")
	if err != nil {
		return false, err
	}
	for _, f := range found {
		r, _, err := s.ws.Open(ctx, s.admin, src+"/"+f.rel)
		if err != nil {
			_ = tx.Rollback()
			return false, fmt.Errorf("files: read %s: %w", src+"/"+f.rel, err)
		}
		_, err = tx.Put(ctx, dst+"/"+f.rel, readerOnly{r}, workspace.PutOptions{})
		_ = r.Close()
		if err != nil {
			_ = tx.Rollback()
			return false, fmt.Errorf("files: write %s: %w", dst+"/"+f.rel, err)
		}
	}
	if _, err := tx.Commit(); err != nil && !errors.Is(err, workspace.ErrCommitted) {
		return false, err
	}
	return true, nil
}

// readerOnly hides the Seek and Close of a file, so a Put reads it as a plain
// stream.
type readerOnly struct{ io.Reader }
