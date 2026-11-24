// V8 -> istanbul coverage harvest for the Playwright suite.
//
// When PW_V8_COVERAGE=1 the suite runs against a NON-instrumented build (built
// with COVERAGE_V8=true, which only adds source maps). Chromium collects native
// V8 coverage with near-zero runtime overhead; we convert it back to per-source
// istanbul data via v8-to-istanbul (using the on-disk source maps), filter to
// src/**, and write the same .nyc_output/*.json the istanbul path produced — so
// `nyc report` and the strict baseline gate are unchanged.
//
// Conversion (v8-to-istanbul load() parses the large bundle source map) is the
// expensive part, so we do NOT convert per test. Instead each worker collects
// raw V8 coverage from every test, merges it with @bcoe/v8-coverage (which sums
// counts and reconciles overlapping ranges correctly — applyCoverage can't be
// called repeatedly, it pushes/overwrites), and converts ONCE at worker
// teardown. That cuts conversions from ~152 (per test) to ~1 per worker.
import v8toIstanbul from 'v8-to-istanbul'
import libCoverage from 'istanbul-lib-coverage'
import { mergeProcessCovs } from '@bcoe/v8-coverage'
import { mkdirSync, writeFileSync, existsSync } from 'node:fs'
import { randomUUID } from 'node:crypto'
import path from 'node:path'

const COVERAGE_DIR = path.resolve(process.cwd(), '.nyc_output')
const DIST_ASSETS = path.resolve(process.cwd(), 'dist', 'assets')
// Absolute app source dir. Match on this (not a bare "/src/" substring) — the
// repo itself lives under .../go/src/..., so a substring check would collide.
const SRC_DIR = path.resolve(process.cwd(), 'src') + path.sep
// Only our own bundle chunks under /assets/*.js carry app source maps.
const APP_CHUNK = /\/assets\/([^/?]+\.js)(\?|$)/

export async function startV8(page) {
  // resetOnNavigation:false so hard navigations (goto) within a test accumulate.
  await page.coverage.startJSCoverage({ resetOnNavigation: false })
}

// One accumulator per worker (created by the worker-scoped fixture).
export function createAccumulator() {
  const processCovs = []

  return {
    // Called on each test teardown with that test's V8 coverage entries.
    add(entries) {
      const result = entries
        .filter((e) => APP_CHUNK.test(e.url))
        // Keep only structural fields (drop the ~1MB `source` per entry — it's
        // re-read from disk at convert time — to bound per-worker memory).
        .map((e) => ({ scriptId: e.scriptId || e.url, url: e.url, functions: e.functions }))
      if (result.length) processCovs.push({ result })
    },

    // Called once at worker teardown: merge all tests' coverage, convert, write.
    async flush() {
      if (processCovs.length === 0) return
      const merged = mergeProcessCovs(processCovs)
      const map = libCoverage.createCoverageMap({})

      for (const script of merged.result) {
        const m = APP_CHUNK.exec(script.url)
        if (!m) continue
        const diskPath = path.join(DIST_ASSETS, m[1])
        if (!existsSync(diskPath)) continue

        // v8-to-istanbul auto-loads source + sibling .map from disk; the served
        // bytes match dist, so the V8 ranges line up.
        const converter = v8toIstanbul(diskPath, 0)
        try {
          await converter.load()
          converter.applyCoverage(script.functions)
          const data = converter.toIstanbul()
          for (const [key, fileCov] of Object.entries(data)) {
            // v8-to-istanbul keys are already absolute; keep only app sources.
            if (!key.startsWith(SRC_DIR) || key.includes(`${path.sep}node_modules${path.sep}`)) continue
            map.merge({ [key]: fileCov })
          }
        } catch {
          // skip a chunk we couldn't convert
        } finally {
          converter.destroy()
        }
      }

      const json = map.toJSON()
      if (Object.keys(json).length === 0) return
      mkdirSync(COVERAGE_DIR, { recursive: true })
      writeFileSync(path.join(COVERAGE_DIR, `v8-${randomUUID()}.json`), JSON.stringify(json))
    },
  }
}
