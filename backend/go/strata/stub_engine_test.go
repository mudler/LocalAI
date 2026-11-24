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
//	native      like ok, but refuses to start the way the engine does for a
//	            native (IQ) pack unless --native, --spec T (T >= 2) and --prefill
//	            are on the command line
//
// GENI is answered like GEN when the embeddings file exists and has a header;
// STRATA_STUB_NOVISION makes it an ERR. LP lines follow each T only when the
// request has logprobs=K, with K alternatives.
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

	. "github.com/onsi/gomega"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("STRATA_STUB_MODE"); mode != "" {
		runStub(mode)
		return
	}
	if os.Getenv("STRATA_STUB_VISION") == "1" && len(os.Args) > 1 && os.Args[1] == "--mmproj" {
		runVisionStub()
		return
	}
	os.Exit(m.Run())
}

func runStub(mode string) {
	out := bufio.NewWriter(os.Stdout)
	say := func(f string, a ...any) {
		_, _ = fmt.Fprintf(out, f+"\n", a...)
		_ = out.Flush()
	}
	switch mode {
	case "native":
		if why := nativeStubCheck(os.Args[1:]); why != "" {
			fmt.Fprintln(os.Stderr, why)
			os.Exit(2)
		}
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
			_, _ = fmt.Fprintln(f, l)
			_ = f.Close()
		}
		if !strings.HasPrefix(l, "GEN ") && !strings.HasPrefix(l, "GENI ") {
			say("ERR expected: GEN <max_new> <id,id,...> or GENI <max_new> <file> <id,id,...>")
			continue
		}
		fields := strings.Fields(l)
		lpK := -1
		geni := fields[0] == "GENI"
		for _, f := range fields[2:] {
			if v, ok := strings.CutPrefix(f, "logprobs="); ok {
				lpK, _ = strconv.Atoi(v)
			}
		}
		if geni {
			// like the real engine: the first word without '=' after the keys is the embeddings file
			path := ""
			for _, f := range fields[2:] {
				if !strings.Contains(f, "=") {
					path = f
					break
				}
			}
			if os.Getenv("STRATA_STUB_NOVISION") != "" {
				say("ERR this engine was started without --vision")
				continue
			}
			if st, err := os.Stat(path); err != nil || st.Size() < 20 {
				say("ERR cannot open %s", path)
				continue
			}
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
			if lpK >= 0 {
				// the chosen token, then the K best: the chosen one first, then id+1, id+2, ...
				alt := fmt.Sprintf("LP -0.5")
				base, _ := strconv.Atoi(id)
				for a := 0; a < lpK; a++ {
					if a == 0 {
						alt += fmt.Sprintf(" %d:-0.5", base)
					} else {
						alt += fmt.Sprintf(" %d:-%d.25", base+a, a+1)
					}
				}
				say("%s", alt)
			}
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

// nativeStubCheck is the check the engine makes for a native (IQ) pack. It
// returns the engine's message, or "" when the command line is complete.
func nativeStubCheck(args []string) string {
	var native, prefill bool
	spec := 0
	for i, a := range args {
		switch a {
		case "--native":
			native = i+1 < len(args) && !strings.HasPrefix(args[i+1], "--")
		case "--prefill":
			prefill = i+1 < len(args)
		case "--spec":
			if i+1 < len(args) {
				spec, _ = strconv.Atoi(args[i+1])
			}
		}
	}
	if !native || spec < 2 || !prefill {
		return "strata generate: pack is a native (IQ) pack: it needs --native SHARD1, --spec T (T >= 2) and --prefill CHUNK"
	}
	return ""
}

// stubConfig returns an engineConfig that runs this test binary as the engine.
func stubConfig(mode string, env ...string) engineConfig {
	exe, err := os.Executable()
	Expect(err).ToNot(HaveOccurred())
	return engineConfig{
		Path:         exe,
		Env:          append([]string{"STRATA_STUB_MODE=" + mode}, env...),
		ReadyTimeout: 5 * time.Second,
		StopTimeout:  5 * time.Second,
	}
}
