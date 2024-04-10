//go:build debug
// +build debug

package main

import (
	"github.com/rs/zerolog/log"
)

func assert(cond bool, msg string) {
	if !cond {
		log.Fatal().Stack().Msg(msg)
	}
}
