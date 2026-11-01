import { expect } from '@playwright/test'
import { mockChat } from './chat-fixtures.js'

// A chat with skills, saved agents and the command routes stubbed, for the
// slash commands spec (chats-slash-commands.spec.js). The event stream is
// faked: window.__emit(kind, payload) plays a server event.

const AT = '2026-10-25T10:00:00Z'
export const HISTORY = [
  { seq: 1, turn: 1, role: 'user', text: 'Compare the quotes.', at: AT },
  { seq: 2, turn: 1, role: 'assistant', text: 'The second one is cheapest.', at: AT },
]
export const SKILLS = [
  { id: 'personal:review', name: 'review', description: 'Check a change before it ships', layer: 'personal', path: '/s/review', enabled: true },
  { id: 'builtin:pdf', name: 'pdf', description: 'Read and write PDF documents', layer: 'builtin', path: '/s/pdf', enabled: true },
  { id: 'addon:office:slides', name: 'slides', description: 'Make slide decks', layer: 'addon', addon: 'office', addon_title: 'Office', path: '/s/slides', enabled: true },
  { id: 'shared:new', name: 'new', description: 'A shared skill named like a command', layer: 'shared', path: '/s/new', enabled: true },
]
export const AGENTS = [
  { id: 'rev1', name: 'Reviewer', description: 'Checks drafts for tone' },
  { id: 'rev2', name: 'Researcher', description: 'Finds sources on the web' },
]

export const emit = (page, kind, payload) => page.evaluate(([k, p]) => window.__emit(k, p), [kind, payload])
export const box = (page) => page.getByTestId('chat-input')
export const menu = (page) => page.locator('#home-slash')

export async function openSlashChat(page, { agentsOn = true, messages = HISTORY } = {}) {
  await page.addInitScript(() => {
    window.__sources = []
    window.EventSource = class {
      constructor(url) { this.url = url; this.l = {}; window.__sources.push(this); setTimeout(() => this.onopen && this.onopen(), 0) }
      addEventListener(kind, fn) { (this.l[kind] = this.l[kind] || []).push(fn) }
      close() { this.closed = true }
    }
    window.__emit = (kind, payload) => {
      const src = window.__sources.filter((x) => x.url.includes('/api/chats/c1/events') && !x.closed).pop()
      for (const fn of (src && src.l[kind]) || []) fn({ data: JSON.stringify(payload) })
    }
  })
  await mockChat(page)
  const api = { messages: [...messages], sent: [], builtins: [], compactRelease: null, agentsOn }
  const detail = { id: 'c1', title: 'Quotes', model: 'qwen3-8b', kind: 'chat', created: AT, updated: AT, running: false, last_turn: 'done', pending: [] }
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, async (route) => {
    if (route.request().method() === 'POST') {
      const body = route.request().postDataJSON()
      api.sent.push(body)
      const seq = api.messages.length + 1
      api.messages.push({ seq, turn: 2, role: 'user', text: body.text, at: AT,
        ...(body.skill ? { invoked: { kind: 'skill', name: body.skill } } : {}),
        ...(body.agent ? { invoked: { kind: 'agent', name: AGENTS.find(a => a.id === body.agent)?.name || body.agent, id: body.agent } } : {}) })
      return route.fulfill({ json: { started: false, pending_id: '' } })
    }
    return route.fulfill({ json: { messages: api.messages } })
  })
  await page.route(/\/api\/chats\/skills(\?.*)?$/, route => route.fulfill({ json: { skills: SKILLS } }))
  await page.route('**/api/chats/c1/tools', route => route.fulfill({ json: {
    builtins: [{ group: 'saved_agents', opt_in: true, off: !api.agentsOn, usable: true, tools: ['list_agents'] }], servers: [], connectors: [],
  } }))
  await page.route('**/api/chats/c1/builtins', route => {
    const body = route.request().postDataJSON()
    api.builtins.push(body)
    api.agentsOn = (body.on || []).includes('saved_agents')
    return route.fulfill({ json: { builtins: [{ group: 'saved_agents', opt_in: true, off: !api.agentsOn, usable: true, tools: ['list_agents'] }] } })
  })
  await page.route('**/api/chats/c1/addons', route => route.fulfill({ json: { addons: [], commands: 'none' } }))
  await page.route('**/api/chats/agents', route => route.fulfill({ json: { agents: AGENTS } }))
  await page.route('**/api/chats/c1/compact', async route => {
    await new Promise((resolve) => { api.compactRelease = resolve })
    api.messages.push({ seq: api.messages.length + 1, turn: 1, role: 'compacted', text: 'Compacted: 9000 tokens to 3000', before: 9000, after: 3000, at: AT })
    return route.fulfill({ json: { before: 9000, after: 3000 } })
  })
  await page.route('**/api/chats/c1/clear', route => {
    api.messages.push({ seq: api.messages.length + 1, turn: 1, role: 'cleared', text: '', at: AT })
    return route.fulfill({ json: { cleared: true } })
  })
  await page.route('**/api/chats/c1', route => route.fulfill({ json: detail }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [{ ...detail, pending: undefined }], persistent: true, files: true } }))
  await page.goto('/app/chats/c1')
  await expect(page.getByTestId('chat-foot')).toBeVisible()
  await expect(page.getByTestId('chat-message')).toHaveCount(messages.filter(m => m.role === 'user' || m.role === 'assistant').length)
  return api
}

