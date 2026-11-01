import { defineConfig } from '@playwright/test'
import base from './playwright.config.js'

// The library sources spec runs against a real server that starts like a normal
// install (default galleries and default sources of the three kinds), on its
// own port, with a fresh data directory. LIBRARY_UI_PORT and LIBRARY_UI_LOG
// change the port and the server log.
const port = Number(process.env.LIBRARY_UI_PORT || 8097)

export default defineConfig({
  ...base,
  testIgnore: [],
  // The Models and Backends pages are still fetching their catalogs (a few
  // requests per host at a time) when the Libraries panel opens, so a list can
  // wait behind them for longer than the default five seconds.
  expect: { timeout: 20_000 },
  timeout: 90_000,
  // A retry that passes is a flake, and the runner (scripts/addons/library_ui.sh)
  // rejects it; it reads this report.
  retries: 0,
  workers: 1,
  reporter: process.env.ADDONS_UI_REPORT ? [['json', { outputFile: process.env.ADDONS_UI_REPORT }]] : base.reporter,
  testMatch: ['**/library-sources.spec.js'],
  globalSetup: undefined,
  use: { ...base.use, baseURL: `http://127.0.0.1:${port}` },
  webServer: process.env.PLAYWRIGHT_EXTERNAL_SERVER ? undefined : {
    command: `../../../tests/e2e-ui/ui-test-server --mock-backend=../../../tests/e2e/mock-backend/mock-backend --port=${port} --library-sources > ${process.env.LIBRARY_UI_LOG || '/dev/null'} 2>&1`,
    port,
    timeout: 120_000,
    reuseExistingServer: false,
  },
})
