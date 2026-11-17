package cli

import (
	"fmt"
	"os"

	"github.com/mudler/LocalAI/core/cli/removedoptions"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/xlog"
)

// checkRemovedOptions stops the server when an option of the removed P2P layer
// or federation is still set in the environment or in the runtime settings.
// main already checks the command line before parsing; this check also covers
// what only exists after parsing, the settings file.
func (r *RunCMD) checkRemovedOptions() error {
	found := removedoptions.Check(removedoptions.Input{
		Args:     os.Args[1:],
		Env:      os.LookupEnv,
		Settings: removedoptions.ReadSettings(r.LocalaiConfigDir),
	})
	if len(found) == 0 {
		return nil
	}
	fmt.Fprintln(os.Stderr, removedoptions.Message(found))
	return ExitCodeError{Code: 1}
}

// authEnabled says whether the server runs with user authentication.
func (r *RunCMD) authEnabled() bool {
	return r.AuthEnabled || r.GitHubClientID != "" || r.OIDCClientID != ""
}

// resolveP2PMode refuses mode "share" while authentication is off. Starting the
// P2P layer for the other modes is done where the application starts.
func (r *RunCMD) resolveP2PMode() error {
	mode, msg, err := config.ResolveP2PMode(r.P2PMode, r.authEnabled())
	if err != nil {
		return err
	}
	if mode != config.P2PModeOff {
		xlog.Info(msg)
	}
	return nil
}
