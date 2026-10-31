// The files next to a page, put into the page before it runs. A page runs from
// a srcdoc with no network, so <script src="app.js">, <link rel="stylesheet"
// href="style.css">, <img src="logo.png">, url() in CSS and import './x.js'
// would all fail. The host reads those files through the same pages API as the
// bridge, with the viewer's rights, and writes them into the document: scripts
// and styles as inline elements, images as data: URLs, ES modules as blob:
// URLs behind an import map. Only files of the page's own folder are read; a
// remote URL stays as it is and the policy of the frame blocks it, as before.
// No React and no network here: the caller passes the reads.

export const MAX_INLINE_TOTAL = 4 << 20 // characters of code, style and data URLs
export const MAX_INLINE_IMAGES = 2 << 20 // bytes of images
const MAX_PATH = 256
const MAX_DEPTH = 8
const MAX_CSS_DEPTH = 8
const READS_AT_ONCE = 4

// The prefix of the import map keys of a page's modules: a bare specifier, so
// it never resolves against the frame's base URL.
export const MODULE_KEY = '@localai-page/'

const IMAGE_TYPES = {
  png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif', webp: 'image/webp',
  avif: 'image/avif', bmp: 'image/bmp', ico: 'image/x-icon', svg: 'image/svg+xml',
}
const FONT_EXT = /\.(woff2?|ttf|otf|eot)$/i
const JS_TYPES = /^(|module|text\/javascript|application\/javascript|text\/ecmascript|application\/ecmascript)$/

const extOf = (p) => (/\.([a-z0-9]+)$/i.exec(p)?.[1] || '').toLowerCase()
export const isImagePath = (p) => Object.hasOwn(IMAGE_TYPES, extOf(p))
const dirOf = (p) => (p.includes('/') ? p.slice(0, p.lastIndexOf('/')) : '')

// resolvePagePath reads a URL that a page names, relative to the folder from
// (a folder of the page, '' for the page's own), and says what it is:
//
//   {path}            a file of the page's folder, normalised
//   {skip: 'remote'}  another scheme (https:, data:, blob:, ...) or //host
//   {skip: 'empty'}   empty, or only a query or a fragment
//   {error: 'absolute' | 'outside' | 'bad'}
//
// The query and the fragment are dropped, %XX is decoded once (as a browser
// does, so %2e%2e is ..), and a path that climbs out of the folder, has a
// backslash, a colon, a control character, an empty segment or is too long is
// refused. The server checks every path again with the viewer's rights.
export function resolvePagePath(ref, from = '') {
  if (typeof ref !== 'string') return { error: 'bad' }
  let s = ref.trim()
  if (s === '' || s[0] === '#' || s[0] === '?') return { skip: 'empty' }
  if (/^[a-z][a-z0-9+.-]*:/i.test(s)) return { skip: 'remote' }
  if (/^[/\\]{2}/.test(s)) return { skip: 'remote' }
  if (s[0] === '/' || s[0] === '\\') return { error: 'absolute' }
  s = s.replace(/[?#][\s\S]*$/, '')
  let dec
  try {
    dec = decodeURIComponent(s)
  } catch {
    return { error: 'bad' }
  }
  if (/[\\:\u0000-\u001f\u007f]/.test(dec)) return { error: 'bad' }
  if (dec[0] === '/') return { error: 'absolute' }
  const out = from ? from.split('/') : []
  const segs = dec.split('/')
  for (let i = 0; i < segs.length; i++) {
    const seg = segs[i]
    if (seg === '') return { error: 'bad' }
    if (seg === '.') continue
    if (seg === '..') {
      if (out.length === 0) return { error: 'outside' }
      out.pop()
      continue
    }
    out.push(seg)
  }
  const path = out.join('/')
  if (path === '' || path.length > MAX_PATH || out.length > MAX_DEPTH) return { error: 'bad' }
  return { path }
}

// --- A small HTML tokenizer -------------------------------------------------
// It finds the start tags of the markup with their attributes and the text of
// the raw text elements, and keeps the offsets, so the page's markup is
// changed only where a file goes in. Comments, doctypes and end tags are
// skipped; the content of script, style and the other raw text elements is
// never read as markup.

const RAW_TEXT = new Set(['script', 'style', 'textarea', 'title', 'xmp', 'iframe', 'noembed', 'noframes', 'noscript'])
const ENTITIES = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: '\u00a0' }

function decodeEntities(s) {
  return s.replace(/&(#x[0-9a-f]+|#[0-9]+|[a-z]+);?/gi, (m, e) => {
    if (e[0] === '#') {
      const n = e[1] === 'x' || e[1] === 'X' ? parseInt(e.slice(2), 16) : parseInt(e.slice(1), 10)
      return n > 0 && n <= 0x10ffff ? String.fromCodePoint(n) : m
    }
    return ENTITIES[e.toLowerCase()] ?? m
  })
}

const attrText = (v) => `"${String(v).replace(/&/g, '&amp;').replace(/"/g, '&quot;')}"`
const isSpace = (c) => c === ' ' || c === '\n' || c === '\t' || c === '\r' || c === '\f'

// scanTags returns the start tags of html: {name, start, end, attrs, inner,
// close}. attrs are {name, value, vs, ve} with the raw value's offsets (vs is
// -1 for an attribute without a value); inner is the {start, end} of a raw
// text element's text and close the offset after its end tag.
export function scanTags(html) {
  const text = typeof html === 'string' ? html : ''
  const n = text.length
  const tags = []
  let i = 0
  while (i < n) {
    const lt = text.indexOf('<', i)
    if (lt < 0) break
    if (text.startsWith('<!--', lt)) {
      const e = text.indexOf('-->', lt + 4)
      i = e < 0 ? n : e + 3
      continue
    }
    const c = text[lt + 1] || ''
    if (c === '!' || c === '?' || c === '/') {
      const e = text.indexOf('>', lt + 2)
      i = e < 0 ? n : e + 1
      continue
    }
    if (!/[a-z]/i.test(c)) {
      i = lt + 1
      continue
    }
    let j = lt + 1
    while (j < n && !isSpace(text[j]) && text[j] !== '/' && text[j] !== '>') j++
    const name = text.slice(lt + 1, j).toLowerCase()
    const attrs = []
    while (j < n) {
      while (j < n && (isSpace(text[j]) || text[j] === '/')) j++
      if (j >= n) break
      if (text[j] === '>') { j++; break }
      let k = j + 1
      while (k < n && !isSpace(text[k]) && text[k] !== '/' && text[k] !== '>' && text[k] !== '=') k++
      const an = text.slice(j, k).toLowerCase()
      j = k
      while (j < n && isSpace(text[j])) j++
      if (text[j] !== '=') {
        attrs.push({ name: an, value: '', vs: -1, ve: -1 })
        continue
      }
      j++
      while (j < n && isSpace(text[j])) j++
      const q = text[j]
      if (q === '"' || q === "'") {
        const e = text.indexOf(q, j + 1)
        const end = e < 0 ? n : e
        attrs.push({ name: an, value: decodeEntities(text.slice(j + 1, end)), vs: j, ve: Math.min(n, end + 1) })
        j = Math.min(n, end + 1)
      } else {
        let e = j
        while (e < n && !isSpace(text[e]) && text[e] !== '>') e++
        attrs.push({ name: an, value: decodeEntities(text.slice(j, e)), vs: j, ve: e })
        j = e
      }
    }
    const tag = { name, start: lt, end: j, attrs }
    if (RAW_TEXT.has(name)) {
      const re = new RegExp(`</${name}(?=[\\s/>]|$)`, 'ig')
      re.lastIndex = j
      const m = re.exec(text)
      const innerEnd = m ? m.index : n
      tag.inner = { start: j, end: innerEnd }
      const gt = m ? text.indexOf('>', innerEnd) : -1
      tag.close = gt < 0 ? n : gt + 1
      i = tag.close
    } else {
      i = j
    }
    tags.push(tag)
  }
  return tags
}

const attrOf = (tag, name) => tag.attrs.find((a) => a.name === name)
const hasAttr = (tag, name) => Boolean(attrOf(tag, name))
const relTokens = (tag) => (attrOf(tag, 'rel')?.value || '').toLowerCase().split(/\s+/).filter(Boolean)

// --- Modules ----------------------------------------------------------------

// The static and dynamic import specifiers of a module: import x from './a.js',
// import './a.js', export {x} from './a.js', export * from './a.js' and
// import('./a.js') with a string literal. A computed import() is left alone.
const STATIC_IMPORT = /(\bimport\s*(?:[\w$*{}\s,]+?\s*from\s*)?|\bexport\s*(?:\*\s*(?:as\s+[\w$]+\s*)?|\{[^}]*\}\s*)from\s*)(['"])([^'"\n\r]+)\2/g
const DYNAMIC_IMPORT = /(\bimport\s*\(\s*)(['"`])([^'"`$\n\r]+)\2(\s*\))/g

function rewriteImports(code, dir, note) {
  const deps = []
  const map = (spec, where) => {
    if (!/^\.{1,2}\//.test(spec) && !/^([a-z][a-z0-9+.-]*:|\/)/i.test(spec)) {
      note('error', spec, `the module ${spec} (${where}) is a package name; a page has no packages: put the module in a file next to the page and import it as './${spec}.js'`)
      return null
    }
    const r = resolvePagePath(spec, dir)
    if (r.path) {
      deps.push(r.path)
      return MODULE_KEY + r.path
    }
    if (r.skip === 'remote') return null
    if (r.error === 'outside' || r.error === 'absolute') note('error', spec, `the module ${spec} (${where}) is outside the page's folder and cannot load`)
    else note('error', spec, `the module ${spec} (${where}) is not a valid path`)
    return null
  }
  const out = code
    .replace(STATIC_IMPORT, (m, head, q, spec) => {
      const key = map(spec, 'import')
      return key ? `${head}${q}${key}${q}` : m
    })
    .replace(DYNAMIC_IMPORT, (m, head, q, spec, tail) => {
      const key = map(spec, 'import()')
      return key ? `${head}${q}${key}${q}${tail}` : m
    })
  return { code: out, deps }
}

// --- CSS ----------------------------------------------------------------------

const CSS_IMPORT = /@import\s+(?:url\(\s*(?:"([^"]*)"|'([^']*)'|([^)\s]*))\s*\)|"([^"]*)"|'([^']*)')\s*([^;]*);/gi
const CSS_URL = /url\(\s*(?:"([^"]*)"|'([^']*)'|([^)'"\s]*))\s*\)/gi

async function replaceAsync(text, re, fn) {
  const parts = []
  let last = 0
  re.lastIndex = 0
  for (let m = re.exec(text); m; m = re.exec(text)) {
    parts.push(text.slice(last, m.index), fn(...m))
    last = m.index + m[0].length
    if (m[0] === '') re.lastIndex++
  }
  parts.push(text.slice(last))
  return (await Promise.all(parts)).join('')
}

// --- The inliner ----------------------------------------------------------------

// inlinePageAssets puts the files that html names into it. reads is
// {read(path) -> {content}, readImage(path) -> {content (base64), media_type}};
// a read that fails throws an error with its HTTP status. allowText(path),
// when given, says whether a text file may go in: a shared page passes the
// files of code that its consent covers (code_files of the server), so a file
// the server did not count never runs under that consent. It resolves to
//
//   html     the page with the files in it
//   head     markup for the head, after the bridge: the loader of the modules
//   modules  true when the page has modules: the frame's policy then allows blob:
//   deps     the files that went in, to build the page again when one changes
//   notes    [{level: 'error' | 'warn', path, message}] for the console; an
//            error also shows in the page
//   tooLarge true when the files were more than the budget and some were left out
export async function inlinePageAssets(html, reads, { maxTotal = MAX_INLINE_TOTAL, maxImages = MAX_INLINE_IMAGES, allowText = null } = {}) {
  const text = typeof html === 'string' ? html : ''
  const tags = scanTags(text)
  const notes = []
  const deps = new Set()
  const seenNotes = new Set()
  let total = 0
  let imageBytes = 0
  let tooLarge = false

  const note = (level, path, message) => {
    const key = `${level}:${message}`
    if (seenNotes.has(key)) return
    seenNotes.add(key)
    notes.push({ level, path, message })
  }

  // Reads, at most a few at once, each file once.
  const cache = new Map()
  let active = 0
  const waiting = []
  const slot = () => new Promise((resolve) => {
    if (active < READS_AT_ONCE) { active++; resolve() } else waiting.push(resolve)
  })
  const release = () => {
    const next = waiting.shift()
    if (next) next()
    else active--
  }
  const fetchOnce = (kind, path) => {
    const key = `${kind}:${path}`
    if (!cache.has(key)) {
      cache.set(key, (async () => {
        await slot()
        try {
          return { file: await (kind === 'image' ? reads.readImage(path) : reads.read(path)) }
        } catch (e) {
          return { error: e }
        } finally {
          release()
        }
      })())
    }
    return cache.get(key)
  }

  const why = (e) => {
    switch (e?.status) {
      case 404: return 'is not in the page\'s folder'
      case 413: return 'is larger than 1 MiB'
      case 415: return 'is not a UTF-8 text file'
      case 400: return 'is not a valid path inside the page\'s folder'
      default: return `could not be read: ${e?.message || 'error'}`
    }
  }

  const budget = (size, path) => {
    if (total + size > maxTotal) {
      tooLarge = true
      note('error', path, `${path} was left out: the files next to this page are larger than ${Math.round(maxTotal / (1 << 20))} MiB in all. Load data with fetch() instead of putting it in the page`)
      return false
    }
    total += size
    return true
  }

  // A text file for code or style. null when it cannot go in; the reason is a
  // note (an error: the page will not work without it).
  async function textFile(path, what) {
    if (allowText && !allowText(path)) {
      note('error', path, `the ${what} ${path} did not load: it is not one of the files of code that you allowed for this shared page`)
      return null
    }
    const got = await fetchOnce('text', path)
    if (got.error) {
      note('error', path, `the ${what} ${path} ${why(got.error)}, so it did not load`)
      return null
    }
    const content = String(got.file?.content ?? '')
    if (!budget(content.length, path)) return null
    deps.add(path)
    return content
  }

  // A data: URL of an image, or null with a warning.
  async function imageURL(ref, dir, where) {
    const r = resolvePagePath(ref, dir)
    if (!r.path) {
      if (r.error) note('warn', ref, `the image ${ref} (${where}) is outside the page's folder and did not load`)
      return null
    }
    if (!isImagePath(r.path)) {
      if (FONT_EXT.test(r.path)) note('warn', r.path, `the font ${r.path} did not load: fonts next to a page are not loaded; use a system font or a data: URL`)
      else note('warn', r.path, `${r.path} (${where}) did not load: only images (png, jpg, gif, webp, avif, bmp, ico, svg) go into a page from its folder`)
      return null
    }
    const got = await fetchOnce('image', r.path)
    if (got.error) {
      note('warn', r.path, `the image ${r.path} ${why(got.error)}, so it did not load`)
      return null
    }
    const b64 = String(got.file?.content ?? '')
    const bytes = Math.floor((b64.length * 3) / 4)
    if (imageBytes + bytes > maxImages) {
      note('warn', r.path, `the image ${r.path} did not load: the images of a page are at most ${Math.round(maxImages / (1 << 20))} MiB in all`)
      return null
    }
    const url = `data:${got.file?.media_type || IMAGE_TYPES[extOf(r.path)]};base64,${b64}`
    if (!budget(url.length, r.path)) return null
    imageBytes += bytes
    deps.add(r.path)
    return url
  }

  // CSS with its relative @import and url() put in, relative to dir.
  async function css(source, dir, depth, chain) {
    const imports = []
    const marked = source.replace(CSS_IMPORT, (m, a, b, c, d, e, media) => {
      imports.push({ ref: a ?? b ?? c ?? d ?? e ?? '', media: media.trim(), raw: m })
      return `\u0000import${imports.length - 1}\u0000`
    })
    let out = await replaceAsync(marked, CSS_URL, async (m, a, b, c) => {
      const ref = a ?? b ?? c ?? ''
      const r = resolvePagePath(ref, dir)
      if (r.skip) return m
      const url = await imageURL(ref, dir, 'CSS url()')
      return url ? `url("${url}")` : m
    })
    const parts = await Promise.all(imports.map(async ({ ref, media, raw }) => {
      const r = resolvePagePath(ref, dir)
      if (r.skip) return raw
      if (!r.path) {
        note('error', ref, `the style sheet ${ref} (@import) is outside the page's folder and did not load`)
        return ''
      }
      if (depth >= MAX_CSS_DEPTH || chain.includes(r.path)) return ''
      const inner = await textFile(r.path, 'style sheet')
      if (inner === null) return ''
      const done = await css(inner, dirOf(r.path), depth + 1, [...chain, r.path])
      return media ? `@media ${media} {\n${done}\n}` : done
    }))
      out = out.replace(/\u0000import(\d+)\u0000/g, (m, n) => parts[Number(n)])
    return out
  }
  const styleText = (s) => s.replace(/<\/(style)/gi, '<\\/$1')

  // The modules: path -> code, with relative specifiers turned into map keys.
  const modules = new Map()
  async function loadModule(path) {
    if (modules.has(path)) return
    modules.set(path, '')
    const source = await textFile(path, 'module')
    if (source === null) {
      modules.set(path, `throw new Error(${JSON.stringify(`${path} did not load into the page; see the notes above`)})`)
      return
    }
    const { code, deps: next } = rewriteImports(source, dirOf(path), note)
    modules.set(path, `${code}\n//# sourceURL=${path}`)
    for (const d of next) await loadModule(d)
  }

  // Warm the cache with the files the markup names, in order.
  for (const tag of tags) {
    const ref = tag.name === 'script' ? attrOf(tag, 'src')?.value
      : tag.name === 'link' && relTokens(tag).includes('stylesheet') ? attrOf(tag, 'href')?.value
        : null
    const r = ref ? resolvePagePath(ref) : null
    if (r?.path && (!allowText || allowText(r.path))) fetchOnce('text', r.path)
  }

  const edits = [] // {start, end, text}
  const tail = [] // deferred classic scripts, before </body>
  let usesModules = false
  const classicLoader = (code, path) => `<script>(function(c){var s=document.createElement('script');s.textContent=c;document.currentScript.after(s)})(${scriptJSON(`${code}\n//# sourceURL=${path}`)})</script>`

  for (const tag of tags) {
    if (tag.name === 'script') {
      const type = (attrOf(tag, 'type')?.value || '').trim().toLowerCase()
      if (!JS_TYPES.test(type)) continue
      const src = attrOf(tag, 'src')
      if (type === 'module') {
        if (src) {
          const r = resolvePagePath(src.value)
          if (r.skip) continue
          if (!r.path) {
            note('error', src.value, `the module ${src.value} is outside the page's folder and cannot load`)
            continue
          }
          usesModules = true
          await loadModule(r.path)
          edits.push({ start: tag.start, end: tag.close, text: `<script type="module">import ${JSON.stringify(MODULE_KEY + r.path)}</script>` })
        } else if (tag.inner) {
          const source = text.slice(tag.inner.start, tag.inner.end)
          const { code, deps: next } = rewriteImports(source, '', note)
          if (next.length === 0) continue
          usesModules = true
          for (const d of next) await loadModule(d)
          edits.push({ start: tag.inner.start, end: tag.inner.end, text: code.replace(/<\/(script)/gi, '<\\/$1') })
        }
        continue
      }
      if (!src) continue
      const r = resolvePagePath(src.value)
      if (r.skip) continue
      if (!r.path) {
        note('error', src.value, `the script ${src.value} is outside the page's folder and cannot load`)
        continue
      }
      const code = await textFile(r.path, 'script')
      if (code === null) continue
      const loader = classicLoader(code, r.path)
      // A deferred or async script runs after the markup is parsed: it goes to
      // the end of the body, in order.
      if (hasAttr(tag, 'defer') || hasAttr(tag, 'async')) {
        tail.push(loader)
        edits.push({ start: tag.start, end: tag.close, text: '' })
      } else {
        edits.push({ start: tag.start, end: tag.close, text: loader })
      }
      continue
    }

    if (tag.name === 'link') {
      const rels = relTokens(tag)
      if (!rels.includes('stylesheet') || rels.includes('alternate')) continue
      const href = attrOf(tag, 'href')
      if (!href) continue
      const r = resolvePagePath(href.value)
      if (r.skip) continue
      if (!r.path) {
        note('error', href.value, `the style sheet ${href.value} is outside the page's folder and cannot load`)
        continue
      }
      const source = await textFile(r.path, 'style sheet')
      if (source === null) continue
      const done = await css(source, dirOf(r.path), 1, [r.path])
      const media = attrOf(tag, 'media')?.value
      edits.push({ start: tag.start, end: tag.end, text: `<style${media ? ` media=${attrText(media)}` : ''}>${styleText(done)}</style>` })
      continue
    }

    if (tag.name === 'style' && tag.inner) {
      const source = text.slice(tag.inner.start, tag.inner.end)
      if (!/url\(|@import/i.test(source)) continue
      const done = await css(source, '', 0, [])
      if (done !== source) edits.push({ start: tag.inner.start, end: tag.inner.end, text: styleText(done) })
    }

    // Image attributes of this tag, and url() in its style attribute.
    const imageAttrs = tag.name === 'img' ? ['src']
      : tag.name === 'source' ? ['src']
        : tag.name === 'video' ? ['poster']
          : tag.name === 'input' && (attrOf(tag, 'type')?.value || '').toLowerCase() === 'image' ? ['src'] : []
    for (const a of tag.attrs) {
      if (a.vs < 0) continue
      if (imageAttrs.includes(a.name)) {
        const r = resolvePagePath(a.value)
        if (r.skip) continue
        if (tag.name === 'source' && r.path && !isImagePath(r.path)) continue // audio or video
        const url = await imageURL(a.value, '', `<${tag.name} ${a.name}>`)
        if (url) edits.push({ start: a.vs, end: a.ve, text: attrText(url) })
      } else if (a.name === 'style' && /url\(/i.test(a.value)) {
        const done = await css(a.value, '', 0, [])
        if (done !== a.value) edits.push({ start: a.vs, end: a.ve, text: attrText(done) })
      }
    }
  }

  if (tail.length) {
    const m = /<\/body\s*>(?![\s\S]*<\/body\s*>)/i.exec(text)
    const at = m ? m.index : text.length
    edits.push({ start: at, end: at, text: tail.join('') })
  }

  edits.sort((a, b) => b.start - a.start || b.end - a.end)
  let out = text
  for (const e of edits) out = out.slice(0, e.start) + e.text + out.slice(e.end)

  let head = ''
  if (usesModules && modules.size > 0) {
    const sources = {}
    for (const [path, code] of modules) sources[MODULE_KEY + path] = code
    head = `<script>(function(src){var map={imports:{}};for(var k in src)map.imports[k]=URL.createObjectURL(new Blob([src[k]],{type:'text/javascript'}));var s=document.createElement('script');s.type='importmap';s.textContent=JSON.stringify(map);document.currentScript.after(s)})(${scriptJSON(sources)})</script>`
  }

  return { html: out, head, modules: usesModules, deps: [...deps].sort(), notes, tooLarge }
}

// pageRefs says whether html names any file of its folder that the inliner
// would read, so a page without one is built at once, without a read.
export function pageRefs(html) {
  for (const tag of scanTags(html)) {
    if (tag.name === 'script') {
      const src = attrOf(tag, 'src')
      if (src && resolvePagePath(src.value).path) return true
      if (!src && tag.inner && (attrOf(tag, 'type')?.value || '').toLowerCase() === 'module') {
        const body = html.slice(tag.inner.start, tag.inner.end)
        STATIC_IMPORT.lastIndex = 0
        DYNAMIC_IMPORT.lastIndex = 0
        if (STATIC_IMPORT.test(body) || DYNAMIC_IMPORT.test(body)) return true
      }
      continue
    }
    if (tag.name === 'style' && tag.inner && /url\(|@import/i.test(html.slice(tag.inner.start, tag.inner.end))) return true
    for (const a of tag.attrs) {
      if (a.name === 'style' && /url\(/i.test(a.value)) return true
      if ((a.name === 'src' || a.name === 'href' || a.name === 'poster') && resolvePagePath(a.value).path) {
        if (tag.name === 'link' && !relTokens(tag).includes('stylesheet')) continue
        if (tag.name === 'a' || tag.name === 'iframe') continue
        return true
      }
    }
  }
  return false
}

// JSON that is safe inside a script element: no "<", and no line separators
// that older engines read as a line end.
export function scriptJSON(v) {
  return JSON.stringify(v).replace(/</g, '\\u003c').replace(/\u2028/g, '\\u2028').replace(/\u2029/g, '\\u2029')
}
