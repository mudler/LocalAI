package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
)

type atomicBool struct{ v atomic.Bool }

func (b *atomicBool) Set(x bool) { b.v.Store(x) }
func (b *atomicBool) Get() bool  { return b.v.Load() }

// cleanExecutable checks that p is an absolute path to a regular file and
// returns it cleaned. The engine and the image encoder are started from
// paths taken from model options or the environment, so a relative or
// non-file path is refused before it reaches exec.
func cleanExecutable(p string) (string, error) {
	if p == "" || !filepath.IsAbs(p) {
		return "", fmt.Errorf("%q is not an absolute path", p)
	}
	p = filepath.Clean(p)
	st, err := os.Stat(p) // #nosec G703 -- the path is absolute and cleaned above; only its type is read
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("%q is not a regular file", p)
	}
	return p, nil
}

// resolveInterpreter turns the python option into a program to start: a bare
// name is looked up in PATH, anything else must pass cleanExecutable.
func resolveInterpreter(name string) (string, error) {
	if name == "" {
		name = "python3"
	}
	if !strings.ContainsRune(name, filepath.Separator) {
		return exec.LookPath(name)
	}
	return cleanExecutable(name)
}
