import { defineConfig } from '@playwright/test'
import base from './playwright.config.js'

// The shared pages spec runs against a real server started with
// --shared-pages-fixture, which seeds shared folders and their pages, on its
// own port. SHARED_PAGES_UI_PORT and SHARED_PAGES_UI_LOG change the port and the
// server log.
const port = Number(process.env.SHARED_PAGES_UI_PORT || 8098)

export default defineConfig({
  ...base,
  testIgnore: [],
  testMatch: ['**/shared-pages-real.spec.js'],
  workers: 1,
  use: { ...base.use, baseURL: `http://127.0.0.1:${port}` },
  webServer: process.env.PLAYWRIGHT_EXTERNAL_SERVER ? undefined : {
    command: `../../../tests/e2e-ui/ui-test-server --mock-backend=../../../tests/e2e/mock-backend/mock-backend --port=${port} --shared-pages-fixture > ${process.env.SHARED_PAGES_UI_LOG || '/dev/null'} 2>&1`,
    port,
    timeout: 120_000,
    reuseExistingServer: false,
  },
})
