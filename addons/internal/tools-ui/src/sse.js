// Reads a server-sent-event stream from a fetch response. EventSource is not
// used: the SDK does not wrap it, and it cannot carry the options of
// sdk.core.fetch (the session and the base path). Each `data:` line holds one
// JSON event; comments and lines that are not JSON are skipped. The read stops
// when the stream ends or the signal aborts.
export async function readSse(response, onEvent, signal) {
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  const emit = (block) => {
    for (const line of block.split('\n')) {
      if (!line.startsWith('data:')) continue
      let event
      try { event = JSON.parse(line.slice(5).trim()) } catch { continue }
      onEvent(event)
    }
  }
  const stop = () => { reader.cancel().catch(() => {}) }
  if (signal) {
    if (signal.aborted) { stop(); return }
    signal.addEventListener('abort', stop, { once: true })
  }
  try {
    for (;;) {
      if (signal?.aborted) return
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      let at
      while ((at = buffer.indexOf('\n\n')) >= 0) {
        const block = buffer.slice(0, at)
        buffer = buffer.slice(at + 2)
        emit(block)
        if (signal?.aborted) return
      }
    }
    buffer += decoder.decode()
    if (buffer.trim()) emit(buffer)
  } finally {
    signal?.removeEventListener('abort', stop)
  }
}
