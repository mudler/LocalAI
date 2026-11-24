import { defineConfig } from '@playwright/test'

// Configuration of the Tables acceptance browser spec
// (scripts/addons/tables_acceptance.sh). The runner starts the acceptance
// cluster, one owner and two frontends with real authentication and the
// packaged Tables, and passes its ready file in ADDONS_TABLES_CLUSTER. The
// specs stop and start the owner, so they run in order on one worker.
export default defineConfig({
  testDir: './e2e',
  testMatch: ['tables-acceptance.spec.js'],
  timeout: 120_000,
  retries: 0,
  workers: 1,
  fullyParallel: false,
  reporter: [['json', { outputFile: process.env.ADDONS_UI_REPORT || 'tables-acceptance-report.json' }]],
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
