//go:build !windows

package model

import "path/filepath"

// resolveLauncher runs the gallery-contract run.sh stub directly: every
// non-Windows host ships a POSIX shell that executes it as-is, so neither the
// executable nor the argument vector needs rewriting. The executable is the
// stub's basename because os.StartProcess resolves it against ProcAttr.Dir.
func resolveLauncher(grpcProcess, _ string, args []string) (processName string, processArgs []string, err error) {
	return filepath.Base(grpcProcess), args, nil
}
