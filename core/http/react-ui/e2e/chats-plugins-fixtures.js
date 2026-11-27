import { expect } from '@playwright/test'
import { mockChat } from './chat-fixtures.js'

// A chat with one plugin on, stubbed for chats-plugins.spec.js: a plugin
// skill, a plugin command, a plugin connector and the chat's plugin switch.

export const AT = '2026-11-26T10:00:00Z'
const DIGEST = 'sha256:' + 'cd'.repeat(32)
export const INSTALLS = [
  { id: 'in-1', plugin: 'review@localai', name: 'review', scope: 'instance', digest: DIGEST, sha: 'a'.repeat(40), runsCode: false, state: 'installed', enabled: true, needsReenable: false, createdAt: AT, updatedAt: AT, approvedAt: AT },
]
const MODEL = {
  name: 'review',
  skills: [{ name: 'check', description: 'Check a diff', userInvocable: true, argumentHint: '<file>' }],
  commands: [{ name: 'pr', description: 'Review a pull request', argumentHint: '<number>' }],
}
const PLAIN = { id: 'personal:notes', name: 'notes', description: 'Keep notes', layer: 'personal', path: '/s/notes', enabled: true }
const PLUGIN_SKILL = {
  id: 'plugin:review@localai:check', name: 'review:check', description: 'Check a diff', layer: 'plugin', path: '/plugins/x/check',
  plugin: 'review@localai', plugin_title: 'review', plugin_install: 'in-1', argument_hint: '<file>', enabled: true,
}

// incognito: the level of an incognito chat, or ''. The API records the
// incognito marker of each plugin and message request.
export async function openPluginChat(page, { incognito = '' } = {}) {
  await page.addInitScript(() => {
    window.EventSource = class { constructor() { setTimeout(() => this.onopen && this.onopen(), 0) } addEventListener() {} close() {} }
  })
  await mockChat(page)
  const api = { off: [], puts: [], sent: [], markers: [], messages: [{ seq: 1, turn: 1, role: 'user', text: 'hello', at: AT }] }
  const mark = (route, what) => api.markers.push([what, route.request().headers()['x-localai-incognito'] || ''])
  await page.route('**/api/plugins/installs', (route) => { mark(route, 'installs'); return route.fulfill({ json: { installs: INSTALLS } }) })
  await page.route('**/api/plugins/installs/in-1', (route) => { mark(route, 'install'); return route.fulfill({ json: { ...INSTALLS[0], model: MODEL, files: [], codeReasons: [], codeFiles: [], skipped: [] } }) })
  await page.route('**/api/chats/c1/plugins', (route) => {
    mark(route, 'switch')
    const body = route.request().postDataJSON()
    api.puts.push(body)
    api.off = body.off
    return route.fulfill({ json: { off: body.off } })
  })
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, (route) => {
    if (route.request().method() === 'POST') {
      mark(route, 'message')
      const body = route.request().postDataJSON()
      api.sent.push(body)
      api.messages.push({ seq: api.messages.length + 1, turn: 2, role: 'user', text: body.text, at: AT, invoked: { kind: body.skill === 'review:pr' ? 'command' : 'skill', name: body.skill } })
      return route.fulfill({ json: { started: false, pending_id: '' } })
    }
    return route.fulfill({ json: { messages: api.messages } })
  })
  await page.route(/\/api\/chats\/skills(\?.*)?$/, (route) => route.fulfill({ json: { skills: api.off.includes('in-1') ? [PLAIN] : [PLAIN, PLUGIN_SKILL] } }))
  await page.route('**/api/chats/c1/tools', (route) => route.fulfill({ json: {
    builtins: [], servers: [], off: [],
    connectors: [{ id: 'k1', name: 'review-search', off: false, usable: true, plugin_install: 'in-1' }],
  } }))
  await page.route('**/api/chats/c1/addons', (route) => route.fulfill({ json: { addons: [], commands: 'none' } }))
  await page.route('**/api/chats/agents', (route) => route.fulfill({ json: { agents: [] } }))
  const detail = { id: 'c1', title: 'Review', model: 'qwen3-8b', kind: 'chat', created: AT, updated: AT, running: false, last_turn: 'done', pending: [], plugins_off: [], ...(incognito ? { incognito } : {}) }
  await page.route('**/api/chats/c1', (route) => route.fulfill({ json: detail }))
  await page.route(/\/api\/chats(\?.*)?$/, (route) => route.fulfill({ json: { conversations: [{ ...detail, pending: undefined }], persistent: true, files: true } }))
  await page.goto('/app/chats/c1')
  await expect(page.getByTestId('chat-foot')).toBeVisible()
  return api
}

