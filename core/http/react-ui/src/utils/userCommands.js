// The slash commands of the user (LocalAI/commands.json): pure helpers, so the
// node unit tests load them. The server checks the same rules in
// core/services/studio/wellknown/commands.go.
//
// A command is {name, description, type: 'prompt' | 'tool', template?, args?,
// tool?}. It expands into text that goes into the message box, where the
// person reads it and sends it by hand. It never sets a system prompt, and a
// tool command only asks the assistant to use a tool: the tool keeps its own
// approval step.

export const COMMAND_LIMITS = Object.freeze({ commands: 30, args: 8, description: 120, template: 2000, default: 200, tool: 64 })

// The built-in commands a user command cannot take. The schema and the server
// list the same names; tests keep the three lists in step with the registry of
// the composer.
export const RESERVED_COMMANDS = Object.freeze([
  'about', 'agent', 'agents', 'apps', 'assistant', 'attach', 'chat', 'chats', 'clear', 'compact',
  'delete', 'docs', 'effort', 'export', 'find', 'gallery', 'goal', 'help', 'import', 'installed',
  'knowledge', 'loop', 'mcp', 'memory', 'model', 'models', 'new', 'settings', 'skill', 'skills', 'stop',
])

const NAME_RE = /^[a-z][a-z0-9-]{0,31}$/
const ARG_RE = /^[a-z][a-z0-9_]{0,23}$/
const TOOL_RE = /^[A-Za-z0-9_.-]{1,64}$/
const USE_RE = /\{\{\s*([^{}]*?)\s*\}\}/g

export const commandNameOk = (name) => typeof name === 'string' && NAME_RE.test(name)
export const argNameOk = (name) => typeof name === 'string' && ARG_RE.test(name)
export const toolNameOk = (name) => typeof name === 'string' && TOOL_RE.test(name)

// templateArgs lists the argument names a template uses, in order of first use.
export function templateArgs(template) {
  const out = []
  for (const m of String(template || '').matchAll(USE_RE)) if (!out.includes(m[1])) out.push(m[1])
  return out
}

// readCommands reads the list of commands.json for the composer: the entries
// that have the shape of a command, in file order. The server already refuses
// a file that breaks the schema and shows the last good version, so this only
// guards against a shape it does not know.
export function readCommands(data) {
  const list = Array.isArray(data?.commands) ? data.commands : []
  const seen = new Set()
  const out = []
  for (const c of list) {
    if (!c || typeof c !== 'object' || !commandNameOk(c.name) || seen.has(c.name)) continue
    if (typeof c.description !== 'string' || !c.description.trim()) continue
    if (c.type === 'prompt' && typeof c.template === 'string' && c.template) {
      const args = (Array.isArray(c.args) ? c.args : []).filter(a => a && argNameOk(a.name) && (typeof a.default === 'string' || typeof a.default === 'number'))
      out.push({ ...c, args })
    } else if (c.type === 'tool' && toolNameOk(c.tool)) {
      out.push({ ...c })
    } else continue
    seen.add(c.name)
  }
  return out
}

// splitTyped splits what the person typed after the name into one value per
// argument, in order. The last argument takes the rest of the text, so a
// sentence fits in it. Extra text of a command without arguments is the rest.
export function splitTyped(rest, argCount) {
  const text = String(rest || '').trim()
  if (!text || argCount <= 0) return []
  const out = []
  let cur = text
  for (let i = 0; i < argCount - 1; i++) {
    const m = /^(\S+)\s*([\s\S]*)$/.exec(cur)
    if (!m) { cur = ''; break }
    out.push(m[1])
    cur = m[2]
  }
  if (cur) out.push(cur)
  return out
}

// argValues pairs each argument with what was typed, or its default.
export function argValues(command, rest) {
  const args = command.args || []
  const typed = splitTyped(rest, args.length)
  return args.map((a, i) => ({ name: a.name, value: typed[i] !== undefined ? typed[i] : String(a.default), typed: typed[i] !== undefined }))
}

// expandCommand turns a command and the text typed after its name into the
// prompt that goes in the message box. A value is put in once and never read
// as a template again. toolText(tool) is the sentence of a tool command, in the
// language of the page. For a command without arguments, the typed text is
// added after the template (a prompt command) or the tool sentence.
export function expandCommand(command, rest = '', { toolText = (tool) => `Use the ${tool} tool.` } = {}) {
  const extra = String(rest || '').trim()
  if (command.type === 'tool') return [toolText(command.tool), extra].filter(Boolean).join(' ')
  const args = command.args || []
  if (args.length === 0) return [command.template, extra].filter(Boolean).join(' ')
  const values = Object.fromEntries(argValues(command, extra).map(v => [v.name, v.value]))
  return command.template.replace(USE_RE, (all, name) => (name in values ? values[name] : all))
}

// matchTyped finds the command a typed text starts with: "/standup 2" gives
// the command named standup and "2". It answers null when the text is not a
// command of the list.
export function matchTyped(text, commands) {
  const m = /^\/([a-z][a-z0-9-]*)(?:\s+([\s\S]*))?$/.exec(String(text || '').trim())
  if (!m) return null
  const command = (commands || []).find(c => c.name === m[1])
  return command ? { command, rest: m[2] || '' } : null
}

// commandProblems says what is wrong with one command of the editor, as codes
// the editor translates; an empty list means it can be saved. others is the
// list of the other commands, for the unique name.
export function commandProblems(c, others = []) {
  const out = []
  const name = (c.name || '').trim()
  if (!name) out.push('nameEmpty')
  else if (!commandNameOk(name)) out.push('nameFormat')
  else {
    if (RESERVED_COMMANDS.includes(name)) out.push('nameBuiltin')
    if (others.some(o => (o.name || '').trim() === name)) out.push('nameTaken')
  }
  if (!(c.description || '').trim()) out.push('descriptionEmpty')
  if ((c.description || '').length > COMMAND_LIMITS.description) out.push('descriptionLong')
  if (c.type === 'tool') {
    if (!(c.tool || '').trim()) out.push('toolEmpty')
    else if (!toolNameOk((c.tool || '').trim())) out.push('toolFormat')
    return out
  }
  const tpl = c.template || ''
  if (!tpl.trim()) out.push('templateEmpty')
  if (tpl.length > COMMAND_LIMITS.template) out.push('templateLong')
  const args = c.args || []
  const names = args.map(a => (a.name || '').trim())
  if (names.some(n => !argNameOk(n))) out.push('argFormat')
  if (new Set(names).size !== names.length) out.push('argTwice')
  if (args.some(a => String(a.default ?? '') === '')) out.push('argDefault')
  if (args.some(a => String(a.default ?? '').length > COMMAND_LIMITS.default)) out.push('argDefaultLong')
  const used = templateArgs(tpl)
  if (used.some(n => !names.includes(n))) out.push('argMissing')
  if (names.some(n => n && !used.includes(n))) out.push('argUnused')
  if (/\{\{|\}\}/.test(tpl.replace(USE_RE, ''))) out.push('braces')
  if (args.length > COMMAND_LIMITS.args) out.push('argCount')
  return out
}

// cleanCommands shapes the editor rows for the file: trimmed, with only the
// keys of the type, and without the editor's own marks (keys that start with
// an underscore).
export function cleanCommands(rows) {
  return rows.map((r) => {
    const base = { name: r.name.trim(), description: r.description.trim(), type: r.type }
    if (r.type === 'tool') return { ...base, tool: r.tool.trim() }
    const out = { ...base, template: r.template }
    const args = (r.args || []).map(a => ({ name: a.name.trim(), default: typeof a.default === 'number' ? a.default : String(a.default) }))
    if (args.length) out.args = args
    return out
  })
}

// withArgsFromTemplate puts the arguments of a row in step with its template:
// one per {{name}}, in the order of first use, each keeping its default; a new
// one starts with an empty default for the person to fill. An argument the
// template no longer names is left out.
export function withArgsFromTemplate(row) {
  const names = templateArgs(row.template).filter(argNameOk)
  const have = new Map((row.args || []).map(a => [a.name, a]))
  return { ...row, args: names.map(n => have.get(n) || { name: n, default: '' }) }
}
