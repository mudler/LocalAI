package cli

import (
	"fmt"
	"os"

	"github.com/mudler/LocalAI/core/cli/removedoptions"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/xlog"
)

// checkRemovedOptions stops the server when an option of the removed P2P layer
// or federation is still set in the runtime settings. main already checks the
// command line and the environment before parsing, so this one reads only what
// exists after parsing, the settings file, and does not report the others twice.
func (r *RunCMD) checkRemovedOptions() error {
	found := removedoptions.Check(removedoptions.Input{
		Settings: removedoptions.ReadSettings(r.LocalaiConfigDir),
	})
	msg := removedoptions.Message(found)
	if msg == "" {
		return nil
	}
	fmt.Fprintln(os.Stderr, msg)
	return ExitCodeError{Code: 1}
}

// authEnabled says whether the server runs with user authentication.
func (r *RunCMD) authEnabled() bool {
	return r.AuthEnabled || r.GitHubClientID != "" || r.OIDCClientID != ""
}

// resolveP2PMode refuses mode "share" while authentication is off, and any
// mode but off when the peer identity cannot be kept (no data path and no
// --p2p-identity). It runs after normalizeDataPath. Starting the layer is done
// where the application starts.
func (r *RunCMD) resolveP2PMode() error {
	mode, msg, err := config.ResolveP2PMode(r.P2PMode, r.authEnabled())
	if err != nil {
		return err
	}
	if mode == config.P2PModeOff {
		return nil
	}
	if _, err := config.ResolveP2PIdentityPath(r.P2PIdentity, r.DataPath); err != nil {
		return err
	}
	if err := config.ValidateP2PPeerAddrs(r.P2PBootstrap); err != nil {
		return fmt.Errorf("--p2p-bootstrap: %w", err)
	}
	xlog.Info(msg)
	return nil
}
