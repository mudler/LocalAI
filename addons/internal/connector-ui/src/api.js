// The core calls of a connector page, with the signed-in person's own session
// (sdk.core.fetch). The bindings, the sealed token and the sender log live in
// core; the add-on only carries the messages.
import { getSdk } from '../../host-compat/src/runtime.js'

const enc = encodeURIComponent

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

const send = (method, path, body) => call(path, { method, body: body === undefined ? undefined : JSON.stringify(body) })

export const agentsApi = { list: () => call('/api/chats/agents') }

export const connectorsApi = {
  list: () => call('/api/chats/connectors'),
  create: (body) => send('POST', '/api/chats/connectors', body),
  update: (id, body) => send('PUT', `/api/chats/connectors/${enc(id)}`, body),
  remove: (id) => send('DELETE', `/api/chats/connectors/${enc(id)}`),
  enable: (id) => send('POST', `/api/chats/connectors/${enc(id)}/enable`, {}),
  disable: (id) => send('POST', `/api/chats/connectors/${enc(id)}/disable`, {}),
  test: (id) => send('POST', `/api/chats/connectors/${enc(id)}/test`, {}),
  senders: (id) => call(`/api/chats/connectors/${enc(id)}/senders`),
  allow: (id, sender) => send('POST', `/api/chats/connectors/${enc(id)}/senders/${enc(sender)}/allow`, {}),
}
