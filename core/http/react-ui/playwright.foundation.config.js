import { defineConfig } from '@playwright/test'

// Configuration of the foundation acceptance suite
// (scripts/addons/foundation.sh). The runner starts the foundation cluster, one
// owner and two frontends with real authentication, and passes the ready file
// in ADDONS_FOUNDATION. The specs end sessions and change the add-on's state,
// so they run in order on one worker.
export default defineConfig({
  testDir: './e2e',
  testMatch: ['addons-foundation.spec.js'],
  timeout: 90_000,
  retries: 0,
  workers: 1,
  fullyParallel: false,
  reporter: [['json', { outputFile: process.env.ADDONS_UI_REPORT || 'addons-foundation-report.json' }]],
  projects: [
    {
      name: 'chromium',
      use: {
        browserName: 'chromium',
        ...(process.env.PLAYWRIGHT_CHROMIUM_PATH
          ? { launchOptions: { executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH } }
          : {}),
      },
    },
  ],
})
