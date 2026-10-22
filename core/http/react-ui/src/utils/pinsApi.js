// Client of the pin and built-in routes. The base and fetch are injected so a
// node test drives it without a browser.
async function readJson(response) {
  if (!response.ok) {
    const e = new Error(`request failed with ${response.status}`)
    e.status = response.status
    throw e
  }
  return response.json()
}

const onlyStrings = (v) => (Array.isArray(v) ? v.filter(x => typeof x === 'string') : [])

export function createPinsApi({ base, fetchImpl = (...a) => fetch(...a) }) {
  const init = (extra = {}) => ({ credentials: 'same-origin', ...extra })
  return {
    async listPins(signal) {
      return onlyStrings(await readJson(await fetchImpl(base('/api/addons/pins'), init({ signal }))))
    },
    async putPins(ids, signal) {
      const response = await fetchImpl(base('/api/addons/pins'), init({
        method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(ids), signal,
      }))
      return onlyStrings(await readJson(response))
    },
    async listBuiltin(signal) {
      const body = await readJson(await fetchImpl(base('/api/addons/builtin'), init({ signal })))
      return Array.isArray(body?.builtin) ? body.builtin : []
    },
  }
}
