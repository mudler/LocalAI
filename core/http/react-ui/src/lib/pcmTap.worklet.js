// AudioWorklet that forwards every render quantum of the first input channel
// to the main thread. It emits no audio. Loaded as a real same-origin file.
class PcmTap extends AudioWorkletProcessor {
  process(inputs) {
    const ch = inputs[0] && inputs[0][0]
    if (ch && ch.length) {
      const copy = new Float32Array(ch)
      this.port.postMessage(copy, [copy.buffer])
    }
    return true
  }
}
registerProcessor('pcm-tap', PcmTap)
