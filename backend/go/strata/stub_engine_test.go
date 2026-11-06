package main

// A stub engine for the tests. The test binary re-executes itself as the
// engine when STRATA_STUB_MODE is set, so no external program is needed.
//
// Modes:
//
//	ok          READY, then answer each GEN with the tokens in STRATA_STUB_TOKENS
//	noready     never prints READY
//	exitearly   exits before READY
//	crash       prints two tokens of a request, then exits
//	err         answers every GEN with an ERR line
//	hangstop    like ok, but ignores STOP and never ends a request
//
// STRATA_STUB_DELAY_MS sets the pause before each token, STRATA_STUB_LOG names
// a file that receives every GEN line, STRATA_STUB_NOSTOP omits "stop" from READY.

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("STRATA_STUB_MODE"); mode != "" {
		runStub(mode)
		return
	}
	os.Exit(m.Run())
}

func runStub(mode string) {
	out := bufio.NewWriter(os.Stdout)
	say := func(f string, a ...any) {
		fmt.Fprintf(out, f+"\n", a...)
		out.Flush()
	}
	switch mode {
	case "noready":
		time.Sleep(time.Hour)
	case "exitearly":
		fmt.Fprintln(os.Stderr, "stub: cannot load model")
		os.Exit(2)
	}
	ready := "READY 4096 stop"
	if os.Getenv("STRATA_STUB_NOSTOP") != "" {
		ready = "READY 4096"
	}
	say("INFO arch=stub")
	say("%s", ready)

	delay, _ := strconv.Atoi(os.Getenv("STRATA_STUB_DELAY_MS"))
	toks := strings.Split(os.Getenv("STRATA_STUB_TOKENS"), ",")

	var stop atomic.Bool
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			l := sc.Text()
			if l == "STOP" {
				if mode != "hangstop" {
					stop.Store(true)
				}
				continue
			}
			lines <- l
		}
		os.Exit(0) // stdin closed: the parent is gone
	}()
	for l := range lines {
		if l == "QUIT" {
			return
		}
		if p := os.Getenv("STRATA_STUB_LOG"); p != "" {
			f, _ := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			fmt.Fprintln(f, l)
			f.Close()
		}
		if !strings.HasPrefix(l, "GEN ") {
			say("ERR expected: GEN <max_new> <id,id,...>")
			continue
		}
		if mode == "err" {
			say("ERR a token id is outside the vocabulary")
			continue
		}
		stop.Store(false)
		max, _ := strconv.Atoi(strings.Fields(l)[1])
		n, finish := 0, "length"
		for n < max {
			if stop.Load() {
				finish = "cancel"
				break
			}
			if delay > 0 {
				time.Sleep(time.Duration(delay) * time.Millisecond)
			}
			if n >= len(toks) && os.Getenv("STRATA_STUB_TOKENS") != "" && mode != "hangstop" && delay == 0 {
				break
			}
			id := "65"
			if os.Getenv("STRATA_STUB_TOKENS") != "" {
				id = toks[n%len(toks)]
			}
			say("T %s", id)
			say("LP -0.5 %s:-0.5", id)
			n++
			if mode == "crash" && n == 2 {
				os.Exit(3)
			}
			if n == len(toks) && os.Getenv("STRATA_STUB_TOKENS") != "" && delay == 0 {
				finish = "stop"
				break
			}
		}
		say("DONE %d 5 1.0 1.0 %s 0 0 0", n, finish)
	}
}

// stubConfig returns an engineConfig that runs this test binary as the engine.
func stubConfig(t *testing.T, mode string, env ...string) engineConfig {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return engineConfig{
		Path:         exe,
		Env:          append([]string{"STRATA_STUB_MODE=" + mode}, env...),
		ReadyTimeout: 5 * time.Second,
		StopTimeout:  5 * time.Second,
	}
}
