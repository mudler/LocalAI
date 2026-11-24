// A fake MicCapture for the VAD tests. `setLevel` is the RMS the mic reports;
// endSegment returns a Blob only while a segment is open.
export function createFakeMic() {
  let level = 0
  let open = false
  const state = { begins: 0, ends: 0, closes: 0 }
  const mic = {
    beginSegment() {
      state.begins++
      open = true
    },
    endSegment() {
      state.ends++
      if (!open) return null
      open = false
      return new Blob([new Uint8Array(44 + 320)], { type: 'audio/wav' })
    },
    level() {
      return level
    },
    close() {
      state.closes++
    },
  }
  return { mic, state, setLevel: (v) => { level = v } }
}
