// The requests behind sdk.userSettings. An add-on can declare per-person
// settings in its manifest; the host keeps the answers and checks every write
// against the declaration. The SDK names an operation and a key; this module
// is the only place that turns them into a route, a method, a body and the
// own-session confirmation header. Add-on code never supplies an address or a
// header, and a call is always bound to the add-on that makes it, so a page
// cannot read or change the settings of another add-on.
//
// No React and no network: the node unit tests drive it directly.

const ADDON = /^[a-z][a-z0-9-]{0,63}$/
const KEY = /^[a-z][a-z0-9-]{0,39}$/
const VALUE = /^[a-z][a-z0-9-]{0,39}$/

const need = (ok, what) => { if (!ok) throw new TypeError(what) }
const matches = (re, v) => typeof v === 'string' && re.test(v)

// userSettingsRequest validates one call and returns { path, method, headers,
// body }. The confirmation header names the add-on, which only a same-origin
// fetch of this page can set; a tool or an API key cannot.
export function userSettingsRequest(call) {
  const { op, addonId } = call || {}
  need(matches(ADDON, addonId), 'A settings call needs an add-on')
  const path = `/api/addons/${encodeURIComponent(addonId)}/user-settings`
  const json = { Accept: 'application/json' }
  switch (op) {
    case 'get':
      return { method: 'GET', path, headers: json }
    case 'set':
      need(matches(KEY, call.key), 'A setting key is required')
      need(matches(VALUE, call.value), 'A setting value is required')
      return { method: 'PUT', path, headers: { ...json, 'X-LocalAI-Confirm': addonId }, body: { key: call.key, value: call.value } }
  }
  throw new TypeError('Unknown settings operation')
}
