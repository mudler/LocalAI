//go:build linux

package chv

import (
	"strings"
	"testing"
)

func TestNewRefusesNegativeStageLimit(t *testing.T) {
	_, err := New(Options{StateDir: t.TempDir(), CacheDir: t.TempDir(), StageLimit: -1})
	if err == nil || !strings.Contains(err.Error(), "StageLimit") {
		t.Fatalf("a negative StageLimit: %v", err)
	}
}
