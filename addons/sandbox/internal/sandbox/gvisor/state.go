package gvisor

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"

	"github.com/mudler/LocalAI/addons/internal/atomicfile"
)

// state is state.json of a session folder.
type state struct {
	View      workspace.View     `json:"view"`
	Actor     workspace.Actor    `json:"actor"`
	Base      int64              `json:"base"`
	Own       []int64            `json:"own"`
	Index     sandbox.UpperIndex `json:"index"`
	Image     string             `json:"image"`
	CPUs      int                `json:"cpus"`
	Memory    int64              `json:"memory"`
	Disk      int64              `json:"disk"`
	Ports     []int              `json:"ports"`
	Suspended bool               `json:"suspended"`
	// Epoch counts the resumes of the folder. A handle owns the folder only
	// while its own epoch is the one in state.json.
	Epoch int64 `json:"epoch"`
}

func (s state) save(dir string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, "state.json"), b, 0o600)
}

func loadState(dir string) (state, error) {
	var s state
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}
