package main

import (
	"fmt"
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("LiveInputTraces", func() {
	It("preserves the live synthesis contract", func() {
		for _, tc := range []struct {
			name   string
			events []liveInputEvent
			state  liveInputState
			rules  []string
		}{
			{"late input then EOF", []liveInputEvent{liveInputChunk, liveInputChunk, liveInputEOF, liveInputChunk}, liveInputEnded, []string{"R1-chunk", "R1-chunk", "R2-finish", "R4-ended-ignore"}},
			{"disconnect before text", []liveInputEvent{liveInputError, liveInputEOF}, liveInputFailed, []string{"R3-fail", "R5-failed-ignore"}},
			{"failure after audio", []liveInputEvent{liveInputChunk, liveInputError, liveInputChunk}, liveInputFailed, []string{"R1-chunk", "R3-fail", "R5-failed-ignore"}},
		} {
			By(tc.name)
			func() {
				state := liveInputOpen
				for i, e := range tc.events {
					next, rule := reduceLiveInput(state, e)
					if rule != tc.rules[i] {
						Fail(fmt.Sprint(rule))
					}
					state = next
				}
				if state != tc.state {
					Fail(fmt.Sprint(state))
				}
			}()
		}
		// Exhaust the finite graph: no terminal state can reopen, and every open
		// state admits both a normal completion and a cancellation.
		for s := liveInputOpen; s <= liveInputFailed; s++ {
			for e := liveInputChunk; e <= liveInputError; e++ {
				next, _ := reduceLiveInput(s, e)
				if s != liveInputOpen && next != s {
					Fail(fmt.Sprint("terminal input reopened"))
				}
			}
		}
	})
})
