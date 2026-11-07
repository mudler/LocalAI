//go:build !linux

package main

import "os/exec"

// The experimental backend targets Linux; elsewhere the child ends when its
// stdin closes at parent exit.
func setChildAttrs(cmd *exec.Cmd) {}

func lockThreadForChild() {}

func processRSS(pid int) uint64 { return 0 }
