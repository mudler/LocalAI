package guestagent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// mergeEnv returns base with the entries of extra added. An entry of extra
// replaces the entry of base with the same name.
func mergeEnv(base, extra []string) []string {
	out := make([]string, 0, len(base)+len(extra))
	at := map[string]int{}
	for _, list := range [][]string{base, extra} {
		for _, kv := range list {
			k, _, _ := strings.Cut(kv, "=")
			if i, ok := at[k]; ok {
				out[i] = kv
				continue
			}
			at[k] = len(out)
			out = append(out, kv)
		}
	}
	return out
}

func lookupEnv(env []string, key string) string {
	v := ""
	for _, kv := range env {
		if k, val, ok := strings.Cut(kv, "="); ok && k == key {
			v = val
		}
	}
	return v
}

// lookPath finds an executable in the PATH of the process to start, not of
// the agent.
func lookPath(name, path string) (string, error) {
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", errors.New("guestagent: " + name + ": command not found")
}
