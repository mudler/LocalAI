// Scopes an add-on style sheet to the add-on's own root element. The sheet is
// read as text and rebuilt rule by rule, so no selector of an add-on can reach
// the host or another add-on:
//
//   .title { }        ->  :where([data-addon="x"]) .title { }
//   :root, html, body ->  the add-on root itself
//   @media, @supports, @container, @layer   rewritten inside
//   @keyframes        renamed per add-on, and animation names follow
//   @import, @font-face, @namespace, @charset, @page   dropped
//   declarations that load a URL or run script, in any rule, keyframe or
//   nested block (url(, src(, image-set(, cross-fade(, element(, paint(,
//   image(, expression(, behavior, -moz-binding, any escape outside a
//   string; data: images included)   dropped
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

// unescape resolves CSS escapes (a backslash and up to six hex digits with one
// optional space, or a backslash and any other character) and removes
// comments, so a function name cannot hide behind u\72l( or url/**/(.
function unescapeCss(text) {
  let out = ''
  for (let i = 0; i < text.length; i++) {
    const ch = text[i]
    if (ch === '/' && text[i + 1] === '*') { i = skipComment(text, i); continue }
    if (ch !== '\\') { out += ch; continue }
    const hex = /^[0-9a-fA-F]{1,6}/.exec(text.slice(i + 1, i + 7))
    if (hex) {
      const code = parseInt(hex[0], 16)
      out += code > 0 && code <= 0x10ffff ? String.fromCodePoint(code) : '�'
      i += hex[0].length
      if (/\s/.test(text[i + 1] || '')) i++
    } else if (i + 1 < text.length) {
      out += text[++i]
    }
  }
  return out
}

// hasEscapeOutsideString reports a backslash that is not inside a quoted
// string. Inside a string an escape is only text; anywhere else it can spell
// an identifier or a function name.
function hasEscapeOutsideString(text) {
  for (let i = 0; i < text.length; i++) {
    const ch = text[i]
    if (ch === '"' || ch === '\'') { i = skipString(text, i); continue }
    if (ch === '/' && text[i + 1] === '*') { i = skipComment(text, i); continue }
    if (ch === '\\') return true
  }
  return false
}

// Functions and values that fetch a resource or run script. The check is a
// conservative substring test on the unescaped text, so an unknown wrapper
// such as url2( or -webkit-image-set( is caught as well. Data: images are
// dropped too: telling a safe one from a URL needs a full value parser.
const LOADING = /(url|src|image-set|cross-fade|element|paint|image|expression)\s*\(/i
const LOADING_VALUE = /behavior|-moz-binding|@import/i
const LOADING_PROP = /^(?:-[a-z]+-)?(?:behavior|binding)$|-moz-binding$/i

// loadsResource reports whether a declaration, or an at-rule prelude, could
// load something.
function loadsResource(text, prop) {
  if (hasEscapeOutsideString(text)) return true
  const plain = unescapeCss(text)
  if (LOADING.test(plain) || LOADING_VALUE.test(prop === null ? plain : plain.slice(plain.indexOf(':') + 1))) return true
  return prop !== null && LOADING_PROP.test(unescapeCss(prop).trim())
}

const ALLOWED_BLOCK_AT_RULES = new Set(['media', 'supports', 'container', 'layer'])

// cleanBlock rebuilds the inside of a declaration block, a keyframes body or
// a nested at-rule. It is applied to every block, so a keyframe step, a nested
// rule and a rule inside @media are held to the same rule as a top-level rule.
function cleanBlock(body, ctx) {
  let out = ''
  // A declaration ends with a semicolon; a nested block needs none.
  const decl = (text) => { out += (out && !out.endsWith('}') ? ';' : '') + text }
  for (const item of rules(stripComments(body))) {
    const { prelude, body: inner } = item
    if (inner === null) {
      for (const piece of splitTop(prelude, ';')) {
        const text = piece.trim()
        if (!text) continue
        for (const d of cleanDeclaration(text, ctx)) decl(d)
      }
      continue
    }
    if (prelude.startsWith('@')) {
      const name = (prelude.match(/^@([A-Za-z-]+)/) || [, ''])[1].toLowerCase()
      if (!ALLOWED_BLOCK_AT_RULES.has(name) || loadsResource(prelude, null)) {
        ctx.dropped.push({ kind: name || 'at-rule', detail: prelude.slice(0, 60) })
        continue
      }
      decl(`${prelude}{${cleanBlock(inner, ctx)}}`)
      continue
    }
    if (loadsResource(prelude, null)) {
      ctx.dropped.push({ kind: 'url', detail: prelude.slice(0, 60) })
      continue
    }
    decl(`${prelude}{${cleanBlock(inner, ctx)}}`)
  }
  return out
}

function cleanDeclaration(text, ctx) {
  const colon = text.indexOf(':')
  if (colon < 0) {
    if (loadsResource(text, null)) { ctx.dropped.push({ kind: 'url', detail: text.slice(0, 60) }); return [] }
    return [text]
  }
  const prop = text.slice(0, colon)
  const value = text.slice(colon + 1)
  if (loadsResource(text, prop)) {
    ctx.dropped.push({ kind: /^\s*@import\b/i.test(value) ? 'import' : 'url', detail: text.slice(0, 60) })
    return []
  }
  const lower = prop.trim().toLowerCase()
  if ((lower === 'animation' || lower === 'animation-name' || lower === '-webkit-animation' || lower === '-webkit-animation-name') && ctx.renames.size) {
    const next = value.replace(/-?[A-Za-z_][A-Za-z0-9_-]*/g, (name) => ctx.renames.get(name) || name)
    return [`${prop}:${next}`]
  }
  return [text]
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
      if (loadsResource(prelude, null)) {
        ctx.dropped.push({ kind: 'url', detail: prelude.slice(0, 60) })
        continue
      }
      if (name === 'media' || name === 'supports' || name === 'container' || name === 'layer') {
        out.push(`${prelude}{${transform(body, ctx)}}`)
      } else if (name === 'keyframes' || name === '-webkit-keyframes') {
        const original = prelude.replace(/^@[A-Za-z-]+\s*/, '').trim()
        const renamed = ctx.renames.get(original) || `${ctx.prefix}${original}`
        ctx.renames.set(original, renamed)
        out.push(`@${name} ${renamed}{${cleanBlock(body, ctx)}}`)
      } else {
        ctx.dropped.push({ kind: name, detail: prelude.slice(0, 60) })
      }
      continue
    }
    if (body === null) continue
    const selectors = splitTop(prelude, ',').map(sel => scopeSelector(sel, ctx.scope)).filter(Boolean)
    if (selectors.length === 0) continue
    out.push(`${selectors.join(',')}{${cleanBlock(body, ctx)}}`)
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
