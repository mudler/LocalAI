// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/sharedfolders"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// When the owner of a folder revokes or lowers a grant, the store refuses the
// grantee's next file call at once. A turn that is running keeps the view it
// was given, so it would go on with a folder it lost and only fail call by
// call; a command that runs in its sandbox keeps its copy until it ends. The
// turn is therefore stopped: this process stops its own turns before the
// owner's call answers, and the same request goes on the chat bus to every
// other process (a frontend or a chat worker), which stops its own.

// folderAccessKey is the control key every process listens on for a lost
// folder grant. It cannot be a chat key: those hold a NUL between the
// principal and the chat ID, and this one starts with it.
const folderAccessKey = "\x00shared-folders"

// folderStopWait bounds how long the owner's call waits for one turn to stop.
const folderStopWait = 10 * time.Second

// folderAccessLost is the hook of the shared-folder service.
func (s *Service) folderAccessLost(ctx context.Context, principal string) {
	s.stopNarrowed(ctx, principal)
	s.bus.Control(folderAccessKey, ControlMsg{FolderAccess: principal})
}

func (s *Service) onFolderAccess(m ControlMsg) {
	if m.FolderAccess != "" {
		s.stopNarrowed(context.Background(), m.FolderAccess)
	}
}

// stopNarrowed stops the turns of principal (every principal for Everyone)
// that run in this process and hold a folder the store no longer gives them
// at the mode they hold it.
func (s *Service) stopNarrowed(ctx context.Context, principal string) {
	if !s.d.Files.Available() {
		return
	}
	type held struct {
		p, conv string
		r       *run
		view    workspace.View
	}
	var check []held
	s.mu.Lock()
	for k, r := range s.runs {
		p, conv, ok := strings.Cut(k, "\x00")
		if !ok || r == nil || r.chat == nil || (principal != sharedfolders.Everyone && p != principal) {
			continue
		}
		check = append(check, held{p, conv, r, r.chat.TurnView()})
	}
	s.mu.Unlock()
	for _, h := range check {
		if !s.narrowed(ctx, h.view) {
			continue
		}
		s.stopAndWait(ctx, h.p, h.conv, h.r)
	}
}

// stopAndWait stops the turn r of a chat and waits until its end is recorded,
// so the owner's call answers once the turn no longer runs.
func (s *Service) stopAndWait(ctx context.Context, p, conv string, r *run) {
	k := key(p, conv)
	s.mu.Lock()
	if s.runs[k] != r {
		s.mu.Unlock()
		return // it ended meanwhile
	}
	s.mu.Unlock()
	// Registered while the run is there: finishTurn removes the run before it
	// notifies, so the end of this turn reaches the channel.
	ended := s.waitFor(k)
	s.mu.Lock()
	still := s.runs[k] == r
	s.mu.Unlock()
	if !still {
		s.forgetWaiter(k, ended)
		return
	}
	s.log.Info("studio: stopping a turn that lost a shared folder", "chat", conv)
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), folderStopWait)
	defer cancel()
	if err := s.stopLocal(sctx, p, conv); err != nil {
		s.log.Warn("studio: could not stop a turn that lost a shared folder", "chat", conv, "error", err)
	}
	select {
	case <-ended:
	case <-sctx.Done():
		s.forgetWaiter(k, ended)
		s.log.Warn("studio: a turn that lost a shared folder did not end in time", "chat", conv)
	}
}

// narrowed says whether the store refuses a mount of v now, or gives it a
// lower mode than v holds. A mount is found by its node, so a rename of the
// folder does not count. An error of the store counts as narrowed: a turn is
// not kept on a folder that nothing says it may still use.
func (s *Service) narrowed(ctx context.Context, v workspace.View) bool {
	st := s.d.Files.Store()
	for _, m := range v.Mounts {
		if m.NodeID <= 0 {
			continue
		}
		n, err := st.NodeByID(ctx, v.Actor, m.NodeID)
		if err != nil {
			return true
		}
		now, err := st.View(ctx, v.Actor, []workspace.MountRequest{{Path: n.Path, At: "x", Mode: m.Mode}})
		if err != nil || len(now.Mounts) != 1 || now.Mounts[0].Mode < m.Mode {
			if err != nil && !errors.Is(err, workspace.ErrNotFound) && !errors.Is(err, workspace.ErrInvalid) {
				s.log.Warn("studio: could not check a folder of a running turn", "error", err)
			}
			return true
		}
	}
	return false
}
