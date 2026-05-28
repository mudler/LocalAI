// Playwright test fixture that harvests istanbul code coverage.
//
// When the app is built with COVERAGE=true (see vite.config.js), every source
// module is instrumented and exposes its counters on window.__coverage__. This
// fixture writes that object to .nyc_output/ after each test so `nyc report`
// can merge the runs into a per-file coverage report.
//
// The app is a React SPA (client-side routing), so window.__coverage__
// accumulates across in-app navigation within a single test; only a full page
// reload / fresh page.goto resets it. Specs import { test, expect } from this
// module instead of '@playwright/test' so collection is automatic.
import { test as base, expect } from '@playwright/test'
import { mkdirSync, writeFileSync } from 'node:fs'
import { randomUUID } from 'node:crypto'
import path from 'node:path'

const COVERAGE_DIR = path.resolve(process.cwd(), '.nyc_output')
const V8_COVERAGE = process.env.PW_V8_COVERAGE === '1'

const withCoverage = base.extend({
  // Worker-scoped V8 coverage accumulator: collects every test's native
  // Chromium coverage and converts it to istanbul ONCE at worker teardown
  // (conversion is expensive; see e2e/v8-coverage.js). null when V8 mode is off.
  _v8acc: [
    async ({}, use) => {
      if (!V8_COVERAGE) {
        await use(null)
        return
      }
      const { createAccumulator } = await import('./v8-coverage.js')
      const acc = createAccumulator()
      await use(acc)
      await acc.flush()
    },
    { scope: 'worker' },
  ],

  page: async ({ page, _v8acc }, use) => {
    // V8 coverage path: collect native Chromium coverage (cheap), hand it to the
    // worker accumulator on teardown. Avoids running an instrumented bundle.
    if (V8_COVERAGE) {
      const { startV8 } = await import('./v8-coverage.js')
      await startV8(page)
      await use(page)
      try {
        _v8acc.add(await page.coverage.stopJSCoverage())
      } catch {
        // page already closed — nothing to collect
      }
      return
    }

    await use(page)

    let coverage
    try {
      coverage = await page.evaluate(() => window.__coverage__)
    } catch {
      // Page was already closed by the test — nothing to collect.
      return
    }
    if (!coverage) return // build wasn't instrumented (COVERAGE unset)

    mkdirSync(COVERAGE_DIR, { recursive: true })
    writeFileSync(
      path.join(COVERAGE_DIR, `playwright-${randomUUID()}.json`),
      JSON.stringify(coverage),
    )
  },
})

export const test = withCoverage
export { expect }
