package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func collect(ids *[]int) func(genToken) error {
	return func(t genToken) error { *ids = append(*ids, t.ID); return nil }
}

func TestReadyTimeout(t *testing.T) {
	cfg := stubConfig(t, "noready")
	cfg.ReadyTimeout = 300 * time.Millisecond
	start := time.Now()
	_, err := startEngine(cfg)
	if err == nil || !errors.Is(err, errNotReady) {
		t.Fatalf("want errNotReady, got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("timeout took too long: %s", time.Since(start))
	}
}

func TestExitBeforeReady(t *testing.T) {
	_, err := startEngine(stubConfig(t, "exitearly"))
	if err == nil || !errors.Is(err, errEngineDied) {
		t.Fatalf("want errEngineDied, got %v", err)
	}
	if !strings.Contains(err.Error(), "cannot load model") {
		t.Fatalf("error should carry the engine's stderr, got %v", err)
	}
}

func TestMissingExecutable(t *testing.T) {
	cfg := stubConfig(t, "ok")
	cfg.Path = filepath.Join(t.TempDir(), "nope")
	if _, err := startEngine(cfg); err == nil {
		t.Fatal("want an error for a missing executable")
	}
}

func TestReadyParsed(t *testing.T) {
	e, err := startEngine(stubConfig(t, "ok"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.close()
	if e.ctxLen != 4096 || !e.canStop {
		t.Fatalf("ctx=%d canStop=%v", e.ctxLen, e.canStop)
	}
	e2, err := startEngine(stubConfig(t, "ok", "STRATA_STUB_NOSTOP=1"))
	if err != nil {
		t.Fatal(err)
	}
	defer e2.close()
	if e2.canStop {
		t.Fatal("READY without stop must clear canStop")
	}
}

func TestStreamAndRequestLine(t *testing.T) {
	log := filepath.Join(t.TempDir(), "gen.log")
	e, err := startEngine(stubConfig(t, "ok", "STRATA_STUB_TOKENS=72,105,33", "STRATA_STUB_LOG="+log))
	if err != nil {
		t.Fatal(err)
	}
	defer e.close()
	var got []int
	dr, err := e.generate(context.Background(),
		genRequest{MaxNew: 10, Keys: []string{"temperature=0.5", "top_k=40"}, IDs: []int{1, 2, 3}}, collect(&got))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != 72 || got[2] != 33 || dr.Finish != "stop" {
		t.Fatalf("got %v finish %q", got, dr.Finish)
	}
	b, _ := os.ReadFile(log)
	if strings.TrimSpace(string(b)) != "GEN 10 temperature=0.5 top_k=40 1,2,3" {
		t.Fatalf("request line was %q", b)
	}
	// The same engine serves a second request: the stream stayed in step.
	got = nil
	if _, err := e.generate(context.Background(), genRequest{MaxNew: 2, IDs: []int{1}}, collect(&got)); err != nil || len(got) != 2 {
		t.Fatalf("second request: %v %v", got, err)
	}
}

func TestCancelSendsStop(t *testing.T) {
	e, err := startEngine(stubConfig(t, "ok", "STRATA_STUB_DELAY_MS=20"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.close()
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	start := time.Now()
	dr, err := e.generate(ctx, genRequest{MaxNew: 100000, IDs: []int{1}}, func(genToken) error {
		n++
		if n == 3 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if dr.Finish != "cancel" {
		t.Fatalf("engine finish reason %q, want cancel", dr.Finish)
	}
	if n > 10 || time.Since(start) > 3*time.Second {
		t.Fatalf("STOP was not honoured promptly: %d tokens in %s", n, time.Since(start))
	}
	// Stream stays in step after the cancel.
	var got []int
	if _, err := e.generate(context.Background(), genRequest{MaxNew: 2, IDs: []int{1}}, collect(&got)); err != nil || len(got) != 2 {
		t.Fatalf("request after cancel: %v %v", got, err)
	}
}

func TestCallbackErrorStopsEngine(t *testing.T) {
	e, err := startEngine(stubConfig(t, "ok", "STRATA_STUB_DELAY_MS=10"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.close()
	boom := errors.New("client went away")
	n := 0
	_, err = e.generate(context.Background(), genRequest{MaxNew: 100000, IDs: []int{1}}, func(genToken) error {
		n++
		if n == 2 {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("want the callback error, got %v", err)
	}
}

func TestStopIgnoredByEngineIsKilled(t *testing.T) {
	cfg := stubConfig(t, "hangstop", "STRATA_STUB_DELAY_MS=10")
	cfg.StopTimeout = 300 * time.Millisecond
	e, err := startEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	_, err = e.generate(ctx, genRequest{MaxNew: 1 << 30, IDs: []int{1}}, func(genToken) error {
		if n++; n == 2 {
			cancel()
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "killed") {
		t.Fatalf("want a killed error, got %v", err)
	}
	if e.alive() {
		t.Fatal("engine should be dead")
	}
	if _, err := e.generate(context.Background(), genRequest{MaxNew: 1, IDs: []int{1}}, func(genToken) error { return nil }); !errors.Is(err, errEngineDied) {
		t.Fatalf("want errEngineDied after the kill, got %v", err)
	}
}

func TestCrashMidStream(t *testing.T) {
	e, err := startEngine(stubConfig(t, "crash"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.close()
	var got []int
	_, err = e.generate(context.Background(), genRequest{MaxNew: 10, IDs: []int{1}}, collect(&got))
	if !errors.Is(err, errEngineDied) {
		t.Fatalf("want errEngineDied, got %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("tokens before the crash: %v", got)
	}
	if e.alive() {
		t.Fatal("alive() should be false after the crash")
	}
	if _, err := e.generate(context.Background(), genRequest{MaxNew: 1, IDs: []int{1}}, collect(&got)); !errors.Is(err, errEngineDied) {
		t.Fatalf("later request: want errEngineDied, got %v", err)
	}
}

func TestERRLine(t *testing.T) {
	e, err := startEngine(stubConfig(t, "err"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.close()
	_, err = e.generate(context.Background(), genRequest{MaxNew: 5, IDs: []int{1}}, func(genToken) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "outside the vocabulary") {
		t.Fatalf("want the engine's message, got %v", err)
	}
	// ERR ends the request only; the engine keeps serving.
	if !e.alive() {
		t.Fatal("engine should still be alive after ERR")
	}
}

func TestCloseEndsEngine(t *testing.T) {
	e, err := startEngine(stubConfig(t, "ok"))
	if err != nil {
		t.Fatal(err)
	}
	e.close()
	if e.alive() {
		t.Fatal("close must end the engine")
	}
}
