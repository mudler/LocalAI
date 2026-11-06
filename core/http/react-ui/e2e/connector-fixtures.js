// The core API of the connector bindings, stubbed for the page of a connector
// add-on (Telegram, Slack): it records the calls and keeps the bindings and
// the sender log in memory.

export const AGENTS = [
  { id: 'a1', name: 'Support', model: 'qwen3-8b', mcp_servers: [], addons: [], collections: [], skills: [], builtins: ['memory.write', 'files.upload', 'triggers.propose', 'triggers.run', 'talkback.channels', 'talkback.new_channel', 'profile.write'] },
  { id: 'a2', name: 'No model', model: '', mcp_servers: [], addons: [], collections: [], skills: [], builtins: [] },
  { id: 'a4', name: 'Builder', model: 'qwen3-8b', mcp_servers: [], addons: [], collections: [], skills: [], builtins: ['memory.write', 'triggers.propose', 'act.files', 'act.addons'] },
  { id: 'a3', name: 'Careful', model: 'qwen3-8b', mcp_servers: [], addons: [], collections: [], skills: [], builtins: ['memory.write', 'files.upload'] },
]

const NEWCOMER = { id: '42', name: 'Newcomer', count: 2, allowed: false, first: '2026-10-27T08:00:00Z', last: '2026-10-27T08:05:00Z' }

const MIB = 1024 * 1024

// The files of a kind that declares 1 MiB in each direction.
export const ONE_MIB = {
  receive: { effective_bytes: MIB, layer: 'addon', declared_bytes: MIB, allowed_bytes: MIB },
  send: { effective_bytes: MIB, layer: 'addon', declared_bytes: MIB, allowed_bytes: MIB },
}

// The files of a kind that declares 20 MiB to read and 256 KiB to send.
export const DECLARED = {
  receive: { effective_bytes: 20 * MIB, layer: 'addon', declared_bytes: 20 * MIB, allowed_bytes: 20 * MIB },
  send: { effective_bytes: 256 * 1024, layer: 'addon', declared_bytes: 256 * 1024, allowed_bytes: 256 * 1024 },
}

// The closed list of capabilities that the kinds call returns, as the host writes it.
export const CAPABILITIES = [
  { name: 'files.upload', title: 'Receive files', description: 'The agent may read files that senders attach to a message.', sensitive: false, usual: true },
  { name: 'memory.write', title: 'Save to memory', description: 'The agent may remember what a sender asks it to remember.', sensitive: false, usual: true },
  { name: 'proposals.confirm', title: 'Confirm changes from the chat', description: 'A sender can confirm a trigger or a change that the agent proposed (a memory note, the agent rules, a skill, a page or the dashboard) by answering yes or no in the same chat. Changes to the rules, skills, pages and dashboard need a verified person.', sensitive: false, usual: true },
  { name: 'talkback.channels', title: 'Post to other channels', description: 'The agent may write to the channels listed under Place the bot may post when a sender asks.', sensitive: false, usual: true },
  { name: 'talkback.new_channel', title: 'Post to a new channel', description: 'The agent may write to a channel that is not approved yet.', sensitive: true, usual: false },
  { name: 'tools.write', title: 'Let people who are not verified change things with add-on tools', description: 'Verified people already use the write tools of the add-ons the agent is connected to. Turn this on for people on the allowlist who are not paired.', sensitive: false, usual: false },
  { name: 'triggers.propose', title: 'Propose triggers', description: 'The agent may propose a timer or an event trigger when a sender asks for one.', sensitive: false, usual: true },
  { name: 'triggers.run', title: 'Fire event triggers', description: 'A message from a sender may start a run of an event trigger.', sensitive: false, usual: true },
]

// A kind that declares two roles and what it can give to them.
export const ROLES_KIND = {
  roles: [{ id: 'workspace_admin', title: 'Workspace admin', description: 'Owners and admins of the workspace.' }, { id: 'member', title: 'Channel member' }],
  permissions: [{ name: 'memory.write', sensitive: false }, { name: 'files.upload', sensitive: false }, { name: 'talkback.channels', sensitive: false }, { name: 'talkback.new_channel', sensitive: true }],
}

// The held-back capabilities of the grants an agent can hold (those that are
// not grants of the agent are never held back by it).
const AGENT_GRANTS = ['memory.write', 'files.upload', 'triggers.propose', 'triggers.run', 'talkback.channels', 'talkback.new_channel']

// A stand-in for the host's access preview: the same shape and the same order
// of checks (agent permission, connection permission, sender trust), written
// from the form values of the request.
// The verb phrases the host uses for each capability in its sentences.
const WORDS = {
  'proposals.confirm': 'confirm proposals', 'memory.write': 'save notes to memory', 'files.upload': 'receive files',
  'triggers.propose': 'set reminders and scheduled tasks', 'triggers.run': 'run scheduled tasks',
  'talkback.channels': 'write to other channels', 'talkback.new_channel': 'start conversations in new channels',
  'profile.write': "change the agent's rules", 'skills.write': 'save skills', 'pages.write': 'publish pages',
  'dashboard.write': 'change the dashboard', 'knowledge.write': 'write to knowledge', 'tools.write': 'change things in other services with add-on tools',
}

export function previewFor(body, roles = []) {
  const agent = AGENTS.find(a => a.id === body.agent_id)
  const decide = (c, who) => {
    if (!body.agent_id) return { capability: c.name, allowed: false, code: 'no_agent', blocked_by: 'connection', message: 'This connection has no agent, so the bot cannot answer or act yet.', fix: { kind: 'open_connection', target: 'agent_id' }, then_check: ['sender'] }
    if (agent && AGENT_GRANTS.includes(c.name) && !agent.builtins.includes(c.name)) {
      return { capability: c.name, allowed: false, code: 'agent_permission_off', blocked_by: 'agent', message: `The agent is not allowed to ${WORDS[c.name]}. Turn on the matching permission in the agent.`, fix: { kind: 'open_agent', target: agent.id, field: c.name }, then_check: ['connection', 'sender'] }
    }
    if (!(body.caps || []).includes(c.name)) {
      return { capability: c.name, allowed: false, code: 'connection_permission_off', blocked_by: 'connection', message: `This connection does not allow the bot to ${WORDS[c.name]}. Tick the matching permission on the connection.`, fix: { kind: 'open_connection', target: 'caps', field: c.name }, then_check: ['sender'] }
    }
    if (who === 'channel_acts') return c.sensitive ? { capability: c.name, allowed: false, code: 'sender_needs_pairing', blocked_by: 'sender', message: `This person needs to be verified with this bot before the bot can ${WORDS[c.name]} for them.`, fix: { kind: 'pair_sender', target: 'pairing' } } : { capability: c.name, allowed: true, via: 'channel' }
    if (who === 'anyone') return { capability: c.name, allowed: false, code: 'channel_not_trusted', blocked_by: 'sender', message: `People in a channel are not trusted one by one, so the bot cannot ${WORDS[c.name]} for them. Add the person to the allowed people.`, fix: { kind: 'add_person', target: 'allow' } }
    if (who.startsWith('role:')) {
      const id = who.slice(5)
      return (body.grants || []).some(g => g.role === id && g.caps.includes(c.name)) && !c.sensitive
        ? { capability: c.name, allowed: true, via: who }
        : { capability: c.name, allowed: false, code: 'role_not_granted', blocked_by: 'sender', message: `The role of this person does not let the bot ${WORDS[c.name]}. Allow it for that role on the connection.`, fix: { kind: 'open_connection', target: 'grants' } }
    }
    if (c.sensitive && who !== 'paired') return { capability: c.name, allowed: false, code: 'sender_needs_pairing', blocked_by: 'sender', message: `This person needs to be verified with this bot before the bot can ${WORDS[c.name]} for them.`, fix: { kind: 'pair_sender', target: 'pairing' } }
    return { capability: c.name, allowed: true, via: who === 'paired' ? 'paired' : 'allowlist' }
  }
  const subjects = [
    { id: 'listed', title: 'A listed person' },
    { id: 'anyone', title: 'A sender let in through a listed channel' },
    { id: 'channel_acts', title: 'A sender in a channel set to let everyone act' },
    { id: 'paired', title: 'A verified person' },
    ...roles.map(r => ({ id: `role:${r.id}`, title: r.title })),
  ].map(sub => ({ ...sub, decisions: CAPABILITIES.map(c => decide(c, sub.id)) }))
  return { binding: 'b1', preview: true, subjects, recent: [], proposal_mode: body.approval_mode || 'ask' }
}

export function connectorApi(page, initial = [], kind = 'telegram', senders = [NEWCOMER], files = ONE_MIB, { capabilities = CAPABILITIES, kindExtra = {}, access = null, preview = null, previewDelay = 0, previewStatus = 200 } = {}) {
  const s = { items: structuredClone(initial), calls: [], senders: structuredClone(senders), previews: [], urls: [] }
  page.on('request', r => { if (r.url().includes('/api/chats/connectors/')) s.urls.push(r.url()) })
  const rec = (method, url, body) => s.calls.push({ method, url: new URL(url).pathname, body })
  return {
    s,
    async install() {
      await page.route('**/api/chats/agents*', r => r.fulfill({ json: { agents: AGENTS } }))
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/senders\/[^/?]+\/allow$/, r => {
        rec('POST', r.request().url(), null)
        s.senders = s.senders.map(x => ({ ...x, allowed: true }))
        return r.fulfill({ json: s.items[0] })
      })
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/access\/preview(\?.*)?$/, async r => {
        const body = r.request().postDataJSON()
        s.previews.push({ id: decodeURIComponent(new URL(r.request().url()).pathname.split('/').at(-3)), body })
        if (previewDelay) await new Promise(ok => setTimeout(ok, previewDelay))
        if (previewStatus !== 200) return r.fulfill({ status: previewStatus, json: { error: { message: 'The check is not available right now.' } } })
        return r.fulfill({ json: typeof preview === 'function' ? preview(body) : preview || previewFor(body, kindExtra.roles || []) })
      })
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/access(\?.*)?$/, r => r.fulfill({ json: access || { binding: 'b1', subjects: [], recent: [] } }))
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/pairing$/, r => {
        rec('POST', r.request().url(), null)
        return r.fulfill({ json: { code: 'LA-K7M2QX9P', expires: new Date(Date.now() + 10 * 60 * 1000).toISOString() } })
      })
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/paired\/[^/?]+$/, r => {
        rec('DELETE', r.request().url(), null)
        const sender = decodeURIComponent(new URL(r.request().url()).pathname.split('/').pop())
        s.items = s.items.map(x => ({ ...x, paired: (x.paired || []).filter(p => p.sender_id !== sender) }))
        return r.fulfill({ json: s.items[0] })
      })
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/senders$/, r => r.fulfill({ json: { senders: s.senders } }))
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/test$/, r => r.fulfill({ json: { bot_id: 1, bot_username: 'support_bot' } }))
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/(enable|disable)$/, r => {
        const verb = new URL(r.request().url()).pathname.split('/').pop()
        rec('POST', r.request().url(), null)
        s.items = s.items.map(x => ({ ...x, enabled: verb === 'enable' }))
        return r.fulfill({ json: s.items[0] })
      })
      await page.route(/\/api\/chats\/connectors\/[^/?]+$/, r => {
        const req = r.request()
        rec(req.method(), req.url(), req.postDataJSON?.())
        if (req.method() === 'DELETE') { s.items = []; return r.fulfill({ status: 204, body: '' }) }
        s.items = s.items.map(x => ({ ...x, ...req.postDataJSON(), token: undefined, has_token: true }))
        return r.fulfill({ json: s.items[0] })
      })
      await page.route(/\/api\/chats\/connectors(\?.*)?$/, r => {
        const req = r.request()
        if (req.method() === 'POST') {
          const b = req.postDataJSON()
          rec('POST', req.url(), b)
          s.items.push({ id: 'b1', kind, allow: [], ...b, token: undefined, has_token: true })
          return r.fulfill({ status: 201, json: s.items.at(-1) })
        }
        return r.fulfill({ json: { connectors: s.items } })
      })
      await page.route(/\/api\/chats(\?.*)?$/, r => r.fulfill({ json: { conversations: [], persistent: true, files: true } }))
      await page.route('**/api/chats/notifications*', r => r.fulfill({ json: { notifications: [], unread: 0 } }))
      // Registered last, so it answers before the route of one binding.
      await page.route(/\/api\/chats\/connectors\/kinds(\?.*)?$/, r => r.fulfill({ json: { kinds: [{ addon: kind, kind, title: kind, files, ...kindExtra }], capabilities } }))
    },
  }
}

// Opens a disclosure of the sheet when it is closed.
async function openDetails(page, testId) {
  const d = page.getByTestId(testId)
  if ((await d.getAttribute('open')) === null) await d.locator('> summary').click()
}

// Opens "Replies, files and places" of the sheet, where the answer settings are.
export const openReplies = (page) => openDetails(page, 'connector-replies-more')

// Opens "Check what it will do" of the sheet (Try it, refusals, tool calls).
export const openCheck = (page) => openDetails(page, 'connector-check-more')

// Opens the group "File sizes and reply language", inside "Replies, files and places", when it is closed.
export async function openAnswerMore(page) {
  await openReplies(page)
  await openDetails(page, 'connector-answer-more')
}

// Picks the Custom preset, which shows the whole matrix of who can do what.
export async function openMatrix(page) {
  const matrix = page.getByTestId('connector-matrix')
  if (await matrix.count() === 0) await page.getByTestId('connector-preset-custom').check()
  return matrix
}

// The menu of a row (Edit, Delete): a row by name, or the only row.
export async function rowMenu(page, item, name) {
  const row = name ? page.getByTestId('connector-row').filter({ hasText: name }) : page.getByTestId('connector-row')
  await row.locator('.action-menu__trigger').click()
  await page.getByRole('menuitem', { name: item, exact: true }).click()
}

// Opens the edit sheet of a saved connection from the menu of its row.
export async function openEdit(page, name) {
  const row = name ? page.getByTestId('connector-row').filter({ hasText: name }) : page.getByTestId('connector-row')
  await row.locator('.action-menu__trigger').click()
  // Edit is the first item of the menu, whatever the language.
  await page.getByRole('menuitem').first().click()
}

// Opens the pane of a connection by a click on its row.
export const openPane = (page, name) => (name ? page.getByTestId('connector-row').filter({ hasText: name }) : page.getByTestId('connector-row')).getByTestId('connector-open').click()

// Saves the open sheet: the primary button, which also tests the connection.
export const saveSheet = (page) => page.getByTestId('connector-save').click()

// Measures a select the way a reader sees it. A native select shows the text
// of the selected option only, and it has no scrollWidth to read, so each
// option is made the selected one in turn and its text is measured, with the
// font of the select, against the room the select leaves for text (its width
// without padding, which holds the chevron). The choice is put back. It
// returns the options that would be cut, with both widths. With `all` false
// only the option that is selected now is measured.
export async function clippedOptions(select, { all = true } = {}) {
  return select.evaluate((el, every) => {
    const cs = getComputedStyle(el)
    const ctx = document.createElement('canvas').getContext('2d')
    ctx.font = `${cs.fontStyle} ${cs.fontWeight} ${cs.fontSize} ${cs.fontFamily}`
    const room = el.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight)
    const kept = el.selectedIndex
    const out = []
    for (let i = 0; i < el.options.length; i++) {
      if (!every && i !== kept) continue
      el.selectedIndex = i
      const text = el.selectedOptions[0].textContent
      const width = ctx.measureText(text).width
      if (width > room + 0.5) out.push({ text, width: Math.ceil(width), room: Math.floor(room) })
    }
    el.selectedIndex = kept
    return { scrollWidth: el.scrollWidth, clientWidth: el.clientWidth, out }
  }, all)
}

// Opens everything the sheet keeps folded: the Custom preset (the matrix), the
// replies and the check. Only what the open sheet has is touched.
// A new connection starts with the status as one quiet line; this opens the list.
export async function openReadiness(page) {
  const quiet = page.getByTestId('connector-ready-quiet')
  if (await quiet.count() > 0 && (await quiet.getAttribute('aria-expanded')) !== 'true') await quiet.click()
}

export async function expandAll(page) {
  const custom = page.getByTestId('connector-preset-custom')
  if (await custom.count() > 0) await custom.check()
  for (const id of ['connector-replies-more', 'connector-check-more']) {
    if (await page.getByTestId(id).count() > 0) await openDetails(page, id)
  }
}
