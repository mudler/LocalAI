import { defineConfig } from '@playwright/test'

// Configuration of the add-on update and upgrade suite (scripts/addons/updates.sh).
// The runner starts a UI test server with a real add-on owner, the real Tables
// executable and the built UI package, with named accounts (ADDONS_UI_URL,
// seeded accounts and real sessions in ADDONS_FIXTURE). The specs change the
// owner's state, so they run in order on one worker.
export default defineConfig({
  testDir: './e2e',
  testMatch: ['addons-upgrade.spec.js'],
  timeout: 180_000,
  retries: 0,
  workers: 1,
  fullyParallel: false,
  reporter: [['json', { outputFile: process.env.ADDONS_UI_REPORT || 'addons-upgrade-report.json' }]],
  use: {
    baseURL: process.env.ADDONS_UI_URL,
  },
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
