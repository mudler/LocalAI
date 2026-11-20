const transient = error => [429, 502, 503, 504].includes(Number(error.status || error.code)) || /owner unavailable|temporarily unavailable|failed to fetch/i.test(error.message)

function delay(ms, signal) {
  return new Promise((resolve, reject) => {
    const abort = () => { clearTimeout(timer); reject(signal.reason || new DOMException('Cancelled', 'AbortError')) }
    const timer = setTimeout(() => { signal?.removeEventListener('abort', abort); resolve() }, ms)
    if (signal?.aborted) abort()
    else signal?.addEventListener('abort', abort, { once: true })
  })
}

// File reads stage private artifacts on the host. Limit whole transfers, not
// just downloads, so a gallery of candidates cannot exhaust per-user slots.
export function createAssetLoader(files, pause = delay) {
  let tail = Promise.resolve()
  return function asset(resource, id, signal) {
    const request = tail.then(async () => {
      for (let attempt = 0; ; attempt++) {
        signal?.throwIfAborted()
        try {
          const out = await files.read({ resourceId: resource, arguments: { tool: 'pipeline-artifact', artifact: id } }, { signal })
          const file = out?.files?.[0]
          if (!file) throw new Error('Asset is unavailable')
          return await files.blob(file.id, { signal })
        } catch (error) {
          if (signal?.aborted || attempt >= 2 || !transient(error)) throw error
          await pause(400 * (attempt + 1), signal)
        }
      }
    })
    tail = request.catch(() => {})
    return request
  }
}
