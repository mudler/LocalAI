// The core calls of a connector page, with the signed-in person's own session
// (sdk.core.fetch). The bindings, the sealed token and the sender log live in
// core; the add-on only carries the messages.
import { getSdk } from '../../host-compat/src/runtime.js'
import { localizeAccess, localizeCapabilities } from './connector.js'

const enc = encodeURIComponent

// The language of the page. The three calls that return text for the owner
// (kinds, access, preview) name it, so the host answers in it: the language
// of the interface may differ from the language of the browser.
let language = ''
// The interface language, English included: a save sends it to the host, which
// keeps it with the binding for the sentences the bot writes itself.
let uiLanguage = ''
// English is the host's own text, so it is not asked for.
export function setApiLanguage(tag) {
  uiLanguage = typeof tag === 'string' ? tag : ''
  language = uiLanguage && !/^en(-|$)/i.test(uiLanguage) ? uiLanguage : ''
}
const withUILanguage = (body) => (uiLanguage && body && typeof body === 'object' ? { ...body, ui_language: uiLanguage } : body)
const localized = (path) => (language ? `${path}${path.includes('?') ? '&' : '?'}lang=${enc(language)}` : path)

async function call(path, init = {}) {
  const response = await getSdk().core.fetch(path, { headers: { 'Content-Type': 'application/json' }, ...init })
  if (!response.ok) {
    let message = `HTTP ${response.status}`
    let body = null
    try {
      body = await response.json()
      message = body?.error?.message || message
    } catch (_e) { /* the body was not JSON */ }
    const err = new Error(message)
    err.status = response.status
    err.body = body
    throw err
  }
  if (response.status === 204) return null
  return response.json()
}

const send = (method, path, body, signal) => call(path, { method, body: body === undefined ? undefined : JSON.stringify(body), signal })

export const agentsApi = { list: () => call('/api/chats/agents') }

// The triggers of the signed-in person, to say which ones a save of a connection changes.
export const triggersApi = { list: () => call('/api/chats/triggers') }

export const connectorsApi = {
  list: () => call('/api/chats/connectors'),
  kinds: () => call(localized('/api/chats/connectors/kinds')).then((r) => (r ? { ...r, capabilities: localizeCapabilities(r.capabilities) } : r)),
  create: (body) => send('POST', '/api/chats/connectors', withUILanguage(body)),
  update: (id, body) => send('PUT', `/api/chats/connectors/${enc(id)}`, withUILanguage(body)),
  remove: (id) => send('DELETE', `/api/chats/connectors/${enc(id)}`),
  enable: (id) => send('POST', `/api/chats/connectors/${enc(id)}/enable`, {}),
  disable: (id) => send('POST', `/api/chats/connectors/${enc(id)}/disable`, {}),
  test: (id) => send('POST', `/api/chats/connectors/${enc(id)}/test`, {}),
  senders: (id) => call(`/api/chats/connectors/${enc(id)}/senders`),
  allow: (id, sender) => send('POST', `/api/chats/connectors/${enc(id)}/senders/${enc(sender)}/allow`, {}),
  // Pairing proves that the owner controls a platform account: the code is sent
  // to the bot as a direct message from that account.
  pair: (id) => send('POST', `/api/chats/connectors/${enc(id)}/pairing`, {}),
  // What each kind of sender may do on a binding now, and the latest decisions.
  access: (id) => call(localized(`/api/chats/connectors/${enc(id)}/access`)).then(localizeAccess),
  // What the form as it is now will do, before it is saved. The id is "new" for a connection that is not saved yet.
  preview: (id, body, signal) => send('POST', localized(`/api/chats/connectors/${enc(id)}/access/preview`), body, signal).then(localizeAccess),
  unpair: (id, sender) => send('DELETE', `/api/chats/connectors/${enc(id)}/paired/${enc(sender)}`),
}
