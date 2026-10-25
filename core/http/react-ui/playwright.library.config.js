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
