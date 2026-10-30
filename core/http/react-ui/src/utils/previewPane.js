// The decisions of a chat's preview pane, kept apart from React so they can be
// tested in node: which files and code blocks have a preview, what a turn
// produced that should open the pane, and the chats where the person closed
// the pane (the pane then stays closed there until they open it again).
// No DOM and no network here; the storage is passed in.

// A code block of this many lines is a deliverable, not a snippet.
export const MIN_BLOCK_LINES = 25

// The view a file gets, by its name (and type, when the server gave one).
//   table     a spreadsheet or delimited text, read by the server
//   document  a Word document, read by the server as text
//   slides    a PowerPoint presentation, read by the server as text
//   pdf       the browser's PDF viewer
//   html      a sandboxed frame
//   image     an image (SVG included: an <img> never runs its script)
//   audio, video   the native players
//   markdown, json, code, text   the text of the file
//   none      a card with the name, the type, the size and Download
const VIEWS = [
  [/\.(xlsx|xlsm|csv|tsv)$/i, 'table'],
  [/\.docx$/i, 'document'],
  [/\.pptx$/i, 'slides'],
  [/\.pdf$/i, 'pdf'],
  [/\.html?$/i, 'html'],
  [/\.(png|jpe?g|gif|webp|svg|avif|bmp)$/i, 'image'],
  [/\.(mp3|wav|ogg|oga|m4a|flac|opus)$/i, 'audio'],
  [/\.(mp4|webm|mov|m4v)$/i, 'video'],
  [/\.(md|markdown)$/i, 'markdown'],
  [/\.json$/i, 'json'],
  [/\.(js|mjs|cjs|jsx|ts|tsx|py|go|rs|c|h|cc|cpp|hpp|java|kt|rb|php|cs|swift|sh|bash|zsh|sql|css|scss|ya?ml|toml|ini|xml|lua|r|pl|dockerfile)$/i, 'code'],
  [/\.(txt|log|text|rst|org|env|conf|cfg)$/i, 'text'],
]

export function viewOf(name, mime = '') {
  for (const [re, view] of VIEWS) if (re.test(name || '')) return view
  if (mime.startsWith('image/')) return 'image'
  if (mime.startsWith('audio/')) return 'audio'
  if (mime.startsWith('video/')) return 'video'
  if (mime === 'application/pdf') return 'pdf'
  if (mime.startsWith('text/')) return 'text'
  return 'none'
}

// The views that read the server's preview endpoint.
export const SERVER_VIEWS = new Set(['table', 'document', 'slides'])
// The views that show the text of the file, and so can copy it.
export const TEXT_VIEWS = new Set(['html', 'markdown', 'json', 'code', 'text'])

// The highlighter's language of a file, by its extension.
const LANG_BY_EXT = {
  js: 'javascript', mjs: 'javascript', cjs: 'javascript', jsx: 'javascript', ts: 'typescript', tsx: 'typescript',
  py: 'python', go: 'go', rs: 'rust', c: 'c', h: 'c', cc: 'cpp', cpp: 'cpp', hpp: 'cpp', java: 'java', kt: 'kotlin',
  rb: 'ruby', php: 'php', cs: 'csharp', swift: 'swift', sh: 'bash', bash: 'bash', zsh: 'bash', sql: 'sql', css: 'css',
  scss: 'scss', yaml: 'yaml', yml: 'yaml', toml: 'ini', ini: 'ini', xml: 'xml', html: 'xml', htm: 'xml', lua: 'lua',
  r: 'r', pl: 'perl', json: 'json', md: 'markdown', markdown: 'markdown',
}

export function languageOf(name) {
  const m = /\.([A-Za-z0-9]+)$/.exec(name || '')
  return m ? LANG_BY_EXT[m[1].toLowerCase()] || '' : ''
}

// The file name and extension of a code block's language, for its title and
// its download.
const EXT_BY_LANG = {
  html: 'html', javascript: 'js', js: 'js', typescript: 'ts', ts: 'ts', jsx: 'jsx', tsx: 'tsx', python: 'py', py: 'py',
  css: 'css', svg: 'svg', json: 'json', yaml: 'yaml', yml: 'yaml', go: 'go', rust: 'rs', java: 'java', markdown: 'md',
  md: 'md', bash: 'sh', sh: 'sh', shell: 'sh', sql: 'sql', xml: 'xml', csv: 'csv', c: 'c', cpp: 'cpp', ruby: 'rb', php: 'php',
}
const BASE_BY_EXT = { html: 'index', js: 'script', ts: 'script', jsx: 'component', tsx: 'component', py: 'script', css: 'styles', svg: 'image', json: 'data', yaml: 'config', go: 'main', rs: 'main', java: 'Main', md: 'document', sh: 'script', sql: 'query' }

export function extensionForLanguage(lang) {
  return EXT_BY_LANG[(lang || '').toLowerCase()] || 'txt'
}

// A file name that the text just before a block gives it: "index.html:",
// "Save this as `app.py`", "**report.md**". Only names with a known extension
// count, so "e.g." or "v1.2" are not names.
const NAMED = /([A-Za-z0-9_][A-Za-z0-9_.-]*\.(html?|css|js|mjs|jsx|ts|tsx|py|go|rs|java|json|ya?ml|toml|md|markdown|sh|sql|svg|xml|csv|tsv|txt|c|cpp|h|rb|php|kt|swift|lua|ini|env))\b/i

export function presentedName(before) {
  const lines = String(before || '').trimEnd().split('\n')
  const last = lines[lines.length - 1] || ''
  const m = NAMED.exec(last)
  return m ? m[1] : ''
}

// What a code block shows as its title: the name the text gave it, else a
// name from its language ("index.html", "script-2.py").
export function blockTitle(lang, index, before) {
  const named = presentedName(before)
  if (named) return named
  const ext = extensionForLanguage(lang)
  const base = BASE_BY_EXT[ext] || 'snippet'
  return index > 0 ? `${base}-${index + 1}.${ext}` : `${base}.${ext}`
}

const lineCount = (code) => String(code || '').replace(/\n$/, '').split('\n').length

// Whether a code block is a result worth the pane: a whole HTML page, an SVG,
// a block of MIN_BLOCK_LINES lines or more, or one the text presents as a file.
// A short snippet never is.
export function isPreviewableBlock({ lang = '', code = '', before = '' }) {
  const l = lang.toLowerCase()
  const head = String(code).trimStart().slice(0, 200).toLowerCase()
  if ((l === 'html' || l === '') && /^<!doctype html|^<html[\s>]|<body[\s>]/.test(head)) return true
  if ((l === 'svg' || l === 'xml' || l === '') && head.startsWith('<svg')) return true
  if (lineCount(code) >= MIN_BLOCK_LINES) return true
  return presentedName(before) !== '' && lineCount(code) >= 3
}

// The view of a code block in the pane.
export function blockView(lang, code) {
  const l = (lang || '').toLowerCase()
  const head = String(code || '').trimStart().slice(0, 200).toLowerCase()
  if (l === 'svg' || head.startsWith('<svg')) return 'image'
  if (l === 'html' && /^<!doctype html|^<html[\s>]|<body[\s>]/.test(head)) return 'html'
  if (l === 'md' || l === 'markdown') return 'markdown'
  if (l === 'json') return 'json'
  return 'code'
}

const FENCE = /^(`{3,}|~{3,})[ \t]*([\w+#.-]*)[^\n]*\n([\s\S]*?)\n?^\1[ \t]*$/gm

// The complete fenced code blocks of a message, in order: { index, lang, code,
// before } where before is the text between the previous block and this one.
export function codeBlocksOf(text) {
  const out = []
  const src = String(text || '')
  let last = 0
  for (const m of src.matchAll(FENCE)) {
    out.push({ index: out.length, lang: (m[2] || '').toLowerCase(), code: m[3], before: src.slice(last, m.index) })
    last = m.index + m[0].length
  }
  return out
}

// A code block of the transcript as the pane shows it. id is stable across
// renders: the message's seq and the block's index.
export function blockItem(msg, block) {
  return {
    type: 'block',
    id: `${msg.seq}-${block.index}`,
    seq: msg.seq,
    index: block.index,
    lang: block.lang,
    code: block.code,
    name: blockTitle(block.lang, block.index, block.before),
    view: blockView(block.lang, block.code),
  }
}

// The previewable blocks of the assistant's messages.
export function previewableBlocks(messages) {
  const out = []
  for (const m of messages || []) {
    if (m.role !== 'assistant' || !m.text) continue
    for (const b of codeBlocksOf(m.text)) if (isPreviewableBlock(b)) out.push(blockItem(m, b))
  }
  return out
}

// The files a turn created or changed, for the chips under its answer:
// { path, name, size, kind: 'created' | 'changed' }, created first.
export function turnFiles(files, turn) {
  if (!turn) return []
  const mine = (files || []).filter(f => f.origin !== 'user' && f.turn === turn)
  return [...mine.filter(f => !f.changed), ...mine.filter(f => f.changed)]
    .map(f => ({ path: f.path, name: f.name, size: f.size, kind: f.changed ? 'changed' : 'created' }))
}

// What should open the pane now: the newest file that the assistant created or
// changed after the watermark, else the newest previewable block of a message
// after it. since is { turn, seq }: what the chat had when it was opened, so a
// reload or an old chat opens nothing. null when nothing new qualifies.
export function autoOpenTarget({ files = [], messages = [], since }) {
  if (!since) return null
  const fresh = files.filter(f => f.origin !== 'user' && (f.turn || 0) > (since.turn || 0))
  if (fresh.length > 0) {
    const newest = fresh.reduce((a, b) => {
      if ((b.turn || 0) !== (a.turn || 0)) return (b.turn || 0) > (a.turn || 0) ? b : a
      return String(b.at || '') >= String(a.at || '') ? b : a
    })
    return { type: 'file', path: newest.path, name: newest.name }
  }
  const blocks = previewableBlocks(messages.filter(m => (m.seq || 0) > (since.seq || 0)))
  if (blocks.length === 0) return null
  const b = blocks[blocks.length - 1]
  return { type: 'block', id: b.id, name: b.name }
}

// The key of a target, to open each one once.
export function targetKey(t) {
  if (!t) return ''
  return t.type === 'file' ? `file:${t.path}` : `block:${t.id}`
}

// A phone gets the chip only: the pane would cover the conversation.
export const PHONE_MAX_WIDTH = 639
export function isPhoneWidth(width) {
  return typeof width === 'number' && width <= PHONE_MAX_WIDTH
}

// The chats where the person closed the pane, in the browser storage per
// account: { [chatId]: at }. At most MAX_CLOSED are kept, the oldest go first.
const CLOSED_PREFIX = 'localai_chat_preview_closed'
export const MAX_CLOSED = 200

export function closedKey(userId) {
  return userId ? `${CLOSED_PREFIX}:${userId}` : CLOSED_PREFIX
}

export function readClosed(storage, key) {
  let raw
  try { raw = storage?.getItem(key) } catch { return {} }
  if (!raw) return {}
  let parsed
  try { parsed = JSON.parse(raw) } catch { return {} }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
  const out = {}
  for (const [id, at] of Object.entries(parsed)) if (id && Number.isFinite(Number(at))) out[id] = Number(at)
  return out
}

export function isClosed(storage, key, chatId) {
  return !!chatId && chatId in readClosed(storage, key)
}

// Marks a chat closed (closed true) or open again (false). A chat with no id
// (a new chat) is never written.
export function setClosed(storage, key, chatId, closed, now = Date.now()) {
  if (!storage || !chatId) return
  const all = readClosed(storage, key)
  if (closed) all[chatId] = now
  else if (chatId in all) delete all[chatId]
  else return
  const ids = Object.keys(all)
  if (ids.length > MAX_CLOSED) {
    ids.sort((a, b) => all[b] - all[a])
    for (const old of ids.slice(MAX_CLOSED)) delete all[old]
  }
  try {
    if (Object.keys(all).length === 0) storage.removeItem(key)
    else storage.setItem(key, JSON.stringify(all))
  } catch { /* private mode or full: the choice lasts for this page only */ }
}
