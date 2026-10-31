package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SystemPath is where the host programs of a backend are looked up. The
// supervisor starts an add-on with no PATH, so exec.LookPath would find
// nothing and a backend could never start.
var SystemPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// LookPath finds an executable file in SystemPath, then in extra. A name with
// a slash is used as it is, like exec.LookPath does. The PATH of the process
// is not read on purpose: it is not under the control of the administrator.
func LookPath(name string, extra ...string) (string, error) {
	if strings.Contains(name, "/") {
		if isExecutable(name) {
			return name, nil
		}
		return "", fmt.Errorf("%q is not an executable file", name)
	}
	dirs := append(filepath.SplitList(SystemPath), extra...)
	for _, dir := range dirs {
		if p := filepath.Join(dir, name); isExecutable(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("%q not found in %s", name, strings.Join(dirs, ":"))
}

func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}
