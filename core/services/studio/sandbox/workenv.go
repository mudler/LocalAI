package sandbox

import (
	"path"
	"slices"
	"strings"
)

// WorkEnvKey is the name of the variable that holds the guest path of the
// chat's work folder.
const WorkEnvKey = "WORK"

// WorkStart returns the folder a shell starts in and the environment it gets,
// for the work folder workDir of a chat (a guest path, see
// ExecutionCall.WorkDir). A workDir that is empty, is not a folder below
// DefaultDir or has a control character gives the home folder and no
// variable: a chat with no work folder yet, or a value that nobody should
// trust, falls back to the plain default.
func WorkStart(workDir, home string) (dir string, env []string) {
	if workDir == "" || strings.ContainsFunc(workDir, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return home, nil
	}
	clean := path.Clean(workDir)
	if !strings.HasPrefix(clean, DefaultDir+"/") {
		return home, nil
	}
	return clean, []string{WorkEnvKey + "=" + clean}
}

// MapWorkEnv rewrites the WORK entry of env for a backend that runs commands
// on the host: there the guest path /workspace/... is a folder below root, so
// $WORK must name that folder for a command to use it. Other entries stay as
// they are. A backend with a real guest (a VM or a container) does not call
// it.
func MapWorkEnv(env []string, root string) []string {
	out := slices.Clone(env)
	for i, e := range env {
		v, ok := strings.CutPrefix(e, WorkEnvKey+"=")
		if !ok {
			continue
		}
		rel, ok := strings.CutPrefix(path.Clean(v), DefaultDir+"/")
		if !ok {
			continue
		}
		out[i] = WorkEnvKey + "=" + strings.TrimRight(root, "/") + "/" + rel
	}
	return out
}
