package chv

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
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
	tmp := filepath.Join(dir, "state.json.tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "state.json"))
}

func loadState(dir string) (state, error) {
	var s state
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}
