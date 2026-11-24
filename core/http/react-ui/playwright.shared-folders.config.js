import { defineConfig } from '@playwright/test'
import base from './playwright.config.js'

// The shared folders spec runs against a real server started with
// --shared-pages-fixture, on its own port, because it changes the grants of
// the fixture's folders. SHARED_FOLDERS_UI_PORT and SHARED_FOLDERS_UI_LOG
// change the port and the server log; SHARED_FOLDERS_SHOTS the folder of the
// screenshots.
const port = Number(process.env.SHARED_FOLDERS_UI_PORT || 8099)

export default defineConfig({
  ...base,
  testIgnore: [],
  testMatch: ['**/shared-folders-real.spec.js'],
  workers: 1,
  use: { ...base.use, baseURL: `http://127.0.0.1:${port}` },
  webServer: process.env.PLAYWRIGHT_EXTERNAL_SERVER ? undefined : {
    command: `../../../tests/e2e-ui/ui-test-server --mock-backend=../../../tests/e2e/mock-backend/mock-backend --port=${port} --shared-pages-fixture > ${process.env.SHARED_FOLDERS_UI_LOG || '/dev/null'} 2>&1`,
    port,
    timeout: 120_000,
    reuseExistingServer: false,
  },
})
