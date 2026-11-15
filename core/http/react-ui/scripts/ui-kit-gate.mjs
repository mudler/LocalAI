#!/usr/bin/env node
/**
 * UI kit ratchet.
 *
 * The kit in src/components/ui/ holds the shared controls. This gate keeps
 * pages from growing new copies of them. It does not forbid the old code that
 * already exists: a baseline file lists today's violations, and the gate fails
 * on any new one. It also fails when the baseline lists a violation that is
 * gone, so the baseline can only shrink. Fix some, then refresh it.
 *
 * Rules (src/components/ui/, src/vendor/ and SideSheet.jsx are exempt):
 *   checkbox     an <input type="checkbox"> without the kit's dk-check class
 *   toggle       an import of the legacy src/components/Toggle
 *   sheet-jsx    the class name dk-sheet in JSX (use SideSheet)
 *   width        a CSS width on a sheet or pane (width, min-width,
 *                max-width or --pane-w)
 *   btn          the legacy .btn class in JSX (use Button)
 *
 *   node scripts/ui-kit-gate.mjs            # check against the baseline
 *   node scripts/ui-kit-gate.mjs --write    # save the current state as baseline
 *   node scripts/ui-kit-gate.mjs --report   # counts per rule and per file
 */
import { readFileSync, writeFileSync, readdirSync, statSync, existsSync } from 'node:fs'
import { join, relative, dirname, resolve, sep } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const BASELINE = join(ROOT, 'ui-kit-baseline.txt')
const RULES = ['checkbox', 'toggle', 'sheet-jsx', 'width', 'btn']

// Files that may hold what the rules forbid elsewhere: the kit, the vendored
// kit CSS, and the one component that draws the sheet markup for everyone.
const EXEMPT = ['src/components/ui/', 'src/vendor/', 'src/components/SideSheet.jsx']
const TOGGLE = 'src/components/Toggle'

// A whole <input ...> tag. "=>" inside a handler must not end it early.
const INPUT_TAG = /<input\b(?:=>|[^>])*?\/?>/g
const CHECKBOX_TYPE = /\btype\s*=\s*(?:"checkbox"|'checkbox'|\{\s*["']checkbox["']\s*\})/
const KIT_CHECK = /(?<![\w-])dk-check(?![\w-])/
const IMPORT = /(?:\bfrom\s+|\bimport\s*\(\s*|\bimport\s+)(["'])([^"']+)\1/g
const SHEET_CLASS = /(?<![\w-])dk-sheet(?![\w-])/g
// A quoted string on a line that sets a class: a .btn or .btn-* token.
const BTN_TOKEN = /(["'`])[^"'`\n]*(?<![\w-])btn(?:-[a-z-]+)?(?![\w-])[^"'`\n]*\1/g
const NON_CLASS_ATTR = /(?<![\w-])(?:data-[\w-]+|testId|id|name|key|to|href)=(?:"[^"]*"|'[^']*')/g
const CSS_RULE = /([^{}]+)\{([^{}]*)\}/g
// A selector that names a sheet or a pane: the kit classes, or a page class
// that ends in -sheet (lib-sheet, ledger-sheet). A page class that only ends
// in -pane (lib-pane) is a layout column, not the kit pane, so it is skipped.
const SHEET_SELECTOR = /\.(?:dk-sheet(?:--[a-z]+)?|ui-pane(?:--[a-z]+)?|[a-z][\w]*(?:-[\w]+)*-sheet)(?![\w-])/
const WIDTH_DECL = /(?:^|[;\s])(?:width|min-width|max-width|--pane-w)\s*:/

function stripComments(css) {
  return css.replace(/\/\*[\s\S]*?\*\//g, m => m.replace(/[^\n]/g, ' '))
}

function lineOf(src, index) {
  return src.slice(0, index).split('\n').length
}

/**
 * Finds the violations in one file. `path` is relative to the react-ui folder
 * with forward slashes. Returns [{ rule, line }].
 */
export function scanFile(path, src) {
  if (EXEMPT.some(e => path.startsWith(e))) return []
  const out = []
  if (/\.jsx?$/.test(path)) {
    for (const m of src.matchAll(INPUT_TAG)) {
      if (CHECKBOX_TYPE.test(m[0]) && !KIT_CHECK.test(m[0])) out.push({ rule: 'checkbox', line: lineOf(src, m.index) })
    }
    for (const m of src.matchAll(IMPORT)) {
      const spec = m[2]
      if (!spec.startsWith('.')) continue
      const target = join(dirname(path), spec).split(sep).join('/').replace(/\.jsx?$/, '')
      if (target === TOGGLE) out.push({ rule: 'toggle', line: lineOf(src, m.index) })
    }
    for (const m of src.matchAll(SHEET_CLASS)) out.push({ rule: 'sheet-jsx', line: lineOf(src, m.index) })
    for (const line of src.split('\n').entries()) {
      const [i, raw] = line
      // Ids and test hooks such as data-testid="btn-save" are not classes.
      const text = raw.replace(NON_CLASS_ATTR, '')
      if (!/className|class=|Class\b/.test(text)) continue
      // One violation per line, however many btn tokens the line holds.
      for (const m of text.matchAll(BTN_TOKEN)) {
        if (/(?<![\w-])btn(?:-[a-z-]+)?(?![\w-])/.test(m[0])) { out.push({ rule: 'btn', line: i + 1 }); break }
      }
    }
  } else if (path.endsWith('.css')) {
    const css = stripComments(src)
    for (const m of css.matchAll(CSS_RULE)) {
      // A rule inside @media has its selector after the last "{"-free chunk,
      // which CSS_RULE already isolates, so strip any leftover at-rule head.
      const selector = m[1].replace(/@[^{]*$/, '')
      if (SHEET_SELECTOR.test(selector) && WIDTH_DECL.test(m[2])) {
        out.push({ rule: 'width', line: lineOf(css, m.index + m[0].indexOf('{')) })
      }
    }
  }
  return out
}

/** Counts violations as a Map of "rule file" -> number. */
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

export function formatBaseline(counts) {
  return [...counts].sort(([a], [b]) => (a < b ? -1 : 1)).map(([k, n]) => `${k} ${n}`).join('\n') + (counts.size ? '\n' : '')
}

export function parseBaseline(text) {
  const counts = new Map()
  for (const raw of text.split('\n')) {
    const line = raw.trim()
    if (!line || line.startsWith('#')) continue
    const m = line.match(/^(\S+) (\S+) (\d+)$/)
    if (!m) throw new Error(`Bad baseline line: ${line}`)
    counts.set(`${m[1]} ${m[2]}`, Number(m[3]))
  }
  return counts
}

/** Compares the current counts with the baseline. */
export function compare(current, baseline) {
  const added = []
  const stale = []
  for (const [key, n] of current) {
    const was = baseline.get(key) || 0
    if (n > was) added.push({ key, was, now: n })
  }
  for (const [key, was] of baseline) {
    const now = current.get(key) || 0
    if (now < was) stale.push({ key, was, now })
  }
  return { added, stale }
}

export function perRule(counts) {
  const totals = Object.fromEntries(RULES.map(r => [r, 0]))
  for (const [key, n] of counts) totals[key.split(' ')[0]] += n
  return totals
}

function walk(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) walk(full, out)
    else if (/\.(jsx?|css)$/.test(entry)) out.push(full)
  }
  return out
}

function loadFiles() {
  const base = join(ROOT, 'src')
  return walk(base).map(f => [relative(ROOT, f).split(sep).join('/'), readFileSync(f, 'utf8')])
}

const HINTS = {
  checkbox: 'Add the kit class: <input type="checkbox" className="dk-check">. Use Switch for a setting that applies at once.',
  toggle: 'components/Toggle is legacy. Use Switch from components/ui.',
  'sheet-jsx': 'Use SideSheet (components/SideSheet.jsx). Do not write dk-sheet markup.',
  width: 'A sheet is 480 px (wide 720 px) and a pane is 360 px (wide 400 px). Do not override the width.',
  btn: 'Use Button from components/ui (or buttonClass for markup that cannot).',
}

function main() {
  const mode = process.argv[2]
  const current = countAll(loadFiles())

  if (mode === '--report') {
    const totals = perRule(current)
    for (const r of RULES) console.log(String(totals[r]).padStart(5), r)
    console.log(String(Object.values(totals).reduce((a, b) => a + b, 0)).padStart(5), 'TOTAL in', current.size, 'rule/file pairs')
    return
  }

  if (mode === '--write') {
    writeFileSync(BASELINE, formatBaseline(current))
    const totals = perRule(current)
    console.log(`Saved UI kit baseline: ${RULES.map(r => `${r} ${totals[r]}`).join(', ')}`)
    return
  }

  if (!existsSync(BASELINE)) {
    console.error(`No baseline at ${BASELINE}. Run with --write to create one.`)
    process.exit(1)
  }
  const { added, stale } = compare(current, parseBaseline(readFileSync(BASELINE, 'utf8')))
  if (added.length > 0) {
    console.error(`New UI kit violations (${added.length}):`)
    for (const { key, was, now } of added) console.error(`  ${key}  ${was} -> ${now}`)
    console.error('')
    for (const r of new Set(added.map(a => a.key.split(' ')[0]))) console.error(`${r}: ${HINTS[r]}`)
    process.exit(1)
  }
  if (stale.length > 0) {
    console.error(`The baseline lists violations that are gone (${stale.length}):`)
    for (const { key, was, now } of stale) console.error(`  ${key}  ${was} -> ${now}`)
    console.error('')
    console.error('Good. Refresh the baseline in this commit: npm run lint:ui-kit:write')
    process.exit(1)
  }
  const totals = perRule(current)
  console.log(`UI kit: ${RULES.map(r => `${r} ${totals[r]}`).join(', ')}, at baseline.`)
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) main()
