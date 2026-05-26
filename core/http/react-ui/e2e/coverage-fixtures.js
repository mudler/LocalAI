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

export const test = base.extend({
  page: async ({ page }, use) => {
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

export { expect }
