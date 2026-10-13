// Scopes an add-on style sheet to the add-on's own root element. The sheet is
// read as text and rebuilt rule by rule, so no selector of an add-on can reach
// the host or another add-on:
//
//   .title { }        ->  :where([data-addon="x"]) .title { }
//   :root, html, body ->  the add-on root itself
//   @media, @supports, @container, @layer   rewritten inside
//   @keyframes        renamed per add-on, and animation names follow
//   @import, @font-face, @namespace, @charset, @page   dropped
//   declarations that load a URL (other than data: images)   dropped
//
// The scope uses :where(), so it adds no specificity: an add-on's own cascade
// is unchanged inside its root.

export function scopeSelectorRoot(addonId) {
  return `:where([data-addon="${String(addonId).replace(/[^a-z0-9-]/g, '')}"])`
}

// readBlock returns the index just after the matching close brace for the open
// brace at `start`, skipping strings, comments and nested blocks.
function skipBlock(text, start) {
  let depth = 0
  for (let i = start; i < text.length; i++) {
    const ch = text[i]
    if (ch === '"' || ch === '\'') { i = skipString(text, i); continue }
    if (ch === '/' && text[i + 1] === '*') { i = skipComment(text, i); continue }
    if (ch === '\\') { i++; continue }
    if (ch === '{') depth++
    else if (ch === '}') { depth--; if (depth === 0) return i + 1 }
  }
  return text.length
}

function skipString(text, i) {
  const quote = text[i]
  for (let j = i + 1; j < text.length; j++) {
    if (text[j] === '\\') { j++; continue }
    if (text[j] === quote) return j
  }
  return text.length
}

function skipComment(text, i) {
  const end = text.indexOf('*/', i + 2)
  return end < 0 ? text.length : end + 1
}

// rules splits a sheet into top-level { prelude, body } items. A statement
// at-rule (no block) has body === null.
function rules(text) {
  const out = []
  let i = 0
  let start = 0
  const n = text.length
  while (i < n) {
    const ch = text[i]
    if (ch === '"' || ch === '\'') { i = skipString(text, i) + 1; continue }
    if (ch === '/' && text[i + 1] === '*') { i = skipComment(text, i) + 1; continue }
    if (ch === '\\') { i += 2; continue }
    if (ch === ';') {
      const prelude = text.slice(start, i).trim()
      if (prelude) out.push({ prelude, body: null })
      i++; start = i; continue
    }
    if (ch === '{') {
      const end = skipBlock(text, i)
      out.push({ prelude: text.slice(start, i).trim(), body: text.slice(i + 1, end - 1) })
      i = end; start = i; continue
    }
    i++
  }
  const rest = text.slice(start).trim()
  if (rest && !/^\/\*[\s\S]*\*\/$/.test(rest)) out.push({ prelude: rest, body: null })
  return out
}

function stripComments(text) {
  let out = ''
  for (let i = 0; i < text.length; i++) {
    const ch = text[i]
    if (ch === '"' || ch === '\'') { const e = skipString(text, i); out += text.slice(i, e + 1); i = e; continue }
    if (ch === '/' && text[i + 1] === '*') { i = skipComment(text, i); continue }
    out += ch
  }
  return out
}

// splitTop splits on a separator at paren, bracket and string depth zero.
function splitTop(text, sep) {
  const parts = []
  let depth = 0
  let last = 0
  for (let i = 0; i < text.length; i++) {
    const ch = text[i]
    if (ch === '"' || ch === '\'') { i = skipString(text, i); continue }
    if (ch === '\\') { i++; continue }
    if (ch === '(' || ch === '[') depth++
    else if (ch === ')' || ch === ']') depth--
    else if (ch === sep && depth === 0) { parts.push(text.slice(last, i)); last = i + 1 }
  }
  parts.push(text.slice(last))
  return parts
}

const GLOBAL_ROOT = /^(?::root|html|body)(?![A-Za-z0-9_-])/i

function scopeSelector(selector, scope) {
  const s = selector.trim()
  if (!s) return ''
  if (GLOBAL_ROOT.test(s)) {
    const rest = s.replace(GLOBAL_ROOT, '').trim()
    return rest ? `${scope} ${rest}` : scope
  }
  // `:host`, `:scope` and `&` mean nothing here; scope them as plain selectors.
  return `${scope} ${s}`
}

const URL_FN = /url\s*\(/i
const DATA_IMAGE = /^url\(\s*(["']?)data:image\/(png|jpe?g|gif|webp|avif|svg\+xml)[;,]/i

// declarations drops every declaration that would load a resource, so a sheet
// cannot reach another origin or an undeclared package file.
function cleanDeclarations(body, dropped, renames) {
  const kept = []
  for (const decl of splitTop(stripComments(body), ';')) {
    const text = decl.trim()
    if (!text) continue
    const colon = text.indexOf(':')
    if (colon < 0) { kept.push(text); continue }
    const value = text.slice(colon + 1)
    if (URL_FN.test(value)) {
      const urls = value.match(/url\s*\([^)]*\)/gi) || []
      if (!urls.every(u => DATA_IMAGE.test(u.replace(/\s+/g, ' ')))) {
        dropped.push({ kind: 'url', detail: text.slice(0, 60) })
        continue
      }
    }
    if (/^\s*@import\b/i.test(value)) { dropped.push({ kind: 'import', detail: text.slice(0, 60) }); continue }
    const prop = text.slice(0, colon).trim().toLowerCase()
    if ((prop === 'animation' || prop === 'animation-name' || prop === '-webkit-animation' || prop === '-webkit-animation-name') && renames.size) {
      const next = value.replace(/-?[A-Za-z_][A-Za-z0-9_-]*/g, (name) => renames.get(name) || name)
      kept.push(`${text.slice(0, colon)}:${next}`)
      continue
    }
    kept.push(text)
  }
  return kept.join(';')
}

function transform(text, ctx) {
  const items = rules(text)
  const out = []
  for (const item of items) {
    const { prelude, body } = item
    if (prelude.startsWith('@')) {
      const name = (prelude.match(/^@([A-Za-z-]+)/) || [, ''])[1].toLowerCase()
      if (body === null) {
        ctx.dropped.push({ kind: name || 'at-rule', detail: prelude.slice(0, 60) })
        continue
      }
      if (name === 'media' || name === 'supports' || name === 'container') {
        out.push(`${prelude}{${transform(body, ctx)}}`)
      } else if (name === 'layer') {
        out.push(`${prelude}{${transform(body, ctx)}}`)
      } else if (name === 'keyframes' || name === '-webkit-keyframes') {
        const original = prelude.replace(/^@[A-Za-z-]+\s*/, '').trim()
        const renamed = ctx.renames.get(original) || `${ctx.prefix}${original}`
        ctx.renames.set(original, renamed)
        out.push(`@${name} ${renamed}{${body}}`)
      } else {
        ctx.dropped.push({ kind: name, detail: prelude.slice(0, 60) })
      }
      continue
    }
    if (body === null) continue
    const selectors = splitTop(prelude, ',').map(sel => scopeSelector(sel, ctx.scope)).filter(Boolean)
    if (selectors.length === 0) continue
    out.push(`${selectors.join(',')}{${cleanDeclarations(body, ctx.dropped, ctx.renames)}}`)
  }
  return out.join('\n')
}

// scopeCss returns { css, dropped }. `dropped` lists what was removed, for the
// console and the tests; it is never an error.
export function scopeCss(text, addonId, { maxBytes = 256 * 1024 } = {}) {
  const dropped = []
  const source = String(text || '')
  if (source.length > maxBytes) return { css: '', dropped: [{ kind: 'size', detail: String(source.length) }] }
  const id = String(addonId).replace(/[^a-z0-9-]/g, '')
  const ctx = { scope: scopeSelectorRoot(id), prefix: `ad-${id}-`, dropped, renames: new Map() }
  // Keyframe names are collected first so animation declarations that appear
  // before their @keyframes still follow the rename.
  for (const item of rules(stripComments(source))) {
    if (/^@(-webkit-)?keyframes\b/i.test(item.prelude) && item.body !== null) {
      const original = item.prelude.replace(/^@[A-Za-z-]+\s*/, '').trim()
      ctx.renames.set(original, `${ctx.prefix}${original}`)
    }
  }
  const css = transform(stripComments(source), ctx)
  return { css, dropped }
}
