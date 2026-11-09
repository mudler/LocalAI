import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installAgents, AGENTS, SKILLS, DRAFT_TOOLS } from './agents-fixtures.js'
import { installUses, USES_ADDONS, DATABASES } from './agents-uses-fixtures.js'
import { mockHome, seedChats, sampleChats } from './home-fixtures.js'
import { mockSettings } from './settings-fixtures.js'
import { mockLibrary } from './library-fixtures.js'
import { mockAddons, app, installedAddon, release, sessionFor, ADMIN } from './addons-fixtures.js'
import { mockLedger, GALLERY } from './ledger-fixtures.js'
import { mockOperate, installedBackend } from './operate-fixtures.js'
import { addonRecord, failedOp, galleryItem, heldGrant, libraryItem, minutesAgo, mockUpdates, okSource, runningOp, snapshotOf, updateAvailable } from './updates-fixtures.js'

// No page may scroll sideways. Every page below is drawn with content that
// does not break by itself (an id, a path, a URL, a name joined by
// underscores) and with far more rows than a demo has, at a phone and a
// tablet width, and the document must not be wider than the window. A block
// that is meant to scroll (code, a table) does so inside its own box; the
// allowlist names those.

const WIDTHS = [360, 390, 768]
// Boxes that scroll on their own, by selector.
// The strips are one row of tabs or chips that scroll on a phone by design
// (the Settings tabs outgrow 390 px with a wider font). The agent editor nav
// is one row of chips that scrolls inside itself on a phone, and so is the
// add-ons tab strip (it first overflowed at 360, the width added here).
const SCROLLS_INSIDE = ['pre', 'code', 'table', '.ag-pre', '.wk-shortcuts__chips', '.st-groups', '.dk-hubtabs', '.aed-nav', '.ad-tabs']

const join = (n, s) => Array.from({ length: n }, (_, i) => `${s}${i}`).join('_')
const WORD = join(9, 'unbroken_name_segment')
const URL = `https://example.com/${'a'.repeat(180)}`

async function overflow(page) {
  return page.evaluate((allow) => {
    const root = document.scrollingElement
    const vw = document.documentElement.clientWidth
    const bad = []
    const clipped = (el) => {
      for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
        const cs = getComputedStyle(p)
        if (cs.position === 'fixed' || cs.overflowX !== 'visible') return true
      }
      return getComputedStyle(el).position === 'fixed'
    }
    for (const el of document.body.querySelectorAll('*')) {
      const r = el.getBoundingClientRect()
      // Text that runs out of its own box counts too: the box fits, its content does not.
      const textOut = r.left + el.scrollWidth > vw + 1 && el.scrollWidth > el.clientWidth + 1 && !clipped(el) && getComputedStyle(el).overflowX === 'visible'
      if (r.width === 0 || (r.right <= vw + 1 && !textOut) || clipped(el)) continue
      const cs = getComputedStyle(el)
      if (cs.visibility === 'hidden' || cs.display === 'none') continue
      // The deepest boxes that pass the edge are the cause; their parents only follow.
      if ([...el.children].some(c => c.getBoundingClientRect().right > vw + 1 || c.getBoundingClientRect().left + c.scrollWidth > vw + 1)) continue
      const d = `${el.tagName.toLowerCase()}.${String(el.className).split(/\s+/).slice(0, 3).join('.')}`
      if (!bad.some(b => b.startsWith(d + ' '))) bad.push(`${d} w=${Math.round(r.width)} right=${Math.round(r.right)}`)
    }
    // A box that scrolls sideways inside the page must be one of the allowed.
    const scrollers = []
    for (const el of document.body.querySelectorAll('*')) {
      const cs = getComputedStyle(el)
      if ((cs.overflowX === 'auto' || cs.overflowX === 'scroll') && el.scrollWidth > el.clientWidth + 1 && !allow.some(s => el.matches(s))) {
        scrollers.push(`${el.tagName.toLowerCase()}.${String(el.className).split(/\s+/).slice(0, 3).join('.')}`)
      }
    }
    return { scrollWidth: root.scrollWidth, clientWidth: root.clientWidth, bad, scrollers }
  }, SCROLLS_INSIDE)
}

const longAgents = Array.from({ length: 6 }, (_, i) => ({
  ...AGENTS[0], id: `a${i + 1}`, name: `research_${WORD}_${i}`, description: `Searches ${URL} and writes briefs`,
  instructions: `${URL}\n${'x'.repeat(300)}`, model: `model_${WORD}`,
  mcp_servers: [`crm_${WORD}`, ...Array.from({ length: 6 }, (_, k) => `connector:m${k}`)], addons: ['office'],
  collections: Array.from({ length: 24 }, (_, k) => `collection_${WORD}_${k}`),
  skills: ['citations', `skill_${WORD}`], network: 'off',
  builtins: ['triggers.propose', 'memory.write', 'skills.write', 'files.upload', 'talkback.channels', 'act.send'], builtins_on: ['saved_agents'],
  folders: [{ path: `/Projects/${WORD}`, mode: 'write', state: 'ok' }, { path: `/Other/${'q'.repeat(160)}`, mode: 'read', state: 'ok' }],
  folder: { path: `/Projects/${WORD}`, mode: 'write', state: 'ok' },
}))
const longBindings = Array.from({ length: 4 }, (_, i) => ({ id: `b${i}`, name: `Support_channel_${WORD}_${i}`, kind: 'slack', agent_id: 'a1', enabled: true, caps: i % 2 ? ['triggers.propose'] : [] }))
const longConnectors = Array.from({ length: 6 }, (_, i) => ({ id: `m${i}`, name: `Docs_connector_${WORD}_${i}`, enabled: true }))

async function agentsSetup(page, extra = {}) {
  await installAgents(page, { agents: longAgents, bindings: longBindings, connectors: longConnectors, collections: longAgents[0].collections, ...extra })
}

async function chatSetup(page) {
  await mockChat(page)
  const detail = { id: 'c1', title: `Quotes ${WORD}`, model: 'qwen3-8b', kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z', running: false, pending: [] }
  const table = '| name | value | note |\n|---|---|---|\n' + Array.from({ length: 6 }, (_, i) => `| ${WORD}_${i} | ${'9'.repeat(40)} | ${URL} |`).join('\n')
  const code = '```js\n' + `const x = "${'long_string_'.repeat(30)}"\n` + '```'
  const messages = [
    { seq: 1, turn: 1, role: 'user', text: `compare ${URL} and ${WORD}`, at: '2026-10-25T10:00:00Z' },
    { seq: 2, turn: 1, role: 'tool', text: `web_fetch: {"url":"${URL}"}\n${'result_without_spaces_'.repeat(40)}`, at: '2026-10-25T10:00:01Z' },
    { seq: 3, turn: 1, role: 'assistant', text: `Here is ${URL}\n\n${table}\n\n${code}\n\n${'word_'.repeat(80)}\n\n- ${WORD}\n- [${URL}](${URL})`, at: '2026-10-25T10:00:02Z' },
  ]
  await page.route('**/api/chats/c1/events', route => route.abort())
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages } }))
  await page.route(/\/api\/chats\/c1$/, route => route.fulfill({ json: detail }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [detail], persistent: true, files: true } }))
  await page.route('**/api/chats/agents*', route => route.fulfill({ json: { agents: longAgents } }))
  await page.route('**/api/chats/push/**', route => route.fulfill({ status: 409, json: { error: { message: 'off' } } }))
  await page.route(/\/api\/chats\/notifications(\?.*)?$/, route => route.fulfill({ json: { notifications: [], unread: 0 } }))
}

const PAGES = [
  { name: 'agent editor', url: '/app/agents/a1/edit', ready: '[data-testid=agent-edit]', setup: agentsSetup },
  { name: 'new agent', url: '/app/agents/new', ready: '[data-testid=agent-wizard]', setup: agentsSetup },
  { name: 'agents list', url: '/app/agents', ready: '.ag-home, .page', setup: agentsSetup },
  { name: 'agent page', url: '/app/agents/a1', ready: '.page', setup: agentsSetup },
  { name: 'skills', url: '/app/skills', ready: '.page', setup: (page) => agentsSetup(page, { skills: Array.from({ length: 8 }, (_, i) => ({ ...SKILLS[0], name: `skill_${WORD}_${i}`, description: `${URL} ${'d'.repeat(120)}` })) }) },
  {
    name: 'home', url: '/app', ready: '.page, main',
    setup: async (page) => { await mockHome(page); await seedChats(page, sampleChats().map((c, i) => ({ ...c, name: `Chat ${WORD}_${i}` }))) },
  },
  { name: 'chat', url: '/app/chats/c1', ready: 'main', setup: chatSetup },
  {
    name: 'messaging connections', url: '/app/chats/connectors', ready: '.page',
    setup: async (page) => {
      await mockChat(page)
      await page.route('**/api/chats/agents*', r => r.fulfill({ json: { agents: longAgents } }))
      await page.route(/\/api\/chats\/connectors\/kinds$/, r => r.fulfill({ json: { kinds: [{ addon: 'slack', kind: 'slack', title: `Slack_${WORD}`, description: URL, page: 'bots' }] } }))
      await page.route(/\/api\/chats\/connectors(\?.*)?$/, r => r.fulfill({ json: { connectors: longBindings.map(b => ({ ...b, addon: 'slack', active: true, accept_any: false, allow: [WORD, URL], has_token: true })) } }))
      await page.route(/\/api\/chats(\?.*)?$/, r => r.fulfill({ json: { conversations: [], persistent: true, files: true } }))
      await page.route('**/api/chats/notifications*', r => r.fulfill({ json: { notifications: [], unread: 0 } }))
    },
  },
  { name: 'MCP connectors', url: '/app/mcp-connectors', ready: '.page', setup: (page) => agentsSetup(page, { connectors: longConnectors.map(c => ({ ...c, url: URL, description: URL })) }) },
  ...['apps', 'browse', 'installed', 'access'].map(tab => ({
    name: `add-ons ${tab}`, url: tab === 'apps' ? '/app/addons' : `/app/addons?tab=${tab}`, ready: '[data-testid=addons]',
    setup: async (page) => {
      await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(ADMIN, { addons: true }) }))
      await page.route('**/api/features', route => route.fulfill({ json: { addons: true, addon_navigation: true, chats: true, agents: true } }))
      await page.route('**/api/addons/builtin', route => route.fulfill({ json: { builtin: [] } }))
      await mockAddons(page, {
        apps: Array.from({ length: 6 }, (_, i) => app({ id: `app${i}`, name: `app${i}`, title: `Addon_${WORD}_${i}`, description: `${URL} ${'d'.repeat(100)}` })),
        installed: Array.from({ length: 6 }, (_, i) => installedAddon({ id: `app${i}`, title: `Addon_${WORD}_${i}`, description: URL })),
        releases: Array.from({ length: 6 }, (_, i) => release(`rel${i}_${WORD}`, { description: URL, name: `Release_${WORD}_${i}` })),
      })
    },
  })),
  { name: 'settings', url: '/app/settings', ready: '.page', setup: (page) => mockSettings(page, { cors_allow_origins: URL, instance_name: WORD, instance_tagline: URL, galleries: [{ url: URL, name: WORD }], api_keys: [`sk-${'k'.repeat(120)}`], collections_db_path: `/data/${WORD}` }) },
  {
    name: 'files', url: '/app/files', ready: '.page, main',
    setup: async (page) => {
      await mockChat(page)
      await page.route(/\/api\/chats\/files\/trash(\?.*)?$/, route => route.fulfill({ json: { entries: [] } }))
      await page.route(/\/api\/chats\/files(\?.*)?$/, route => route.fulfill({
        json: { path: '/', entries: Array.from({ length: 12 }, (_, i) => ({ path: `/file_${WORD}_${i}.txt`, name: `file_${WORD}_${i}.txt`, kind: i % 3 ? 'file' : 'dir', size: 1234, version: 1, rev: 3 })) },
      }))
      await page.route('**/api/workspace/events*', route => route.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream' }, body: ': ping\n\n' }))
    },
  },
  {
    name: 'memory collections', url: '/app/collections', ready: '.page',
    setup: (page) => mockLibrary(page, { collections: Array.from({ length: 20 }, (_, i) => `collection_${WORD}_${i}`), entries: { [`collection_${WORD}_0`]: [`${WORD}.pdf`, `${URL}.md`] } }),
  },
  {
    name: 'models', url: '/app/models', ready: 'body',
    setup: (page) => mockLedger(page, { gallery: GALLERY.map((e, i) => (i % 2 ? { ...e, name: `${e.name}_${WORD}`, description: `${URL} ${e.description}` } : e)) }),
  },
  {
    // Long names, a long failure and a failed source: the rows wrap, the page does not widen.
    name: 'updates', url: '/app/updates', ready: '[data-testid=updates-page]',
    setup: async (page) => {
      await mockOperate(page)
      await mockUpdates(page, {
        snapshot: snapshotOf(
          [galleryItem(`gallery_${WORD}`, '1.0.0', '1.1.0'), galleryItem(`failed_${WORD}`, '1', '2'), libraryItem(`library_${WORD}`, '0.1', '0.2')],
          { sources: { gallery: okSource(), library: { state: 'failed', error: URL, stale: true, checkedAt: minutesAgo(3), lastSuccessAt: minutesAgo(600) } } },
        ),
        operations: [failedOp(`failed_${WORD}`, URL), runningOp(`library_${WORD}`)],
        installed: [installedBackend(`current_${WORD}`, '1.0')],
        addons: [addonRecord('long', `Addon_${WORD}`, updateAvailable('2.0.0')), addonRecord('held', `Held_${WORD}`, updateAvailable('2.0.0'))],
        grants: { revision: 1, addons: [heldGrant('held', `Held_${WORD}`, '2.0.0')] },
      })
    },
  },
]

for (const width of WIDTHS) {
  test.describe(`no horizontal overflow at ${width}px`, () => {
    test.use({ viewport: { width, height: 900 } })
    for (const p of PAGES) {
      for (const theme of ['light', 'dark']) {
        test(`${p.name} (${theme})`, async ({ page }) => {
          await page.addInitScript((t) => localStorage.setItem('localai-theme', t), theme)
          await p.setup(page)
          await page.goto(p.url)
          await page.locator(p.ready).first().waitFor()
          if (p.open) for (const b of await page.locator(p.open).all()) await b.click()
          await page.waitForFunction(() => !document.querySelector('.skeleton, [aria-busy=true]'), null, { timeout: 5000 }).catch(() => {})
          await page.waitForTimeout(300)
          if (process.env.OVERFLOW_SHOTS) await page.screenshot({ path: `${process.env.OVERFLOW_SHOTS}/${p.name.replace(/ /g, '-')}-${theme}-${width}.png`, fullPage: true })
          const r = await overflow(page)
          expect(r.bad, `elements past the right edge (document ${r.scrollWidth} in ${r.clientWidth})`).toEqual([])
          expect(r.scrollWidth, 'document width').toBeLessThanOrEqual(r.clientWidth + 1)
          expect(r.scrollers, 'boxes that scroll sideways and are not allowed').toEqual([])
        })
      }
    }
  })
}

// The agent editor with the data of a plain saved agent: short names, no
// long text, one add-on, builtin switches, and two messaging connections,
// one of which lacks a permission (so a warning shows under a switch). The
// desktop widths are where a sidebar and a scrollbar take room.
const plainBase = { description: '', instructions: '', collections: [], mcp_servers: [], addons: ['sandbox'], skills: ['citations'], network: 'off', created: '2026-10-01T10:00:00Z', updated: '2026-10-02T10:00:00Z' }
const plainAgents = [
  { ...plainBase, id: 'a1', name: 'daily-assistant-x', model: 'qwen3-8b', builtins: ['triggers.propose', 'memory.write', 'talkback.channels', 'files.upload', 'act.files'], builtins_on: ['saved_agents'] },
  { ...plainBase, id: 'a2', name: 'support-triage-agent', model: 'qwen3-8b', builtins: ['triggers.propose', 'triggers.run', 'memory.write', 'talkback.channels', 'files.upload', 'act.send'], builtins_on: [] },
]
const plainCaps = ['triggers.propose', 'triggers.run', 'memory.write', 'talkback.channels', 'files.upload']
const plainBindings = [
  { id: 'b1', name: 'Team', kind: 'slack', agent_id: 'a1', enabled: true, caps: plainCaps },
  { id: 'b2', name: 'Personal', kind: 'telegram', agent_id: 'a2', enabled: true, caps: plainCaps.filter(c => c !== 'triggers.run') },
]

// 320 is the narrowest phone the editor supports; 360 is the common one.
for (const width of [1280, 1100, 390, 360, 320]) {
  for (const collapsed of [false, true]) {
    test.describe(`plain agent editor at ${width}px, sidebar ${collapsed ? 'collapsed' : 'open'}`, () => {
      test.use({ viewport: { width, height: 900 } })
      for (const aid of ['a1', 'a2']) {
        test(`${aid} with every fold open does not scroll sideways`, async ({ page }) => {
          await page.addInitScript((c) => localStorage.setItem('localai_sidebar_collapsed', String(c)), collapsed)
          await installAgents(page, { agents: plainAgents, bindings: plainBindings, skills: SKILLS })
          await page.route('**/api/chats/draft/tools*', route => route.fulfill({
            json: {
              ...DRAFT_TOOLS, servers: [], off: [],
              addons: [{ id: 'sandbox', tools: 3, off: false, title: 'Sandbox', description: 'Run commands in an isolated box.' }, { id: 'office', tools: 2, off: false, title: 'Office', description: 'Read and write documents.' }],
              connectors: [{ id: 'm1', name: 'Docs', off: false, usable: true }],
            },
          }))
          await page.route(/\/api\/chats\/connectors\/kinds$/, r => r.fulfill({ json: { kinds: [{ kind: 'slack', addon: 'slack', title: 'Slack' }, { kind: 'telegram', addon: 'telegram', title: 'Telegram' }], capabilities: [] } }))
          await page.goto(`/app/agents/${aid}/edit`)
          await page.locator('[data-testid=agent-edit]').waitFor()
          await page.waitForTimeout(300)
          const r = await overflow(page)
          expect(r.bad, `elements past the right edge (document ${r.scrollWidth} in ${r.clientWidth})`).toEqual([])
          expect(r.scrollWidth, 'document width').toBeLessThanOrEqual(r.clientWidth + 1)
          expect(r.scrollers, 'boxes that scroll sideways and are not allowed').toEqual([])
        })
      }
    })
  }
}

// "What it can use" with every kind of row: add-ons with long titles and
// accounts, a warning sentence, the tool and skill lists open, a resources list
// with long names and the amber state. 320 is the narrowest phone.
for (const width of [1280, 390, 360, 320]) {
  test.describe(`What it can use at ${width}px`, () => {
    test.use({ viewport: { width, height: 900 } })
    for (const theme of ['light', 'dark']) {
      test(`add-on rows, lists open, long names (${theme})`, async ({ page }) => {
        await page.addInitScript((t) => localStorage.setItem('localai-theme', t), theme)
        const addons = USES_ADDONS.map((a) => ({
          ...a, title: `${a.title} ${WORD}`, description: `${a.description} ${URL}`,
          connections: a.connections?.map((c) => ({ ...c, name: `${c.name} ${WORD}` })),
          resources: a.resources && { ...a.resources, noun_plural: `databases_${WORD}`, noun: `database_${WORD}` },
          tool_list: a.tool_list?.map((x) => ({ ...x, name: `${x.name}_${WORD}` })),
        }))
        const databases = { ...DATABASES, items: DATABASES.items.map((x) => ({ ...x, name: `${x.name}_${WORD}` })) }
        await installUses(page, { addons, databases, agents: [{ ...AGENTS[1], id: 'a2', addons: ['tracker', 'tables', 'office'], skills: ['pdf'] }] })
        await page.goto('/app/agents/a2/edit')
        await page.locator('[data-testid=agent-addon-box]').first().waitFor()
        for (const d of await page.locator('[data-testid=agent-uses] details').all()) await d.evaluate((e) => { e.open = true })
        await page.waitForTimeout(400)
        const r = await overflow(page)
        expect(r.bad, `elements past the right edge (document ${r.scrollWidth} in ${r.clientWidth})`).toEqual([])
        expect(r.scrollWidth, 'document width').toBeLessThanOrEqual(r.clientWidth + 1)
        expect(r.scrollers, 'boxes that scroll sideways and are not allowed').toEqual([])
      })
    }
  })
}
