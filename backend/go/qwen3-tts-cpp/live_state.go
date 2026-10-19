package main

// Native may ask for text again after EOF while flushing its codec. Keep input
// terminal so that request cannot block waiting for a frame that will never come.
type liveInputState uint8

const (
	liveInputOpen liveInputState = iota
	liveInputEnded
	liveInputFailed
)

type liveInputEvent uint8

const (
	liveInputChunk liveInputEvent = iota
	liveInputEOF
	liveInputError
)

func reduceLiveInput(state liveInputState, event liveInputEvent) (liveInputState, string) {
	switch state {
	case liveInputOpen:
		switch event {
		case liveInputChunk:
			return liveInputOpen, "R1-chunk"
		case liveInputEOF:
			return liveInputEnded, "R2-finish"
		case liveInputError:
			return liveInputFailed, "R3-fail"
		}
	case liveInputEnded:
		return state, "R4-ended-ignore"
	case liveInputFailed:
		return state, "R5-failed-ignore"
	}
	panic("invalid live input transition")
}
