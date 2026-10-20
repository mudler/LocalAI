// SPDX-License-Identifier: MIT
package application

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/mudler/xlog"
)

func capture(t *testing.T) *bytes.Buffer {
	var buf bytes.Buffer
	xlog.SetLogger(xlog.NewLoggerWithHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}), xlog.LogLevelDebug))
	return &buf
}

func TestMemoryOnlyPinsLogOneWarning(t *testing.T) {
	buf := capture(t)
	if _, err := pinStoreFor(nil, ""); err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(buf.Bytes(), []byte("memory only")); got != 1 {
		t.Fatalf("want one memory-only warning, got %d in %q", got, buf.String())
	}
}

func TestPinsFileDoesNotWarn(t *testing.T) {
	buf := capture(t)
	if _, err := pinStoreFor(nil, filepath.Join(t.TempDir(), "addon-pins.json")); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("unexpected log: %q", buf.String())
	}
}
