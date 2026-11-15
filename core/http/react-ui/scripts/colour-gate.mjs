#!/usr/bin/env node
/**
 * Colour ratchet.
 *
 * Colours come from the theme roles (--dk-* and the --color-* names in
 * src/theme.css), so one theme file restyles the whole app. This gate keeps
 * pages from growing new literal colours: a hex value, or an rgb(), rgba(),
 * hsl() or hsla() call with numbers in it. It does not forbid the literals
 * that already exist. A baseline file lists today's count per file, and the
 * gate fails when a file has more, and when the baseline lists more than the
 * file has, so the baseline can only shrink.
 *
 * Not scanned: src/vendor/ (generated kit), src/theme/ and
 * src/theme-localai.css (the palette itself), comments, and any line that
 * sets a mask (a mask only reads the alpha channel, so #000 is not a colour).
 * Allowed by name: the vendor colours in src/utils/format.js, which are the
 * logos of NVIDIA, AMD, Intel and Apple, not the product palette.
 *
 *   node scripts/colour-gate.mjs            # check against the baseline
 *   node scripts/colour-gate.mjs --write    # save the current state as baseline
 *   node scripts/colour-gate.mjs --report   # counts per file, worst first
 */
import { readFileSync, writeFileSync, readdirSync, statSync, existsSync } from 'node:fs'
import { join, relative, dirname, resolve, sep } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { compare, formatBaseline, parseBaseline } from './ui-kit-gate.mjs'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const BASELINE = join(ROOT, 'colour-baseline.txt')

const EXEMPT = ['src/vendor/', 'src/theme/', 'src/theme-localai.css']
const ALLOWED_FILES = ['src/utils/format.js']

// #rgb, #rrggbb, #rrggbbaa. Not after a word character, "&" (an HTML entity)
// or "/" (a URL), and not followed by more word characters ("#aed-sec-").
const HEX = /(?<![\w&/])#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{3,4})(?![\w-])/g
const FUNC = /\b(?:rgba?|hsla?)\(\s*[0-9.]/g
const MASK = /mask/i

function stripComments(path, src) {
  const blank = m => m.replace(/[^\n]/g, ' ')
  let out = src.replace(/\/\*[\s\S]*?\*\//g, blank)
  if (!path.endsWith('.css')) out = out.replace(/(^|\s)\/\/[^\n]*/g, (m, lead) => lead + blank(m.slice(lead.length)))
  return out
}

/** Finds the literal colours in one file. Returns [{ rule, line }]. */
export function scanFile(path, src) {
  if (EXEMPT.some(e => path === e || path.startsWith(e))) return []
  if (ALLOWED_FILES.includes(path)) return []
  const out = []
  stripComments(path, src).split('\n').forEach((line, i) => {
    if (MASK.test(line)) return
    const n = (line.match(HEX) || []).length + (line.match(FUNC) || []).length
    for (let k = 0; k < n; k++) out.push({ rule: 'colour', line: i + 1 })
  })
  return out
}

export function countAll(files) {
  const counts = new Map()
  for (const [path, src] of files) {
    for (const { rule } of scanFile(path, src)) {
      const key = `${rule} ${path}`
      counts.set(key, (counts.get(key) || 0) + 1)
    }
  }
  return counts
}

export { compare, formatBaseline, parseBaseline }

function walk(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) walk(full, out)
    else if (/\.(jsx?|css)$/.test(entry)) out.push(full)
  }
  return out
}

function loadFiles() {
  return walk(join(ROOT, 'src')).map(f => [relative(ROOT, f).split(sep).join('/'), readFileSync(f, 'utf8')])
}

const HINT = 'Use a role: var(--dk-accent), var(--color-error), color-mix(in srgb, var(--dk-text) 25%, transparent). A new colour belongs in the theme file, not in a page.'

function main() {
  const mode = process.argv[2]
  const current = countAll(loadFiles())
  const total = [...current.values()].reduce((a, b) => a + b, 0)

  if (mode === '--report') {
    for (const [k, n] of [...current].sort((a, b) => b[1] - a[1])) console.log(String(n).padStart(5), k.slice(7))
    console.log(String(total).padStart(5), 'TOTAL in', current.size, 'files')
    return
  }
  if (mode === '--write') {
    writeFileSync(BASELINE, formatBaseline(current))
    console.log(`Saved colour baseline: ${total} literal colours in ${current.size} files`)
    return
  }
  if (!existsSync(BASELINE)) {
    console.error(`No baseline at ${BASELINE}. Run with --write to create one.`)
    process.exit(1)
  }
  const { added, stale } = compare(current, parseBaseline(readFileSync(BASELINE, 'utf8')))
  if (added.length > 0) {
    console.error(`New literal colours (${added.length} files):`)
    for (const { key, was, now } of added) console.error(`  ${key.slice(7)}  ${was} -> ${now}`)
    console.error('')
    console.error(HINT)
    process.exit(1)
  }
  if (stale.length > 0) {
    console.error(`The baseline lists literal colours that are gone (${stale.length} files):`)
    for (const { key, was, now } of stale) console.error(`  ${key.slice(7)}  ${was} -> ${now}`)
    console.error('')
    console.error('Good. Refresh the baseline in this commit: npm run lint:colours:write')
    process.exit(1)
  }
  console.log(`Colours: ${total} literal colours in ${current.size} files, at baseline.`)
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) main()
