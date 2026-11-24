// The requests behind sdk.resources. The SDK names an operation and its
// parameters; this module is the only place that turns them into a route, a
// method, a body and the own-session confirmation header. Add-on code never
// supplies an address or a header, so it cannot reach another route or
// another add-on, and a call is shaped the same way for every add-on.
//
// No React and no network: the node unit tests drive it directly.

const ADDON = /^[a-z][a-z0-9-]{0,63}$/
const OPERATION = /^[a-z][a-z0-9-]{0,63}$/
const SEGMENT = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/
const USER = /^[A-Za-z0-9][A-Za-z0-9._:@-]{0,255}$/
const CONVERSATION = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/
const TOOL = /^[a-z][a-z0-9-]{0,63}$/
const KINDS = new Set(['rename', 'delete', 'apply'])
const ROLES = new Set(['viewer', 'editor'])

export function resourceOperationId() {
  const c = globalThis.crypto
  const raw = c && typeof c.randomUUID === 'function'
    ? c.randomUUID()
    : Math.random().toString(16).slice(2) + Date.now().toString(16)
  return `op-${raw}`
}

const need = (ok, what) => { if (!ok) throw new TypeError(what) }
const revisionOk = (r) => r && typeof r === 'object' && Number.isInteger(r.schema) && r.schema >= 1 && Number.isInteger(r.data) && r.data >= 0
const objectOk = (v) => v !== null && typeof v === 'object' && !Array.isArray(v)
const enc = encodeURIComponent

// resourceRequest validates one call and returns { path, method, headers,
// body }. The confirmation header names the resource (the operation for a
// creation), which is what the host's own-session rule checks.
export function resourceRequest(call) {
  const { op, addonId, generation } = call || {}
  need(ADDON.test(String(addonId)), 'A resource call needs an add-on')
  const json = { Accept: 'application/json' }
  const confirm = (id) => ({ ...json, 'X-LocalAI-Confirm': id })
  const resource = () => { need(SEGMENT.test(String(call.resourceId)), 'A resource identifier is required'); return call.resourceId }
  const generationOk = () => { need(Number.isInteger(generation) && generation >= 1, 'A generation is required'); return generation }
  const operation = () => { need(OPERATION.test(String(call.operationId)), 'An operation identifier is required'); return call.operationId }
  const conversation = () => { need(CONVERSATION.test(String(call.conversationId)), 'A conversation identifier is required'); return call.conversationId }
  const base = '/api/addons/resources'

  switch (op) {
    case 'list':
      return { method: 'GET', path: `${base}?addonId=${enc(addonId)}`, headers: json }
    case 'create': {
      need(objectOk(call.arguments), 'Creation arguments are an object')
      const id = operation()
      return { method: 'POST', path: base, headers: confirm(id), body: { addonId, operationId: id, generation: generationOk(), arguments: call.arguments } }
    }
    case 'rename': {
      const r = resource()
      need(typeof call.name === 'string' && call.name !== '', 'A name is required')
      need(revisionOk(call.revision), 'The revision that was read is required')
      return { method: 'POST', path: `${base}/${enc(r)}/rename`, headers: confirm(r), body: { operationId: operation(), generation: generationOk(), revision: call.revision, name: call.name } }
    }
    case 'delete': {
      const r = resource()
      need(typeof call.confirm === 'string' && call.confirm !== '', 'The typed confirmation is required')
      need(revisionOk(call.revision), 'The revision that was read is required')
      return { method: 'POST', path: `${base}/${enc(r)}/delete`, headers: confirm(r), body: { operationId: operation(), generation: generationOk(), revision: call.revision, confirm: call.confirm } }
    }
    case 'apply': {
      const r = resource()
      need(TOOL.test(String(call.tool)), 'A tool is required')
      need(objectOk(call.arguments), 'Arguments are an object')
      need(revisionOk(call.revision), 'The revision that was read is required')
      return { method: 'POST', path: `${base}/${enc(r)}/apply`, headers: confirm(r), body: { operationId: operation(), generation: generationOk(), revision: call.revision, tool: call.tool, arguments: call.arguments } }
    }
    case 'grants': {
      const r = resource()
      return { method: 'GET', path: `${base}/${enc(r)}/grants`, headers: confirm(r) }
    }
    case 'grant-set': {
      const r = resource()
      need(USER.test(String(call.userId)), 'A user identifier is required')
      need(ROLES.has(call.role), 'A role is viewer or editor')
      return { method: 'PUT', path: `${base}/${enc(r)}/grants/${enc(call.userId)}`, headers: confirm(r), body: { role: call.role, operationId: operation() } }
    }
    case 'grant-revoke': {
      const r = resource()
      need(USER.test(String(call.userId)), 'A user identifier is required')
      return { method: 'DELETE', path: `${base}/${enc(r)}/grants/${enc(call.userId)}?operationId=${enc(operation())}`, headers: confirm(r) }
    }
    case 'users': {
      const r = resource()
      need(typeof call.query === 'string' && call.query.trim().length >= 2 && call.query.length <= 64, 'A query of 2 to 64 characters is required')
      const limit = Number.isInteger(call.limit) && call.limit > 0 ? `&limit=${Math.min(call.limit, 20)}` : ''
      return { method: 'GET', path: `${base}/${enc(r)}/users?q=${enc(call.query.trim())}${limit}`, headers: confirm(r) }
    }
    case 'status': {
      const r = resource()
      need(KINDS.has(call.kind), 'An operation kind is required')
      return { method: 'GET', path: `${base}/${enc(r)}/operations/${enc(operation())}?kind=${call.kind}`, headers: confirm(r) }
    }
    case 'selection-get':
      return { method: 'GET', path: `/api/addons/conversations/${enc(conversation())}/selection/${enc(addonId)}`, headers: json }
    case 'selection-put': {
      const r = resource()
      return { method: 'PUT', path: `/api/addons/conversations/${enc(conversation())}/selection/${enc(addonId)}`, headers: json, body: { resourceId: r } }
    }
    case 'selection-clear':
      return { method: 'DELETE', path: `/api/addons/conversations/${enc(conversation())}/selection/${enc(addonId)}`, headers: json }
    default:
      throw new TypeError('Unknown resource operation')
  }
}

// dispatchArgs is what the SDK's typed read hands the client: the add-on, its
// generation, the action, the tool arguments and, for a read of one resource,
// the resource the person named. Nothing else of the SDK's body goes on.
export function dispatchArgs(body) {
  const out = {
    operationId: body.operationId,
    action: body.action,
    addonId: body.addonId,
    generation: body.generation,
    args: body.arguments,
  }
  if (typeof body.resourceId === 'string' && body.resourceId !== '') out.resourceId = body.resourceId
  return out
}

// chatHeaders are the extra headers of a chat completion request. The only one
// is the conversation the request belongs to, which the owner uses to find the
// database the person selected for that conversation. It is sent only for a
// chat that opted into add-on tools and only when the identifier has the
// shape the server accepts; the model never sees it or sets it.
export function chatHeaders(chat) {
  if (!chat || chat.addonTools !== true) return {}
  const id = chat.id
  return typeof id === 'string' && CONVERSATION.test(id) ? { 'X-LocalAI-Conversation-Id': id } : {}
}
