package cli

import (
	"os"
	"strings"

	"github.com/mudler/xlog"
)

// deprecatedFlags maps old flag names to their new replacements.
var deprecatedFlags = map[string]string{
	"--p2ptoken": "--p2p-token",
}

// warnDeprecatedFlags checks os.Args for any deprecated flag names and logs
// a warning directing the user to the new name. Old flags continue to work
// via kong aliases, so this is purely informational.
func warnDeprecatedFlags() {
	for _, arg := range os.Args[1:] {
		// Strip any =value suffix to match flag names like --p2ptoken=xyz
		flag := arg
		if idx := strings.Index(flag, "="); idx != -1 {
			flag = flag[:idx]
		}
		if newName, ok := deprecatedFlags[flag]; ok {
			xlog.Warn("Deprecated flag used", "old", flag, "new", newName, "message", "please switch to the new flag name; the old name will be removed in a future release")
		}
	}
}
