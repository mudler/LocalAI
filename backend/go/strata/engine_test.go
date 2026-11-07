package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func collect(ids *[]int) func(genToken) error {
	return func(t genToken) error { *ids = append(*ids, t.ID); return nil }
}

var _ = Describe("engine", func() {
	It("ready timeout", func() {
		cfg := stubConfig("noready")
		cfg.ReadyTimeout = 300 * time.Millisecond
		start := time.Now()
		_, err := startEngine(cfg)
		if err == nil || !errors.Is(err, errNotReady) {
			Fail(fmt.Sprintf("want errNotReady, got %v", err))
		}
		if time.Since(start) > 3*time.Second {
			Fail(fmt.Sprintf("timeout took too long: %s", time.Since(start)))
		}
	})

	It("exit before ready", func() {
		_, err := startEngine(stubConfig("exitearly"))
		if err == nil || !errors.Is(err, errEngineDied) {
			Fail(fmt.Sprintf("want errEngineDied, got %v", err))
		}
		if !strings.Contains(err.Error(), "cannot load model") {
			Fail(fmt.Sprintf("error should carry the engine's stderr, got %v", err))
		}
	})

	It("missing executable", func() {
		cfg := stubConfig("ok")
		cfg.Path = filepath.Join(GinkgoT().TempDir(), "nope")
		if _, err := startEngine(cfg); err == nil {
			Fail("want an error for a missing executable")
		}
	})

	It("ready parsed", func() {
		e, err := startEngine(stubConfig("ok"))
		Expect(err).ToNot(HaveOccurred())
		defer e.close()
		if e.ctxLen != 4096 || !e.canStop {
			Fail(fmt.Sprintf("ctx=%d canStop=%v", e.ctxLen, e.canStop))
		}
		e2, err := startEngine(stubConfig("ok", "STRATA_STUB_NOSTOP=1"))
		Expect(err).ToNot(HaveOccurred())
		defer e2.close()
		if e2.canStop {
			Fail("READY without stop must clear canStop")
		}
	})

	It("stream and request line", func() {
		log := filepath.Join(GinkgoT().TempDir(), "gen.log")
		e, err := startEngine(stubConfig("ok", "STRATA_STUB_TOKENS=72,105,33", "STRATA_STUB_LOG="+log))
		Expect(err).ToNot(HaveOccurred())
		defer e.close()
		var got []int
		dr, err := e.generate(context.Background(),
			genRequest{MaxNew: 10, Keys: []string{"temperature=0.5", "top_k=40"}, IDs: []int{1, 2, 3}}, collect(&got))
		Expect(err).ToNot(HaveOccurred())
		if len(got) != 3 || got[0] != 72 || got[2] != 33 || dr.Finish != "stop" {
			Fail(fmt.Sprintf("got %v finish %q", got, dr.Finish))
		}
		b, _ := os.ReadFile(log)
		if strings.TrimSpace(string(b)) != "GEN 10 temperature=0.5 top_k=40 1,2,3" {
			Fail(fmt.Sprintf("request line was %q", b))
		}
		// The same engine serves a second request: the stream stayed in step.
		got = nil
		if _, err := e.generate(context.Background(), genRequest{MaxNew: 2, IDs: []int{1}}, collect(&got)); err != nil || len(got) != 2 {
			Fail(fmt.Sprintf("second request: %v %v", got, err))
		}
	})

	It("cancel sends stop", func() {
		e, err := startEngine(stubConfig("ok", "STRATA_STUB_DELAY_MS=20"))
		Expect(err).ToNot(HaveOccurred())
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
			Fail(fmt.Sprintf("want context.Canceled, got %v", err))
		}
		if dr.Finish != "cancel" {
			Fail(fmt.Sprintf("engine finish reason %q, want cancel", dr.Finish))
		}
		if n > 10 || time.Since(start) > 3*time.Second {
			Fail(fmt.Sprintf("STOP was not honoured promptly: %d tokens in %s", n, time.Since(start)))
		}
		// Stream stays in step after the cancel.
		var got []int
		if _, err := e.generate(context.Background(), genRequest{MaxNew: 2, IDs: []int{1}}, collect(&got)); err != nil || len(got) != 2 {
			Fail(fmt.Sprintf("request after cancel: %v %v", got, err))
		}
	})

	It("callback error stops engine", func() {
		e, err := startEngine(stubConfig("ok", "STRATA_STUB_DELAY_MS=10"))
		Expect(err).ToNot(HaveOccurred())
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
			Fail(fmt.Sprintf("want the callback error, got %v", err))
		}
	})

	It("stop ignored by engine is killed", func() {
		cfg := stubConfig("hangstop", "STRATA_STUB_DELAY_MS=10")
		cfg.StopTimeout = 300 * time.Millisecond
		e, err := startEngine(cfg)
		Expect(err).ToNot(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		n := 0
		_, err = e.generate(ctx, genRequest{MaxNew: 1 << 30, IDs: []int{1}}, func(genToken) error {
			if n++; n == 2 {
				cancel()
			}
			return nil
		})
		if err == nil || !strings.Contains(err.Error(), "killed") {
			Fail(fmt.Sprintf("want a killed error, got %v", err))
		}
		if e.alive() {
			Fail("engine should be dead")
		}
		if _, err := e.generate(context.Background(), genRequest{MaxNew: 1, IDs: []int{1}}, func(genToken) error { return nil }); !errors.Is(err, errEngineDied) {
			Fail(fmt.Sprintf("want errEngineDied after the kill, got %v", err))
		}
	})

	It("crash mid stream", func() {
		e, err := startEngine(stubConfig("crash"))
		Expect(err).ToNot(HaveOccurred())
		defer e.close()
		var got []int
		_, err = e.generate(context.Background(), genRequest{MaxNew: 10, IDs: []int{1}}, collect(&got))
		if !errors.Is(err, errEngineDied) {
			Fail(fmt.Sprintf("want errEngineDied, got %v", err))
		}
		if len(got) != 2 {
			Fail(fmt.Sprintf("tokens before the crash: %v", got))
		}
		if e.alive() {
			Fail("alive() should be false after the crash")
		}
		if _, err := e.generate(context.Background(), genRequest{MaxNew: 1, IDs: []int{1}}, collect(&got)); !errors.Is(err, errEngineDied) {
			Fail(fmt.Sprintf("later request: want errEngineDied, got %v", err))
		}
	})

	It("e r r line", func() {
		e, err := startEngine(stubConfig("err"))
		Expect(err).ToNot(HaveOccurred())
		defer e.close()
		_, err = e.generate(context.Background(), genRequest{MaxNew: 5, IDs: []int{1}}, func(genToken) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "outside the vocabulary") {
			Fail(fmt.Sprintf("want the engine's message, got %v", err))
		}
		// ERR ends the request only; the engine keeps serving.
		if !e.alive() {
			Fail("engine should still be alive after ERR")
		}
	})

	It("close ends engine", func() {
		e, err := startEngine(stubConfig("ok"))
		Expect(err).ToNot(HaveOccurred())
		e.close()
		if e.alive() {
			Fail("close must end the engine")
		}
	})

})
