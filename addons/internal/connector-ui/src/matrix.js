// The matrix "who can do what": one row for each capability, one column for
// each kind of sender, and every cell taken from the host's own check (the
// access preview). Nothing here decides: a cell is the word for what the host
// said. A capability the host refuses in a link before the sender (the server,
// the tools switch, the agent, the connection) reads Off, and one refused for
// this kind of sender reads No.

// The columns, in the order of the table. subject is the id of the subject in
// the preview. "On for this bot" has no subject: it is the permission switch.
export const MATRIX_COLUMNS = [
  { id: 'on' },
  { id: 'paired', subject: 'paired' },
  { id: 'listed', subject: 'listed' },
  { id: 'channel_acts', subject: 'channel_acts' },
  { id: 'channel_member', subject: 'anyone' },
  { id: 'anyone', subject: 'anyone', onlyWhenAny: true },
]

// The columns shown for a form and a preview: the "Anyone" column only when
// the bot accepts anyone, and a column whose subject the host did not send is
// left out rather than guessed. A kind that declares roles gets one column for
// each role; those are the only columns the page lets the owner edit (grants).
export function matrixColumns(form, view, roles = []) {
  const has = (id) => (view?.subjects || []).some((s) => s.id === id)
  const kinds = MATRIX_COLUMNS.filter((c) => {
    if (c.onlyWhenAny) return Boolean(form?.accept_any) && has(c.subject)
    return !c.subject || has(c.subject)
  })
  const byRole = (roles || []).filter((r) => has(`role:${r.id}`)).map((r) => ({ id: `role:${r.id}`, subject: `role:${r.id}`, role: r.id, title: r.title }))
  return [...kinds, ...byRole]
}

// The links before the sender: a capability refused there is switched off.
const OFF_LINKS = new Set(['server', 'tools', 'agent', 'connection'])

// How the host says a capability asks first, for the rows that do.
function asksFirst(cap, subject, view) {
  if (cap === 'memory.write') return subject?.note_save === 'ask'
  if (cap === 'triggers.propose') return Boolean(view?.proposal_mode) && view.proposal_mode !== 'auto'
  return false
}

// The state of one cell: yes, asks, no, off or waiting, with the decision it
// came from (for the message, the fix and the checks that are left).
export function cellOf(decision, subject, view, cap) {
  if (!decision) return { state: 'unknown', decision: null }
  if (!decision.allowed) return { state: OFF_LINKS.has(decision.blocked_by) ? 'off' : 'no', decision }
  return { state: asksFirst(cap, subject, view) ? 'asks' : 'yes', decision }
}

// The sub-rows of the permission to change things with add-on tools: one for
// each tool the host names. A tool that waits for a person says so; one the
// owner relaxed runs at once for a verified person only.
function toolState(base, tool, hold, view, col) {
  if (base.state !== 'yes') return base
  const approval = (view?.approvals || []).find((a) => a.tool === tool)
  if (approval && (approval.runs_without_asking || []).includes('paired') && col === 'paired') return { ...base, state: 'yes' }
  if ((hold.connections || []).some((c) => c.write_mode === 'propose')) return { ...base, state: 'waiting' }
  if (approval?.skip && col !== 'paired') return { ...base, state: 'asks' }
  return base
}

// The rows of the matrix. Each has key, cap, title and cells (by column id);
// a row of a tool also has parent.
export function matrixRows(view, capabilities, form, columns) {
  const subjectOf = (col) => (view?.subjects || []).find((s) => s.id === col.subject)
  const rows = []
  for (const c of capabilities || []) {
    const cells = {}
    for (const col of columns) {
      if (col.id === 'on') { cells.on = { state: (form?.caps || []).includes(c.name) ? 'yes' : 'off', decision: null }; continue }
      const subject = subjectOf(col)
      cells[col.id] = cellOf((subject?.decisions || []).find((d) => d.capability === c.name), subject, view, c.name)
    }
    rows.push({ key: c.name, cap: c.name, title: c.title, cells })
    for (const hold of (view?.addon_tools || []).filter((h) => h.capability === c.name)) {
      for (const tool of hold.tools || []) {
        const sub = {}
        for (const col of columns) sub[col.id] = toolState(cells[col.id], tool, hold, view, col.id)
        rows.push({ key: `${c.name}:${hold.addon}:${tool}`, cap: c.name, parent: c.name, addon: hold.addon, tool, cells: sub })
      }
    }
  }
  return rows
}
