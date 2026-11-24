// Small helpers about the server API. No Electron import.
import http from 'node:http'

/** The WebUI route that opens the chat, with an optional model. */
export function chatUrl(base: string, model?: string): string {
  const root = base.replace(/\/+$/, '')
  return model ? `${root}/app?model=${encodeURIComponent(model)}` : `${root}/app`
}

export function listModels(base: string, timeoutMs = 3000): Promise<string[]> {
  return new Promise((resolve) => {
    const req = http.get(`${base.replace(/\/+$/, '')}/v1/models`, { timeout: timeoutMs }, (res) => {
      let body = ''
      res.setEncoding('utf8').on('data', (d) => (body += d))
      res.on('end', () => {
        try {
          const j = JSON.parse(body) as { data?: { id?: string }[] }
          resolve((j.data ?? []).map((m) => m.id).filter((x): x is string => typeof x === 'string'))
        } catch {
          resolve([])
        }
      })
    })
    req.on('timeout', () => {
      req.destroy()
      resolve([])
    })
    req.on('error', () => resolve([]))
  })
}
