//go:build debug
// +build debug

package main

import (
	"github.com/mudler/xlog"
)

func assert(cond bool, msg string) {
	if !cond {
		xlog.Fatal().Stack().Msg(msg)
	}
}
