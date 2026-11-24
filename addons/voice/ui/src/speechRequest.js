// The one request of the speech page: POST /v1/audio/speech, the OpenAI-shaped
// route of the core API, answered with audio.
export function buildSpeechRequest({ model, text, voice = '', instructions = '' }) {
  const input = String(text || '').trim()
  if (!model || !input) return null
  const request = { model, input }
  if (String(voice).trim()) request.voice = String(voice).trim()
  if (String(instructions).trim()) request.instructions = String(instructions).trim()
  return request
}

export async function speak(core, request) {
  const response = await core.fetch('/v1/audio/speech', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(request),
  })
  if (!response.ok) {
    const body = await response.json().catch(() => ({}))
    throw new Error(body?.error?.message || `HTTP ${response.status}`)
  }
  return response.blob()
}
