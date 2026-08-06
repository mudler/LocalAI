#!/usr/bin/env node
/**
 * Inline-style ratchet.
 *
 * The React UI ships a full design system (tokens, form grids, data tables,
 * stat cards, callouts) that the pages largely bypassed: at the time this gate
 * was written there were ~1,700 `style={{...}}` literals across src/pages and
 * src/components. Each one is a spacing or colour decision made locally, so no
 * two pages share a rhythm, which is the main reason the app reads as
 * unfinished rather than as one product.
 *
 * This gate does not try to forbid inline styles. Some are legitimate: a width
 * driven by a runtime percentage, a CSS custom property passed to a chart. It
 * only enforces that the total never goes UP, the same ratchet discipline as
 * the coverage baseline. Converting a page lowers the number; you then refresh
 * the baseline in the same commit.
 *
 *   node scripts/inline-style-gate.mjs            # check against baseline
 *   node scripts/inline-style-gate.mjs --write    # save current as baseline
 *   node scripts/inline-style-gate.mjs --report   # per-file counts, worst first
 */
import { readFileSync, writeFileSync, readdirSync, statSync, existsSync } from 'node:fs'
import { join, relative, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const SCAN = ['src/pages', 'src/components']
const BASELINE = join(ROOT, 'inline-style-baseline.txt')
const PATTERN = /style=\{\{/g

function walk(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) walk(full, out)
    else if (entry.endsWith('.jsx') || entry.endsWith('.js')) out.push(full)
  }
  return out
}

/* Two className attributes on one element is always a bug: JSX keeps the last
   and silently drops the first, so an `<i className={icon} className="text-xs">`
   loses its icon. This is exactly what a careless style-to-class conversion
   produces, so the check lives next to the thing that causes it. eslint would
   catch it via react/jsx-props-no-duplicate-props, but that needs
   eslint-plugin-react, which this project does not depend on. */
const DUPE = /className=(?:\{(?:[^{}]|\{(?:[^{}]|\{[^{}]*\})*\})*\}|"[^"]*")(?:\s+[a-zA-Z-]+=(?:"[^"]*"|\{[^{}]*\}))*\s+className=/g

const counts = []
const dupes = []
let total = 0
for (const rel of SCAN) {
  const dir = join(ROOT, rel)
  if (!existsSync(dir)) continue
  for (const file of walk(dir)) {
    const src = readFileSync(file, 'utf8')
    const n = (src.match(PATTERN) || []).length
    if (n > 0) counts.push([relative(ROOT, file), n])
    total += n
    for (const m of src.matchAll(DUPE)) {
      dupes.push([relative(ROOT, file), src.slice(0, m.index).split('\n').length])
    }
  }
}
counts.sort((a, b) => b[1] - a[1])

if (dupes.length > 0 && process.argv[2] !== '--report') {
  console.error(`Duplicate className attributes (${dupes.length}). JSX keeps the last and drops the first:`)
  for (const [file, line] of dupes.slice(0, 20)) console.error(`  ${file}:${line}`)
  console.error('')
  console.error('Merge them: className={`${expr} the-class`}')
  process.exit(1)
}

const mode = process.argv[2]

if (mode === '--report') {
  for (const [file, n] of counts) console.log(String(n).padStart(4), file)
  console.log(String(total).padStart(4), 'TOTAL across', counts.length, 'files')
  process.exit(0)
}

if (mode === '--write') {
  writeFileSync(BASELINE, `${total}\n`)
  console.log(`Saved inline-style baseline: ${total}`)
  process.exit(0)
}

if (!existsSync(BASELINE)) {
  console.error(`No baseline at ${BASELINE}. Run with --write to create one.`)
  process.exit(1)
}

const baseline = Number(readFileSync(BASELINE, 'utf8').trim())
if (Number.isNaN(baseline)) {
  console.error(`Baseline file is not a number: ${BASELINE}`)
  process.exit(1)
}

if (total > baseline) {
  console.error(`Inline styles went UP: ${total} (baseline ${baseline}, +${total - baseline}).`)
  console.error('')
  console.error('Use a class from src/App.css instead. The layout and text primitives')
  console.error('(.stack, .hstack, .text-note, .text-meta, .loading-center) cover most')
  console.error('cases; give genuinely page-specific shapes their own named class.')
  console.error('')
  console.error('Worst files right now:')
  for (const [file, n] of counts.slice(0, 5)) console.error(`  ${String(n).padStart(4)} ${file}`)
  process.exit(1)
}

if (total < baseline) {
  console.log(`Inline styles: ${total} (baseline ${baseline}, -${baseline - total}).`)
  console.log('Refresh the baseline in this commit: npm run lint:inline-styles:write')
  process.exit(0)
}

console.log(`Inline styles: ${total}, at baseline.`)
