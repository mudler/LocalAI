package model

import (
	process "github.com/mudler/go-processmanager"
)

type GRPCProcessFilter = func(id string, p *process.Process) bool

func all(_ string, _ *process.Process) bool {
	return true
}

func allExcept(s string) GRPCProcessFilter {
	return func(id string, p *process.Process) bool {
		return id != s
	}
}
