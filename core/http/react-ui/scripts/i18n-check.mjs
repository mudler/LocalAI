#!/usr/bin/env node
/**
 * Locale completeness ratchet.
 *
 * English (public/locales/en) is the source of truth. Every other locale must
 * have every namespace file and every key of English. `fallbackLng: 'en'`
 * hides a gap at run time, so a locale that never got a new page shows English
 * text with no error anywhere. This gate makes the gap visible in CI.
 *
 * Existing gaps are recorded in i18n-baseline.json as a count of missing keys
 * per locale and namespace (a missing file counts all its keys). The gate
 * only lets that number go down:
 *
 *   - a locale and namespace with more missing keys than the baseline fails;
 *   - one with fewer fails too, until you lower the baseline, so a fixed gap
 *     cannot come back unnoticed.
 *
 *   node scripts/i18n-check.mjs            # check against the baseline
 *   node scripts/i18n-check.mjs --write    # save the current gaps as baseline
 *   node scripts/i18n-check.mjs --report   # list every missing key
 */
import { readFileSync, writeFileSync, readdirSync, existsSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const LOCALES = join(ROOT, 'public/locales')
const BASELINE = join(ROOT, 'i18n-baseline.json')
const SOURCE = 'en'

// A leaf is a string, number, boolean or null; objects nest and arrays are
// leaves, which is how i18next treats them with returnObjects.
function leaves(value, prefix = '', out = []) {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    for (const [key, child] of Object.entries(value)) leaves(child, prefix ? `${prefix}.${key}` : key, out)
  } else {
    out.push(prefix)
  }
  return out
}

function readKeys(file) {
  return new Set(leaves(JSON.parse(readFileSync(file, 'utf8'))))
}

const dirs = (path) => readdirSync(path, { withFileTypes: true }).filter((e) => e.isDirectory()).map((e) => e.name).sort()
const namespaces = (locale) => readdirSync(join(LOCALES, locale)).filter((f) => f.endsWith('.json')).map((f) => f.slice(0, -5)).sort()

/** Returns { 'locale/namespace': [missing keys] } for every gap. */
export function gaps() {
  const found = {}
  const source = Object.fromEntries(namespaces(SOURCE).map((ns) => [ns, readKeys(join(LOCALES, SOURCE, `${ns}.json`))]))
  for (const locale of dirs(LOCALES).filter((l) => l !== SOURCE)) {
    for (const [ns, keys] of Object.entries(source)) {
      const file = join(LOCALES, locale, `${ns}.json`)
      const have = existsSync(file) ? readKeys(file) : new Set()
      const missing = [...keys].filter((k) => !have.has(k)).sort()
      if (missing.length) found[`${locale}/${ns}`] = missing
    }
  }
  return found
}

function main(argv) {
  const found = gaps()
  const counts = Object.fromEntries(Object.entries(found).map(([name, keys]) => [name, keys.length]))
  if (argv.includes('--report')) {
    for (const [name, keys] of Object.entries(found)) console.log(`${name}: ${keys.length} missing\n  ${keys.join('\n  ')}`)
    return 0
  }
  if (argv.includes('--write')) {
    writeFileSync(BASELINE, JSON.stringify(counts, null, 2) + '\n')
    console.log(`i18n baseline written: ${Object.keys(counts).length} locale namespaces with gaps`)
    return 0
  }
  const baseline = existsSync(BASELINE) ? JSON.parse(readFileSync(BASELINE, 'utf8')) : {}
  const problems = []
  for (const name of new Set([...Object.keys(counts), ...Object.keys(baseline)])) {
    const now = counts[name] || 0
    const was = baseline[name] || 0
    if (now > was) {
      const sample = (found[name] || []).slice(0, 5).join(', ')
      problems.push(`${name}: ${now} keys missing, baseline allows ${was} (for example ${sample}). Add the keys to the locale.`)
    } else if (now < was) {
      problems.push(`${name}: ${now} keys missing, baseline says ${was}. Lower the baseline with: node scripts/i18n-check.mjs --write`)
    }
  }
  if (problems.length) {
    console.error(`i18n completeness failed:\n${problems.map((p) => '  ' + p).join('\n')}`)
    return 1
  }
  const total = Object.values(counts).reduce((a, b) => a + b, 0)
  console.log(`i18n: no new gaps (${total} keys missing in ${Object.keys(counts).length} locale namespaces, all in the baseline).`)
  return 0
}

if (process.argv[1] === fileURLToPath(import.meta.url)) process.exit(main(process.argv.slice(2)))
