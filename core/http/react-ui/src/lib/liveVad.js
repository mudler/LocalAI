// Voice activity detection for hands-free capture. It watches the loudness of
// an open microphone, decides when an utterance started and ended, and hands
// each finished clip to the caller. An energy gate answers "has this person
// stopped talking" locally, with no model and no round trip.

/** How often the loudness is sampled. */
const pollMs = 50
/** Silence that ends an utterance; survives the pause between clauses. */
export const silenceHoldMs = 900
/** Speech needed before a segment counts as an utterance (rejects a cough). */
export const minSpeechMs = 200
/** Hard cap on one utterance, so a stuck-open mic cannot grow without bound. */
export const maxUtteranceMs = 60_000
/** Absolute RMS floor for speech; a silent room cannot adapt below it. */
const minThreshold = 0.015
/** Speech must also stand this far above the measured room tone. */
const noiseMargin = 3
/** Ceiling on the adaptive threshold; above it "room tone" is a person talking. */
const maxThreshold = 0.08
/** The noise floor may climb 2% per poll; it drops to a quieter frame at once. */
const noiseRelease = 1.02

/**
 * Starts segmenting speech from `mic` (see liveMic.js). Returns
 * { pause(), resume(), stop() }; all are safe to call more than once.
 * `pause()` discards the segment in flight and emits nothing; `stop()` also
 * releases the microphone.
 */
export function startVad({ mic, onUtterance, onState }) {
  let running = true
  let paused = false
  let capturing = false
  // Session-wide: the room is the same room after the first utterance.
  let noiseFloor = 0
  let speechMs = 0
  let silenceMs = 0
  let segmentMs = 0

  const beginSegment = () => {
    // Empty the level window: the first reading of a segment must not average
    // what the microphone heard before it (a pause, the assistant's voice).
    mic.level()
    speechMs = 0
    silenceMs = 0
    segmentMs = 0
    capturing = true
    mic.beginSegment()
  }

  // Closes the segment and emits the clip when it held speech and we still
  // want it. `emit` is false when pausing or stopping mid-sentence.
  const endSegment = (emit) => {
    if (!capturing) return
    capturing = false
    const spoke = speechMs >= minSpeechMs
    const clip = mic.endSegment()
    if (emit && spoke && clip) onUtterance(clip)
    if (spoke) onState('listening')
  }

  const tick = () => {
    if (!running || paused || !capturing) return
    const level = mic.level()
    segmentMs += pollMs

    const threshold = Math.min(maxThreshold, Math.max(minThreshold, noiseFloor * noiseMargin))
    const speaking = level > threshold
    const wasSpeech = speechMs >= minSpeechMs
    // Hold the floor while a person is talking, so a long sentence does not
    // lift it up to the speech level and get cut mid-word.
    if (!(speaking && wasSpeech)) {
      noiseFloor = noiseFloor === 0 ? level : Math.min(level, noiseFloor * noiseRelease)
    }

    if (speaking) {
      speechMs += pollMs
      silenceMs = 0
      if (!wasSpeech && speechMs >= minSpeechMs) onState('hearing')
    } else if (speechMs >= minSpeechMs) {
      silenceMs += pollMs
      if (silenceMs >= silenceHoldMs) {
        endSegment(true)
        beginSegment()
        return
      }
    }

    if (speechMs >= minSpeechMs && segmentMs >= maxUtteranceMs) {
      // A frozen floor cannot follow a room that got louder; learn it again.
      noiseFloor = 0
      endSegment(true)
      beginSegment()
      return
    }
    // Nothing said for a long time: recycle the segment so a room left
    // listening does not accumulate an hour of silence in memory.
    if (speechMs < minSpeechMs && segmentMs >= maxUtteranceMs) {
      endSegment(false)
      beginSegment()
    }
  }

  const timer = setInterval(tick, pollMs)
  beginSegment()

  return {
    pause() {
      if (!running || paused) return
      paused = true
      endSegment(false)
    },
    resume() {
      if (!running || !paused) return
      paused = false
      beginSegment()
    },
    stop() {
      if (!running) return
      running = false
      clearInterval(timer)
      endSegment(false)
      mic.close()
    },
  }
}
